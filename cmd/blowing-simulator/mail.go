package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
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
//	MAIL_PROVIDER   resend (default)
//	MAIL_API_KEY    API ključ provajdera — bez njega je slanje isključeno
//	MAIL_FROM       "Wolken-ASM Reports <noreply@mail.wolken-asm.de>"
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
		client:   &http.Client{Timeout: 10 * time.Second},
	}
	if m.provider == "" {
		m.provider = "resend"
	}
	if m.apiBase == "" {
		m.apiBase = defaultMailAPIBase
	}
	for _, a := range strings.Split(os.Getenv("MAIL_ADMIN_TO"), ",") {
		if a = strings.TrimSpace(a); a != "" {
			m.adminTo = append(m.adminTo, a)
		}
	}
	if m.apiKey == "" || m.from == "" {
		log.Println("mail: disabled (MAIL_API_KEY / MAIL_FROM not set) — no e-mail will be sent")
		return nil
	}
	log.Printf("mail: enabled via %s, from %q", m.provider, m.from)
	return m
}

func (m *mailer) Enabled() bool {
	return m != nil && m.apiKey != "" && m.from != ""
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
	if strings.EqualFold(m.provider, "resend") {
		return m.sendResend(to, subject, text)
	}
	return fmt.Errorf("nepoznat MAIL_PROVIDER %q", m.provider)
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
