package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"strings"
	"time"
)

// Slanje e-pošte je **opciono**, isti princip kao za S3: ako ključ nije
// postavljen, aplikacija radi kao ranije i ništa se ne šalje.
//
// Zašto HTTP API a ne SMTP: OCI Always Free podrazumevano blokira odlazni
// TCP 25, a i 587 ume da bude zatvoren. Resend/Brevo HTTP API ide preko 443,
// pa nema otvaranja portova ni održavanja SMTP relay-a.
//
// Konfiguracija (env):
//
//	MAIL_PROVIDER   smtp | resend   (default: smtp)
//	MAIL_FROM       "Wolken-ASM Reports <noreply@wolken-asm.de>"
//	MAIL_SMTP_ADDR  smtp.gmail.com:587 (STARTTLS)
//	MAIL_SMTP_USER  adresa naloga koji šalje (mora biti From ili njegov alias)
//	MAIL_SMTP_PASS  Google app password (2FA mora biti uključen)
//	MAIL_API_KEY    Resend ključ (samo za MAIL_PROVIDER=resend)
//	MAIL_ADMIN_TO   opciono: fiksni primaoci za admin obaveštenja (CSV);
//	                ako nije postavljeno, koriste se svi aktivni admini
//	APP_BASE_URL    npr. https://ha.wolken-asm.de (za linkove u porukama)
//	MAIL_API_BASE   opciono: override endpointa (koristi se u testovima)
const defaultMailAPIBase = "https://api.resend.com"

type mailer struct {
	provider string
	apiKey   string
	from     string
	apiBase  string
	appURL   string
	adminTo  []string
	smtpAddr string
	smtpUser string
	smtpPass string
	client   *http.Client
}

var mail *mailer

// NewMailerFromEnv vraća mailer ili nil kada slanje nije konfigurisano.
func NewMailerFromEnv() *mailer {
	m := &mailer{
		provider: strings.ToLower(strings.TrimSpace(os.Getenv("MAIL_PROVIDER"))),
		apiKey:   strings.TrimSpace(os.Getenv("MAIL_API_KEY")),
		from:     strings.TrimSpace(os.Getenv("MAIL_FROM")),
		apiBase:  strings.TrimRight(strings.TrimSpace(os.Getenv("MAIL_API_BASE")), "/"),
		appURL:   strings.TrimRight(strings.TrimSpace(os.Getenv("APP_BASE_URL")), "/"),
		smtpAddr: strings.TrimSpace(os.Getenv("MAIL_SMTP_ADDR")),
		smtpUser: strings.TrimSpace(os.Getenv("MAIL_SMTP_USER")),
		smtpPass: os.Getenv("MAIL_SMTP_PASS"),
		client:   &http.Client{Timeout: 10 * time.Second},
	}
	if m.provider == "" {
		m.provider = "smtp"
	}
	if m.smtpAddr == "" {
		m.smtpAddr = "smtp.gmail.com:587"
	}
	if m.apiBase == "" {
		m.apiBase = defaultMailAPIBase
	}
	for _, a := range strings.Split(os.Getenv("MAIL_ADMIN_TO"), ",") {
		if a = strings.TrimSpace(a); a != "" {
			m.adminTo = append(m.adminTo, a)
		}
	}
	if !m.Enabled() {
		log.Printf("mail: disabled (%s: nedostaje konfiguracija) — no e-mail will be sent", m.provider)
		return nil
	}
	log.Printf("mail: enabled via %s, from %q", m.provider, m.from)
	return m
}

// Enabled: `smtp` traži nalog i app password, `resend` traži API ključ. U oba
// slučaja `MAIL_FROM` je obavezan.
func (m *mailer) Enabled() bool {
	if m == nil || m.from == "" {
		return false
	}
	if strings.EqualFold(m.provider, "smtp") {
		return m.smtpUser != "" && m.smtpPass != ""
	}
	return m.apiKey != ""
}

// Send šalje poruku u pozadini: registracija i admin akcije ne smeju da čekaju
// provajdera, a greška u slanju ne sme da obori zahtev.
func (m *mailer) Send(to []string, subject, text string) {
	if !m.Enabled() {
		return
	}
	to = dedupeEmails(to)
	if len(to) == 0 {
		return
	}
	go func() {
		if err := m.send(to, subject, text); err != nil {
			// Sadržaj poruke se ne loguje (može sadržati jednokratni link).
			log.Printf("mail: slanje na %d primaoca nije uspelo: %v", len(to), err)
		}
	}()
}

func (m *mailer) send(to []string, subject, text string) error {
	switch strings.ToLower(m.provider) {
	case "smtp":
		return m.sendSMTP(to, subject, text)
	case "resend":
		return m.sendResend(to, subject, text)
	}
	return fmt.Errorf("nepoznat MAIL_PROVIDER %q", m.provider)
}

// buildMessage pravi MIME poruku (text/plain, UTF-8). Telo je base64 jer
// poruke sadrže naša slova (č/ć/ž/š/đ).
func buildMessage(from string, to []string, subject, text string) []byte {
	var b bytes.Buffer
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n")
	b.WriteString("Auto-Submitted: auto-generated\r\n\r\n")

	enc := base64.StdEncoding.EncodeToString([]byte(text))
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	if enc != "" {
		b.WriteString(enc + "\r\n")
	}
	return b.Bytes()
}

// sendSMTP šalje preko STARTTLS (587). Port 25 se ne koristi — OCI ga blokira.
// Envelope sender je nalog koji se autentifikuje; From header sme biti njegov
// alias (Google to proverava).
func (m *mailer) sendSMTP(to []string, subject, text string) error {
	host, _, err := net.SplitHostPort(m.smtpAddr)
	if err != nil {
		return fmt.Errorf("neispravan MAIL_SMTP_ADDR: %w", err)
	}
	auth := smtp.PlainAuth("", m.smtpUser, m.smtpPass, host)
	msg := buildMessage(m.from, to, subject, text)
	if err := smtp.SendMail(m.smtpAddr, auth, m.smtpUser, to, msg); err != nil {
		return err
	}
	return nil
}

// sendResend šalje preko https://resend.com/docs/api-reference/emails/send-email
func (m *mailer) sendResend(to []string, subject, text string) error {
	payload, err := json.Marshal(map[string]any{
		"from":    m.from,
		"to":      to,
		"subject": subject,
		"text":    text,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, m.apiBase+"/emails", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func dedupeEmails(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, e := range in {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" || seen[e] || !strings.Contains(e, "@") {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	return out
}

func (m *mailer) link(path string) string {
	if m.appURL == "" {
		return path
	}
	return m.appURL + path
}

// --- Primaoci ---

// adminRecipients: MAIL_ADMIN_TO ako je postavljen, inače svi aktivni admini.
func adminRecipients() []string {
	if mail.Enabled() && len(mail.adminTo) > 0 {
		return mail.adminTo
	}
	var out []string
	_ = db.Select(&out, `SELECT email FROM users
		WHERE role = 'admin' AND coalesce(status, 'active') = 'active'
		ORDER BY id`)
	return out
}

// orgAdminRecipients: aktivni organizacijski admini date organizacije.
func orgAdminRecipients(orgID int64) []string {
	if orgID == 0 {
		return nil
	}
	var out []string
	_ = db.Select(&out, `SELECT email FROM users
		WHERE org_id = $1 AND is_org_admin AND coalesce(status, 'active') = 'active'
		ORDER BY id`, orgID)
	return out
}

func userEmail(userID int64) string {
	var email string
	_ = db.Get(&email, `SELECT email FROM users WHERE id = $1`, userID)
	return email
}

// --- Događaji ---

// notifyNewRegistration obaveštava admine i org admina firme da nalog čeka.
func notifyNewRegistration(email, orgName string, orgID int64) {
	if !mail.Enabled() {
		return
	}
	to := append(adminRecipients(), orgAdminRecipients(orgID)...)
	subject := "Nova registracija čeka odobrenje"
	text := fmt.Sprintf("Novi nalog %s%s čeka odobrenje.\n\nOdobri: %s\n",
		email, withSuffix(orgName), mail.link("/admin/users"))
	if len(orgAdminRecipients(orgID)) > 0 {
		text += "Organizacija može da odobri nalog i na " + mail.link("/org/users") + "\n"
	}
	mail.Send(to, subject, text)
}

// notifyAccountApproved javlja korisniku da može da se prijavi.
func notifyAccountApproved(userID int64) {
	if !mail.Enabled() {
		return
	}
	email := userEmail(userID)
	if email == "" {
		return
	}
	mail.Send([]string{email}, "Nalog je odobren",
		fmt.Sprintf("Tvoj nalog je odobren. Prijavi se: %s\n", mail.link("/login")))
}

// notifyResetLink šalje jednokratni link korisniku (admin ga je pokrenuo).
func notifyResetLink(userID int64, link string) bool {
	if !mail.Enabled() {
		return false
	}
	email := userEmail(userID)
	if email == "" {
		return false
	}
	mail.Send([]string{email}, "Nova lozinka za Wolken-ASM Reports",
		fmt.Sprintf("Postavi novu lozinku (link važi 24 h i radi jednom):\n\n%s\n\n"+
			"Ako ovo nisi tražio, ignoriši poruku — lozinka ostaje nepromenjena.\n",
			mail.link(link)))
	return true
}

func withSuffix(orgName string) string {
	if strings.TrimSpace(orgName) == "" {
		return ""
	}
	return " (" + orgName + ")"
}
