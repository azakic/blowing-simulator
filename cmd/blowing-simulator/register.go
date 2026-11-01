package main

import (
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	UserStatusActive  = "active"
	UserStatusPending = "pending"
)

// Register throttle: max 5 pokušaja po IP adresi na sat. Broji se u bazi, jer
// aplikacija radi u više replika (in-memory brojač bi bio po podu).
const (
	regWindow     = time.Hour
	regMaxPerWndw = 5
)

// Anti-bot: forma se ne prihvata ako prođe manje od `regMinFillSeconds` ili
// više od `regMaxFormAge`. Honeypot polje `website` mora ostati prazno.
const (
	regMinFillSeconds = 3
	regMaxFormAge     = time.Hour
	regHoneypotField  = "website"
)

// registerAllowed beleži pokušaj i vraća false kad je limit prekoračen.
func registerAllowed(ip string) bool {
	if db == nil || ip == "" {
		return true
	}
	window := fmt.Sprintf("%d minutes", int(regWindow.Minutes()))
	if _, err := db.Exec(`DELETE FROM registration_attempts
		WHERE created_at < NOW() - $1::interval`, window); err != nil {
		log.Printf("register throttle cleanup: %v", err)
	}
	var n int
	if err := db.Get(&n, `SELECT count(*) FROM registration_attempts
		WHERE ip = $1 AND created_at > NOW() - $2::interval`, ip, window); err != nil {
		log.Printf("register throttle count: %v", err)
		return true
	}
	if n >= regMaxPerWndw {
		return false
	}
	if _, err := db.Exec(`INSERT INTO registration_attempts (ip) VALUES ($1)`, ip); err != nil {
		log.Printf("register throttle insert: %v", err)
	}
	return true
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// validateRegistration proverava unos bez pristupa bazi (testabilno).
// Vraća poruku greške ili "" ako je unos ispravan.
func validateRegistration(email, pw, pw2, code string) string {
	email = strings.TrimSpace(email)
	if len(email) < 5 || !strings.Contains(email, "@") || strings.ContainsAny(email, " \t") {
		return "Unesi ispravan email."
	}
	if len(pw) < 8 {
		return "Lozinka mora imati najmanje 8 znakova."
	}
	if pw != pw2 {
		return "Lozinke se ne poklapaju."
	}
	if strings.TrimSpace(code) == "" {
		return "Unesi registracioni kod (dobijaš ga od nas)."
	}
	return ""
}

// formTimingOK proverava honeypot i starost forme.
func formTimingOK(r *http.Request) bool {
	if strings.TrimSpace(r.FormValue(regHoneypotField)) != "" {
		return false
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("t")), 10, 64)
	if err != nil {
		return false
	}
	elapsed := time.Since(time.Unix(ts, 0))
	return elapsed >= regMinFillSeconds*time.Second && elapsed <= regMaxFormAge
}

// RegisterHandler je forma za registraciju radnika i partnera. Pristup traži
// **registracioni kod organizacije** — bez koda nema ni naloga ni šanse za
// spam. Kod određuje i rolu i organizaciju:
//
//	interna organizacija        → rola `editor`
//	podizvođačka organizacija   → rola `partner`
//
// Nalog se ne aktivira odmah (čeka odobrenje u /admin/users).
//
//	GET  /register
//	POST /register  code, email, password, password2, t, website(honeypot)
func RegisterHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		renderRegister(w, r, "", http.StatusOK)
		return
	}

	if !formTimingOK(r) || !registerAllowed(clientIP(r)) {
		renderRegister(w, r, "Zahtev nije prihvaćen. Pokušaj ponovo kasnije.", http.StatusTooManyRequests)
		return
	}

	email := strings.TrimSpace(r.FormValue("email"))
	code := strings.TrimSpace(r.FormValue("code"))

	if msg := validateRegistration(email, r.FormValue("password"), r.FormValue("password2"), code); msg != "" {
		renderRegister(w, r, msg, http.StatusBadRequest)
		return
	}

	orgID, orgKind, ok := orgByInviteCode(code)
	if !ok {
		renderRegister(w, r, "Nepoznat registracioni kod.", http.StatusForbidden)
		return
	}
	role := ""
	switch orgKind {
	case OrgKindInternal:
		role = RoleEditor
	case OrgKindSubcontractor:
		role = RolePartner
	default:
		renderRegister(w, r, "Taj kod ne otvara nalog. Obrati se administratoru.", http.StatusForbidden)
		return
	}

	var exists bool
	if err := db.Get(&exists, `SELECT EXISTS (SELECT 1 FROM users WHERE lower(email) = lower($1))`, email); err != nil {
		renderRegister(w, r, "Greška u bazi. Pokušaj ponovo.", http.StatusInternalServerError)
		return
	}
	if exists {
		// Ne otkrivamo da li nalog postoji — ista poruka kao za uspeh.
		renderRegister(w, r, "Registracija primljena. Nalog čeka odobrenje administratora.", http.StatusOK)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(r.FormValue("password")), bcrypt.DefaultCost)
	if err != nil {
		renderRegister(w, r, "Greška pri obradi lozinke.", http.StatusInternalServerError)
		return
	}

	if _, err := db.Exec(`INSERT INTO users (email, password_hash, role, org_id, status)
		VALUES ($1, $2, $3, $4, $5)`,
		email, string(hash), role, sqlNullInt64(orgID), UserStatusPending); err != nil {
		renderRegister(w, r, "Registracija nije uspela. Pokušaj ponovo.", http.StatusInternalServerError)
		return
	}

	renderRegister(w, r, "Registracija primljena. Nalog čeka odobrenje administratora.", http.StatusOK)
}

func renderRegister(w http.ResponseWriter, r *http.Request, message string, code int) {
	tmpl, err := template.ParseFiles("web/templates/register.html")
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_ = tmpl.Execute(w, map[string]any{
		"Message": message,
		"Error":   code >= 400,
		"Email":   strings.TrimSpace(r.FormValue("email")),
		"Now":     time.Now().Unix(),
	})
}
