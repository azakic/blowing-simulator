package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"html/template"
	"net/http"
	"strings"
	"time"
)

// Zaboravljena lozinka bez e-mail servera.
//
// Admin (globalni ili organizacijski) napravi **jednokratni link** koji važi
// 24 h i pošalje ga korisniku kanalom koji već koristimo (WhatsApp, mail,
// telefon). U bazi se čuva samo SHA-256 tokena, pa dump baze ne otkriva
// važeće linkove. Novi link poništava stari.

const passwordResetTTL = 24 * time.Hour

func hashResetToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// createPasswordResetLink vraća putanju `/reset?t=…` (bez hosta).
func createPasswordResetLink(userID, actorID int64) (string, error) {
	var exists bool
	if err := db.Get(&exists, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, userID); err != nil {
		return "", err
	}
	if !exists {
		return "", errors.New("korisnik nije nađen")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	// Stari, još neiskorišćeni linkovi prestaju da važe.
	if _, err := db.Exec(`UPDATE password_resets SET used_at = NOW()
		WHERE user_id = $1 AND used_at IS NULL`, userID); err != nil {
		return "", err
	}
	if _, err := db.Exec(`INSERT INTO password_resets (user_id, token_hash, created_by, expires_at)
		VALUES ($1, $2, $3, $4)`,
		userID, hashResetToken(token), sqlNullInt64(actorID),
		time.Now().Add(passwordResetTTL)); err != nil {
		return "", err
	}
	return "/reset?t=" + token, nil
}

// passwordResetTarget vraća korisnika za važeći, neiskorišćen token.
func passwordResetTarget(token string) (int64, string, error) {
	token = strings.TrimSpace(token)
	if token == "" || db == nil {
		return 0, "", errors.New("nedostaje token")
	}
	var row struct {
		UserID int64  `db:"user_id"`
		Email  string `db:"email"`
	}
	if err := db.Get(&row, `SELECT pr.user_id, u.email
		FROM password_resets pr JOIN users u ON u.id = pr.user_id
		WHERE pr.token_hash = $1 AND pr.used_at IS NULL AND pr.expires_at > NOW()`,
		hashResetToken(token)); err != nil {
		return 0, "", errors.New("link je istekao ili je već iskorišćen")
	}
	return row.UserID, row.Email, nil
}

// consumePasswordReset označava token kao iskorišćen.
func consumePasswordReset(token string) error {
	_, err := db.Exec(`UPDATE password_resets SET used_at = NOW()
		WHERE token_hash = $1 AND used_at IS NULL`, hashResetToken(token))
	return err
}

// validateNewPassword proverava novu lozinku i potvrdu (bez baze, testabilno).
func validateNewPassword(pw, pw2 string) string {
	if len(pw) < 8 {
		return "Lozinka mora imati najmanje 8 znakova."
	}
	if pw != pw2 {
		return "Lozinke se ne poklapaju."
	}
	return ""
}

// ResetPasswordHandler je javna strana za postavljanje nove lozinke sa
// jednokratnog linka. Ne traži prijavu — token je dokaz.
func ResetPasswordHandler(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.FormValue("t"))
	userID, email, err := passwordResetTarget(token)
	if err != nil {
		renderReset(w, r, "", "", "Link je istekao ili je već iskorišćen. Zamoli administratora za novi.", http.StatusForbidden)
		return
	}

	if r.Method != http.MethodPost {
		renderReset(w, r, token, email, "", http.StatusOK)
		return
	}

	if msg := validateNewPassword(r.FormValue("password"), r.FormValue("password2")); msg != "" {
		renderReset(w, r, token, email, msg, http.StatusBadRequest)
		return
	}
	if err := resetUserPassword(userID, r.FormValue("password")); err != nil {
		renderReset(w, r, token, email, "Greška: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := consumePasswordReset(token); err != nil {
		// Lozinka je već promenjena; token ostaje važeći do isteka, ali se
		// greška ne krije od administratora u logu.
		logUserEvent(0, userID, "password-reset-consume", "neuspešno poništavanje linka")
	}
	logUserEvent(0, userID, "password-reset", "jednokratni link")
	renderReset(w, r, "", "", "Lozinka je promenjena. Možeš da se prijaviš.", http.StatusOK)
}

func renderReset(w http.ResponseWriter, r *http.Request, token, email, message string, code int) {
	tmpl, err := template.New("reset.html").Funcs(template.FuncMap(csrfTemplateFuncs(r))).ParseFiles("web/templates/reset.html")
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_ = tmpl.Execute(w, map[string]any{
		"Token":   token,
		"Email":   email,
		"Message": message,
		"Error":   code >= 400,
		"Done":    token == "" && code < 400,
	})
}
