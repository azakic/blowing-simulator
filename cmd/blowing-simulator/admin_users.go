package main

import (
	"net/http"
	"strconv"
	"strings"
	"time"
	"text/template"

	"golang.org/x/crypto/bcrypt"
)

// AdminUser represents a user row for admin listing
type AdminUser struct {
	ID        int64     `db:"id"`
	Email     string    `db:"email"`
	Role      string    `db:"role"`
	CreatedAt time.Time `db:"created_at"`
}

func listAllUsers() ([]AdminUser, error) {
	var users []AdminUser
	err := db.Select(&users, "SELECT id, email, role, created_at FROM users ORDER BY id ASC")
	return users, err
}

func createUser(email, password, role string) error {
	if email == "" || password == "" || role == "" {
		return http.ErrMissingFile // simple reuse; better define custom error
	}
	role = strings.ToLower(role)
	if role != "admin" && role != "editor" && role != "partner" && role != "viewer" {
		role = "viewer"
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = db.Exec("INSERT INTO users (email, password_hash, role) VALUES ($1,$2,$3)", email, string(hash), role)
	return err
}

func updateUserRole(id int64, role string) error {
	role = strings.ToLower(role)
	if role != "admin" && role != "editor" && role != "partner" && role != "viewer" {
		return nil
	}
	_, err := db.Exec("UPDATE users SET role=$1, updated_at=NOW() WHERE id=$2", role, id)
	return err
}

func resetUserPassword(id int64, newPassword string) error {
	if newPassword == "" {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = db.Exec("UPDATE users SET password_hash=$1, updated_at=NOW() WHERE id=$2", string(hash), id)
	return err
}

// AdminUsersHandler serves the admin users page (GET list, POST actions)
func AdminUsersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		action := r.FormValue("action")
		switch action {
		case "create":
			_ = createUser(strings.TrimSpace(r.FormValue("email")), r.FormValue("password"), r.FormValue("role"))
		case "set-role":
			if id, err := strconv.ParseInt(r.FormValue("id"), 10, 64); err == nil {
				_ = updateUserRole(id, r.FormValue("role"))
			}
		case "reset-password":
			if id, err := strconv.ParseInt(r.FormValue("id"), 10, 64); err == nil {
				_ = resetUserPassword(id, r.FormValue("new_password"))
			}
		}
		http.Redirect(w, r, "/admin/users", http.StatusFound)
		return
	}
	users, _ := listAllUsers()
	tmpl, err := template.ParseFiles("web/templates/admin-users.html")
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = tmpl.Execute(w, map[string]any{
		"Users": users,
		"UserRole": func() string { c:=GetAuthClaims(r); if c!=nil {return c.Role}; return "" }(),
	})
}
