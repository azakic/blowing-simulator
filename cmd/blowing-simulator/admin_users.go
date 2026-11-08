package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"text/template"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const UserStatusDisabled = "disabled"

// AdminUser represents a user row for admin listing
type AdminUser struct {
	ID           int64          `db:"id"`
	Email        string         `db:"email"`
	Role         string         `db:"role"`
	OrgID        sql.NullInt64  `db:"org_id"`
	OrgName      sql.NullString `db:"org_name"`
	Status       sql.NullString `db:"status"`
	RequestedOrg sql.NullString `db:"requested_org"`
	IsOrgAdmin   bool           `db:"is_org_admin"`
	LastLoginAt  sql.NullTime   `db:"last_login_at"`
	CreatedAt    time.Time      `db:"created_at"`
	Protocols    int            `db:"protocols"`
}

type userEvent struct {
	CreatedAt time.Time      `db:"created_at"`
	Actor     sql.NullString `db:"actor"`
	Target    sql.NullString `db:"target"`
	Action    string         `db:"action"`
	Detail    string         `db:"detail"`
}

func listAllUsers() ([]AdminUser, error) {
	var users []AdminUser
	err := db.Select(&users, `SELECT u.id, u.email, u.role, u.org_id, o.name AS org_name,
			u.status, u.requested_org, u.is_org_admin, u.last_login_at, u.created_at,
			(SELECT count(*) FROM protocols p WHERE p.user_id = u.id) AS protocols
		FROM users u LEFT JOIN orgs o ON o.id = u.org_id
		ORDER BY (u.status = 'pending') DESC, (u.status = 'disabled'), u.id`)
	return users, err
}

// validUserRole normalizuje i proverava rolu.
func validUserRole(role string) (string, bool) {
	role = strings.ToLower(strings.TrimSpace(role))
	switch role {
	case RoleAdmin, RoleEditor, RolePartner, RoleViewer:
		return role, true
	}
	return "", false
}

// validateRoleOrg vraća poruku greške kada rola i tip organizacije ne idu
// zajedno. `orgKind` je "" kada korisnik nema organizaciju.
func validateRoleOrg(role, orgKind string) string {
	switch role {
	case RolePartner:
		if orgKind == "" {
			return "A partner must belong to a (subcontractor) organisation."
		}
		if orgKind == OrgKindInternal {
			return "A partner cannot belong to the internal organisation."
		}
	case RoleAdmin, RoleEditor, RoleViewer:
		if orgKind == OrgKindSubcontractor || orgKind == OrgKindClient {
			return "An internal account (admin/editor/viewer) cannot belong to a partner organisation."
		}
	}
	return ""
}

// loadUserByID vraća dovoljno podataka za mintJWT (id, email, role).
func loadUserByID(id int64) (User, error) {
	var u User
	err := db.Get(&u, `SELECT id, email, role FROM users WHERE id = $1`, id)
	return u, err
}

func roleOfUser(id int64) (string, string, error) {
	var row struct {
		Role string         `db:"role"`
		Kind sql.NullString `db:"org_kind"`
	}
	err := db.Get(&row, `SELECT u.role, o.kind AS org_kind FROM users u
		LEFT JOIN orgs o ON o.id = u.org_id WHERE u.id = $1`, id)
	return row.Role, row.Kind.String, err
}

// countActiveAdmins broji aktivne administratore (ne računajući `excludeID`).
func countActiveAdmins(excludeID int64) int {
	var n int
	_ = db.Get(&n, `SELECT count(*) FROM users
		WHERE role = 'admin' AND coalesce(status, 'active') = 'active' AND id <> $1`, excludeID)
	return n
}

// logUserEvent beleži akciju nad nalogom (ko, šta, kome).
func logUserEvent(actorID, targetID int64, action, detail string) {
	_, err := db.Exec(`INSERT INTO user_events (actor_id, user_id, action, detail)
		VALUES ($1, $2, $3, $4)`,
		sql.NullInt64{Int64: actorID, Valid: actorID != 0},
		sql.NullInt64{Int64: targetID, Valid: targetID != 0},
		action, detail)
	if err != nil {
		fmt.Printf("user_events: %v\n", err)
	}
}

func createUser(email, password, role string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || password == "" {
		return fmt.Errorf("email and password are required")
	}
	r, ok := validUserRole(role)
	if !ok {
		return fmt.Errorf("nepoznata rola")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO users (email, password_hash, role, status)
		VALUES ($1, $2, $3, 'active')`, email, string(hash), r)
	return err
}

func updateUserRole(id int64, role string) error {
	r, ok := validUserRole(role)
	if !ok {
		return fmt.Errorf("nepoznata rola")
	}
	// Zaštita od zaključavanja: ne sme ostati bez aktivnog admina.
	if r != RoleAdmin {
		if oldRole, _, err := roleOfUser(id); err == nil && oldRole == RoleAdmin {
			if countActiveAdmins(id) == 0 {
				return fmt.Errorf("you cannot remove the role from the last active administrator")
			}
		}
	}
	res, err := db.Exec("UPDATE users SET role=$1, updated_at=NOW() WHERE id=$2", r, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

func resetUserPassword(id int64, newPassword string) error {
	if newPassword == "" {
		return fmt.Errorf("new password is required")
	}
	if len(newPassword) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	res, err := db.Exec("UPDATE users SET password_hash=$1, updated_at=NOW() WHERE id=$2", string(hash), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("user not found")
	}
	// Svaka promena lozinke obara postojeće sesije (self-service strana posle
	// toga izda novi token, vidi account.go).
	invalidateUserSessions(id)
	return nil
}

// AdminUsersHandler serves the admin users page (GET list, POST actions)
func AdminUsersHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		msg := handleUserAction(r)
		http.Redirect(w, r, "/admin/users?msg="+url.QueryEscape(msg), http.StatusFound)
		return
	}

	users, err := listAllUsers()
	if err != nil {
		http.Error(w, "Query failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	orgs, _ := listOrgs()
	events, _ := listUserEvents(20)

	tmpl, err := template.New("admin-users.html").Funcs(template.FuncMap(csrfTemplateFuncs(r))).ParseFiles("web/templates/admin-users.html")
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = tmpl.Execute(w, map[string]any{
		"Users":      users,
		"Orgs":       orgs,
		"Events":     events,
		"IsAdmin":    true,
		"IsOrgAdmin": isOrgAdminRequest(r),
		"UserRole":   userRole(r),
		"Message":    r.URL.Query().Get("msg"),
	})
}

// handleUserAction izvršava POST i vraća poruku za prikaz.
func handleUserAction(r *http.Request) string {
	claims := GetAuthClaims(r)
	var actorID int64
	if claims != nil {
		actorID = claims.UserID
	}
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)

	switch r.FormValue("action") {
	case "create":
		email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
		if err := createUser(email, r.FormValue("password"), r.FormValue("role")); err != nil {
			return "Error: " + err.Error()
		}
		logUserEvent(actorID, 0, "create", email)
		return "User created: " + email

	case "set-access":
		// Rola i organizacija se menjaju zajedno, jer pojedinačno mogu da budu
		// u zabranjenoj kombinaciji (npr. viewer → partner).
		role, ok := validUserRole(r.FormValue("role"))
		if !ok {
			return "Nepoznata rola."
		}
		orgID, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("org_id")), 10, 64)
		kind := ""
		if orgID != 0 {
			kind = orgKindOf(orgID)
			if kind == "" {
				return "Unknown organisation."
			}
		}
		if msg := validateRoleOrg(role, kind); msg != "" {
			return msg
		}
		// Organizacijski admin je `partner` sa flagom; flag bez partnerske
		// uloge ne bi imao smisla (vidi isOrgAdminUser).
		orgAdmin := r.FormValue("is_org_admin") != ""
		if orgAdmin && role != RolePartner {
			return "Only a partner can be an organisation admin."
		}
		oldRole, _, err := roleOfUser(id)
		if err != nil {
			return "User not found."
		}
		if oldRole == RoleAdmin && role != RoleAdmin && countActiveAdmins(id) == 0 {
			return "You cannot remove the role from the last active administrator."
		}
		tx, err := db.Begin()
		if err != nil {
			return "Error: " + err.Error()
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`UPDATE users SET role = $1, org_id = $2, is_org_admin = $3,
			updated_at = NOW() WHERE id = $4`,
			role, sqlNullInt64(orgID), orgAdmin, id); err != nil {
			return "Error: " + err.Error()
		}
		if err := tx.Commit(); err != nil {
			return "Error: " + err.Error()
		}
		detail := role + " / " + orgNameByID(orgID)
		if orgAdmin {
			detail += " / org admin"
		}
		logUserEvent(actorID, id, "set-access", detail)
		return "Role and organisation saved."

	case "set-role":
		role, ok := validUserRole(r.FormValue("role"))
		if !ok {
			return "Nepoznata rola."
		}
		_, orgKind, err := roleOfUser(id)
		if err != nil {
			return "User not found."
		}
		if msg := validateRoleOrg(role, orgKind); msg != "" {
			return msg
		}
		if err := updateUserRole(id, role); err != nil {
			return "Error: " + err.Error()
		}
		logUserEvent(actorID, id, "set-role", role)
		return "Role changed."

	case "set-org":
		orgID, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("org_id")), 10, 64)
		role, _, err := roleOfUser(id)
		if err != nil {
			return "User not found."
		}
		kind := ""
		if orgID != 0 {
			kind = orgKindOf(orgID)
			if kind == "" {
				return "Unknown organisation."
			}
		}
		if msg := validateRoleOrg(role, kind); msg != "" {
			return msg
		}
		if err := setUserOrg(id, orgID); err != nil {
			return "Error: " + err.Error()
		}
		logUserEvent(actorID, id, "set-org", orgNameByID(orgID))
		return "Organisation set."

	case "reset-password":
		if _, _, err := roleOfUser(id); err != nil {
			return "User not found."
		}
		if err := resetUserPassword(id, r.FormValue("new_password")); err != nil {
			return "Error: " + err.Error()
		}
		logUserEvent(actorID, id, "reset-password", "")
		return "Password changed."

	case "reset-link":
		if _, _, err := roleOfUser(id); err != nil {
			return "User not found."
		}
		link, err := createPasswordResetLink(id, actorID)
		if err != nil {
			return "Error: " + err.Error()
		}
		logUserEvent(actorID, id, "reset-link", "")
		if notifyResetLink(id, link) {
			return "A one-time link was emailed to the user (valid for 24 h)."
		}
		return "Jednokratni link (važi 24 h): " + absoluteURL(r, link)

	case "approve":
		role, orgKind, err := roleOfUser(id)
		if err != nil {
			return "User not found."
		}
		if msg := validateRoleOrg(role, orgKind); msg != "" {
			return "Before approval: " + msg
		}
		if err := setUserStatus(id, UserStatusPending, UserStatusActive); err != nil {
			return "Error: " + err.Error()
		}
		logUserEvent(actorID, id, "approve", "")
		notifyAccountApproved(id)
		return "Account approved."

	case "reject":
		if _, err := db.Exec(`DELETE FROM users WHERE id = $1 AND status = $2`, id, UserStatusPending); err != nil {
			return "Error: " + err.Error()
		}
		logUserEvent(actorID, id, "reject", "")
		return "Request rejected."

	case "disable":
		if id == actorID {
			return "You cannot disable your own account."
		}
		role, _, err := roleOfUser(id)
		if err != nil {
			return "User not found."
		}
		if role == RoleAdmin && countActiveAdmins(id) == 0 {
			return "You cannot disable the last active administrator."
		}
		if err := setUserStatus(id, UserStatusActive, UserStatusDisabled); err != nil {
			return "Error: " + err.Error()
		}
		logUserEvent(actorID, id, "disable", "")
		return "Account disabled."

	case "enable":
		if _, _, err := roleOfUser(id); err != nil {
			return "User not found."
		}
		if err := setUserStatus(id, UserStatusDisabled, UserStatusActive); err != nil {
			return "Error: " + err.Error()
		}
		logUserEvent(actorID, id, "enable", "")
		return "Account re-enabled."

	case "delete":
		if id == actorID {
			return "You cannot delete your own account."
		}
		var owned int
		_ = db.Get(&owned, `SELECT count(*) FROM protocols WHERE user_id = $1`, id)
		if owned > 0 {
			return fmt.Sprintf("The user has %d protocols — disable the account instead of deleting it.", owned)
		}
		role, _, err := roleOfUser(id)
		if err != nil {
			return "User not found."
		}
		if role == RoleAdmin && countActiveAdmins(id) == 0 {
			return "You cannot delete the last active administrator."
		}
		res, err := db.Exec(`DELETE FROM users WHERE id = $1`, id)
		if err != nil {
			return "Error: " + err.Error()
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return "User not found."
		}
		logUserEvent(actorID, id, "delete", "")
		return "Account deleted."
	}
	return "Nepoznata akcija."
}

// listUserEvents vraća poslednjih N događaja nad nalozima.
func listUserEvents(limit int) ([]userEvent, error) {
	var out []userEvent
	err := db.Select(&out, `SELECT e.created_at,
			a.email AS actor, u.email AS target, e.action, e.detail
		FROM user_events e
		LEFT JOIN users a ON a.id = e.actor_id
		LEFT JOIN users u ON u.id = e.user_id
		ORDER BY e.created_at DESC, e.id DESC LIMIT $1`, limit)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	return out, err
}

// setUserStatus menja status naloga samo ako je trenutno u `from` (bezbedno za
// approve/reject utrku).
func setUserStatus(id int64, from, to string) error {
	res, err := db.Exec(`UPDATE users SET status = $1, updated_at = NOW()
		WHERE id = $2 AND status = $3`, to, id, from)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("account is not in status %q", from)
	}
	return nil
}
