package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"context"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jmoiron/sqlx"
	"golang.org/x/crypto/bcrypt"
	"text/template"
)

type User struct {
	ID           int64          `db:"id"`
	Email        string         `db:"email"`
	PasswordHash string         `db:"password_hash"`
	Role         string         `db:"role"`
	CreatedAt    time.Time      `db:"created_at"`
	UpdatedAt    sql.NullTime   `db:"updated_at"`
}

func ensureUsersTable(db *sqlx.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS users (
		id SERIAL PRIMARY KEY,
		email TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		role TEXT NOT NULL CHECK (role IN ('admin','editor','partner','viewer')),
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ
	);
	`
	_, err := db.Exec(schema)
	if err != nil {
		return err
	}
	
	// Migrate existing constraint to include 'partner' role
	_, _ = db.Exec("ALTER TABLE users DROP CONSTRAINT IF EXISTS users_role_check")
	_, _ = db.Exec("ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('admin','editor','partner','viewer'))")
	
	return nil
}

func seedDefaultAdmin(db *sqlx.DB) error {
	var count int
	if err := db.Get(&count, "SELECT COUNT(1) FROM users"); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	email := getEnvOrDefault("ADMIN_EMAIL", "admin@example.com")
	pwd := getEnvOrDefault("ADMIN_PASSWORD", "admin123!")
	hash, err := bcrypt.GenerateFromPassword([]byte(pwd), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = db.Exec("INSERT INTO users (email, password_hash, role) VALUES ($1,$2,'admin')", email, string(hash))
	return err
}

func hashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

func checkPassword(hash, pw string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw))
}

type AuthClaims struct {
	UserID int64  `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

func jwtSecret() []byte {
	return []byte(getEnvOrDefault("AUTH_SECRET", "change-me-in-prod"))
}

func mintJWT(u *User) (string, error) {
	claims := AuthClaims{
		UserID: u.ID,
		Email:  u.Email,
		Role:   u.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString(jwtSecret())
}

func parseJWT(tokenStr string) (*AuthClaims, error) {
	tkn, err := jwt.ParseWithClaims(tokenStr, &AuthClaims{}, func(token *jwt.Token) (interface{}, error) {
		return jwtSecret(), nil
	})
	if err != nil {
		return nil, err
	}
	if claims, ok := tkn.Claims.(*AuthClaims); ok && tkn.Valid {
		return claims, nil
	}
	return nil, errors.New("invalid token")
}

func setAuthCookie(w http.ResponseWriter, token string) {
	c := &http.Cookie{
		Name:     "auth_token",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// Secure: true, // enable behind HTTPS
	}
	http.SetCookie(w, c)
}

func clearAuthCookie(w http.ResponseWriter) {
	c := &http.Cookie{
		Name:     "auth_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
		SameSite: http.SameSiteLaxMode,
	}
	http.SetCookie(w, c)
}

func requireAuth(handler http.HandlerFunc, allowedRoles ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("auth_token")
		if err != nil || c.Value == "" {
			http.Redirect(w, r, "/login?next="+urlQuery(r.URL.String()), http.StatusFound)
			return
		}
		claims, err := parseJWT(c.Value)
		if err != nil {
			clearAuthCookie(w)
			http.Redirect(w, r, "/login?next="+urlQuery(r.URL.String()), http.StatusFound)
			return
		}
		if len(allowedRoles) > 0 && !roleAllowed(claims.Role, allowedRoles) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		// attach claims to context for downstream handlers
		ctx := context.WithValue(r.Context(), authClaimsContextKey{}, claims)
		handler.ServeHTTP(w, r.WithContext(ctx))
	}
}

// context key type to avoid collisions
type authClaimsContextKey struct{}

// GetAuthClaims retrieves auth claims from request context if present
func GetAuthClaims(r *http.Request) *AuthClaims {
	val := r.Context().Value(authClaimsContextKey{})
	if val == nil {
		return nil
	}
	if claims, ok := val.(*AuthClaims); ok {
		return claims
	}
	return nil
}

func roleAllowed(role string, allowed []string) bool {
	if role == "admin" {
		return true
	}
	for _, a := range allowed {
		if strings.EqualFold(a, role) {
			return true
		}
	}
	return false
}

func urlQuery(s string) string {
	return strings.ReplaceAll(s, " ", "%20")
}

func LoginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		tmpl, err := template.ParseFiles("web/templates/login.html")
		if err != nil {
			http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		_ = tmpl.Execute(w, map[string]any{
			"Next": r.URL.Query().Get("next"),
		})
		return
	}
	// POST
	email := strings.TrimSpace(r.FormValue("email"))
	pw := r.FormValue("password")
	if email == "" || pw == "" {
		http.Error(w, "Missing credentials", http.StatusBadRequest)
		return
	}
	var u User
	err := db.Get(&u, "SELECT id, email, password_hash, role, created_at FROM users WHERE email=$1", email)
	if err != nil {
		http.Error(w, "Invalid email or password", http.StatusUnauthorized)
		return
	}
	if err := checkPassword(u.PasswordHash, pw); err != nil {
		http.Error(w, "Invalid email or password", http.StatusUnauthorized)
		return
	}
	tok, err := mintJWT(&u)
	if err != nil {
		http.Error(w, "Login failed", http.StatusInternalServerError)
		return
	}
	setAuthCookie(w, tok)
	next := r.FormValue("next")
	if next == "" {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusFound)
}

func LogoutHandler(w http.ResponseWriter, r *http.Request) {
	clearAuthCookie(w)
	http.Redirect(w, r, "/login", http.StatusFound)
}

func initAuth(db *sqlx.DB) error {
	if err := ensureUsersTable(db); err != nil {
		return fmt.Errorf("ensureUsersTable: %w", err)
	}
	if err := seedDefaultAdmin(db); err != nil {
		return fmt.Errorf("seedDefaultAdmin: %w", err)
	}
	return nil
}
