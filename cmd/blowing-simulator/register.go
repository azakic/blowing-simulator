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

// Vrste naloga na formi za registraciju.
const (
	RegKindWorker  = "worker"  // naš radnik — unosi protokole (rola editor)
	RegKindPartner = "partner" // podizvođač (rola partner)
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
func validateRegistration(email, pw, pw2, kind, company string, orgID int64) string {
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
	switch kind {
	case RegKindWorker:
		return ""
	case RegKindPartner:
		if orgID <= 0 && strings.TrimSpace(company) == "" {
			return "Izaberi svoju firmu iz liste ili upiši njen naziv."
		}
		return ""
	default:
		return "Izaberi vrstu naloga."
	}
}

// RegisterHandler je javna forma za registraciju radnika i partnera.
// Nalog se **ne aktivira odmah** — čeka odobrenje u /admin/users, jer `editor`
// vidi sve protokole.
//
//	GET  /register
//	POST /register  email, password, password2, kind=worker|partner, org_id, company
func RegisterHandler(w http.ResponseWriter, r *http.Request) {
	orgs, _ := partnerOrgs()

	if r.Method == http.MethodGet {
		renderRegister(w, r, orgs, "", http.StatusOK)
		return
	}

	if !registerAllowed(clientIP(r)) {
		renderRegister(w, r, orgs, "Previše pokušaja registracije. Pokušaj ponovo kasnije.", http.StatusTooManyRequests)
		return
	}

	email := strings.TrimSpace(r.FormValue("email"))
	kind := r.FormValue("kind")
	company := strings.TrimSpace(r.FormValue("company"))
	orgID, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("org_id")), 10, 64)

	if msg := validateRegistration(email, r.FormValue("password"), r.FormValue("password2"), kind, company, orgID); msg != "" {
		renderRegister(w, r, orgs, msg, http.StatusBadRequest)
		return
	}

	var exists bool
	if err := db.Get(&exists, `SELECT EXISTS (SELECT 1 FROM users WHERE lower(email) = lower($1))`, email); err != nil {
		renderRegister(w, r, orgs, "Greška u bazi. Pokušaj ponovo.", http.StatusInternalServerError)
		return
	}
	if exists {
		// Ne otkrivamo da li nalog postoji — ista poruka kao za uspeh.
		renderRegister(w, r, orgs, "", http.StatusOK)
		return
	}

	role := RolePartner
	org := sqlNullInt64(0)
	requested := ""
	if kind == RegKindWorker {
		role = RoleEditor
		org = sqlNullInt64(internalOrgID())
	} else {
		if orgID > 0 {
			var kindOfOrg string
			if err := db.Get(&kindOfOrg, `SELECT kind FROM orgs WHERE id = $1`, orgID); err == nil && kindOfOrg == OrgKindSubcontractor {
				org = sqlNullInt64(orgID)
			} else {
				orgID = 0
			}
		}
		if orgID == 0 {
			requested = company
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(r.FormValue("password")), bcrypt.DefaultCost)
	if err != nil {
		renderRegister(w, r, orgs, "Greška pri obradi lozinke.", http.StatusInternalServerError)
		return
	}

	if _, err := db.Exec(`INSERT INTO users (email, password_hash, role, org_id, requested_org, status)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6)`,
		email, string(hash), role, org, requested, UserStatusPending); err != nil {
		renderRegister(w, r, orgs, "Registracija nije uspela. Pokušaj ponovo.", http.StatusInternalServerError)
		return
	}

	renderRegister(w, r, orgs, "Registracija primljena. Nalog čeka odobrenje administratora.", http.StatusOK)
}

func renderRegister(w http.ResponseWriter, r *http.Request, orgs []Org, message string, code int) {
	tmpl, err := template.ParseFiles("web/templates/register.html")
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_ = tmpl.Execute(w, map[string]any{
		"Orgs":    orgs,
		"Message": message,
		"Error":   code >= 400 && code != http.StatusOK,
		"Email":   strings.TrimSpace(r.FormValue("email")),
	})
}
