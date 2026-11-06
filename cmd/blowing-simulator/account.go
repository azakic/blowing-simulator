package main

import (
	"database/sql"
	"html/template"
	"net/http"
	"net/url"
	"time"
)

// /account — svaki korisnik menja **svoju** lozinku (traži se trenutna).
// Ovo rešava većinu zahteva bez administratora; zaboravljena lozinka ide
// preko jednokratnog linka, vidi password_reset.go.

type accountInfo struct {
	ID          int64          `db:"id"`
	Email       string         `db:"email"`
	Role        string         `db:"role"`
	CreatedAt   time.Time      `db:"created_at"`
	LastLoginAt sql.NullTime   `db:"last_login_at"`
	OrgName     sql.NullString `db:"org_name"`
	IsOrgAdmin  bool           `db:"is_org_admin"`
}

func loadAccount(userID int64) (accountInfo, error) {
	var a accountInfo
	err := db.Get(&a, `SELECT u.id, u.email, u.role, u.created_at, u.last_login_at,
			u.is_org_admin, o.name AS org_name
		FROM users u LEFT JOIN orgs o ON o.id = u.org_id
		WHERE u.id = $1`, userID)
	return a, err
}

func AccountHandler(w http.ResponseWriter, r *http.Request) {
	c := GetAuthClaims(r)
	if c == nil {
		http.Redirect(w, r, "/login?next=/account", http.StatusFound)
		return
	}

	if r.Method == http.MethodPost {
		msg := handleAccountAction(r)
		http.Redirect(w, r, "/account?msg="+url.QueryEscape(msg), http.StatusFound)
		return
	}

	a, err := loadAccount(c.UserID)
	if err != nil {
		http.Error(w, "Account not found", http.StatusNotFound)
		return
	}
	tmpl, err := template.ParseFiles("web/templates/account.html")
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = tmpl.Execute(w, map[string]any{
		"Account":    a,
		"IsAdmin":    isAdminRole(r),
		"IsOrgAdmin": isOrgAdminRequest(r),
		"UserRole":   userRole(r),
		"Message":    r.URL.Query().Get("msg"),
	})
}

// handleAccountAction menja samo lozinku sopstvenog naloga. Rola i
// organizacija se ne mogu menjati odavde.
func handleAccountAction(r *http.Request) string {
	c := GetAuthClaims(r)
	if c == nil {
		return "Sesija je istekla. Prijavi se ponovo."
	}
	if r.FormValue("action") != "change-password" {
		return "Nepoznata akcija."
	}

	var hash string
	if err := db.Get(&hash, `SELECT password_hash FROM users WHERE id = $1`, c.UserID); err != nil {
		return "Korisnik nije nađen."
	}
	if checkPassword(hash, r.FormValue("current_password")) != nil {
		return "Trenutna lozinka nije ispravna."
	}
	if msg := validateNewPassword(r.FormValue("new_password"), r.FormValue("password2")); msg != "" {
		return msg
	}
	if r.FormValue("new_password") == r.FormValue("current_password") {
		return "Nova lozinka mora da se razlikuje od trenutne."
	}
	if err := resetUserPassword(c.UserID, r.FormValue("new_password")); err != nil {
		return "Greška: " + err.Error()
	}
	logUserEvent(c.UserID, c.UserID, "self-password", "")
	return "Lozinka je promenjena."
}
