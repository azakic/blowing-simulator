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
			u.status, u.requested_org, u.created_at,
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
			return "Partner mora imati organizaciju (podizvođač)."
		}
		if orgKind == OrgKindInternal {
			return "Partner ne može biti u internoj organizaciji."
		}
	case RoleAdmin, RoleEditor, RoleViewer:
		if orgKind == OrgKindSubcontractor || orgKind == OrgKindClient {
			return "Interni nalog (admin/editor/viewer) ne može biti u partnerskoj organizaciji."
		}
	}
	return ""
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
		return fmt.Errorf("email i lozinka su obavezni")
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
				return fmt.Errorf("ne možeš ukinuti rolu poslednjem aktivnom administratoru")
			}
		}
	}
	res, err := db.Exec("UPDATE users SET role=$1, updated_at=NOW() WHERE id=$2", r, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("korisnik nije nađen")
	}
	return nil
}

func resetUserPassword(id int64, newPassword string) error {
	if newPassword == "" {
		return fmt.Errorf("nova lozinka je obavezna")
	}
	if len(newPassword) < 8 {
		return fmt.Errorf("lozinka mora imati najmanje 8 znakova")
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
		return fmt.Errorf("korisnik nije nađen")
	}
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

	tmpl, err := template.ParseFiles("web/templates/admin-users.html")
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = tmpl.Execute(w, map[string]any{
		"Users":    users,
		"Orgs":     orgs,
		"Events":   events,
		"IsAdmin":  true,
		"UserRole": userRole(r),
		"Message":  r.URL.Query().Get("msg"),
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
			return "Greška: " + err.Error()
		}
		logUserEvent(actorID, 0, "create", email)
		return "Korisnik napravljen: " + email

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
				return "Nepoznata organizacija."
			}
		}
		if msg := validateRoleOrg(role, kind); msg != "" {
			return msg
		}
		oldRole, _, err := roleOfUser(id)
		if err != nil {
			return "Korisnik nije nađen."
		}
		if oldRole == RoleAdmin && role != RoleAdmin && countActiveAdmins(id) == 0 {
			return "Ne možeš ukinuti rolu poslednjem aktivnom administratoru."
		}
		tx, err := db.Begin()
		if err != nil {
			return "Greška: " + err.Error()
		}
		defer tx.Rollback()
		if _, err := tx.Exec(`UPDATE users SET role = $1, org_id = $2, updated_at = NOW() WHERE id = $3`,
			role, sqlNullInt64(orgID), id); err != nil {
			return "Greška: " + err.Error()
		}
		if err := tx.Commit(); err != nil {
			return "Greška: " + err.Error()
		}
		logUserEvent(actorID, id, "set-access", role+" / "+orgNameByID(orgID))
		return "Rola i organizacija sačuvane."

	case "set-role":
		role, ok := validUserRole(r.FormValue("role"))
		if !ok {
			return "Nepoznata rola."
		}
		_, orgKind, err := roleOfUser(id)
		if err != nil {
			return "Korisnik nije nađen."
		}
		if msg := validateRoleOrg(role, orgKind); msg != "" {
			return msg
		}
		if err := updateUserRole(id, role); err != nil {
			return "Greška: " + err.Error()
		}
		logUserEvent(actorID, id, "set-role", role)
		return "Rola promenjena."

	case "set-org":
		orgID, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("org_id")), 10, 64)
		role, _, err := roleOfUser(id)
		if err != nil {
			return "Korisnik nije nađen."
		}
		kind := ""
		if orgID != 0 {
			kind = orgKindOf(orgID)
			if kind == "" {
				return "Nepoznata organizacija."
			}
		}
		if msg := validateRoleOrg(role, kind); msg != "" {
			return msg
		}
		if err := setUserOrg(id, orgID); err != nil {
			return "Greška: " + err.Error()
		}
		logUserEvent(actorID, id, "set-org", orgNameByID(orgID))
		return "Organizacija postavljena."

	case "reset-password":
		if _, _, err := roleOfUser(id); err != nil {
			return "Korisnik nije nađen."
		}
		if err := resetUserPassword(id, r.FormValue("new_password")); err != nil {
			return "Greška: " + err.Error()
		}
		logUserEvent(actorID, id, "reset-password", "")
		return "Lozinka promenjena."

	case "approve":
		role, orgKind, err := roleOfUser(id)
		if err != nil {
			return "Korisnik nije nađen."
		}
		if msg := validateRoleOrg(role, orgKind); msg != "" {
			return "Pre odobrenja: " + msg
		}
		if err := setUserStatus(id, UserStatusPending, UserStatusActive); err != nil {
			return "Greška: " + err.Error()
		}
		logUserEvent(actorID, id, "approve", "")
		return "Nalog odobren."

	case "reject":
		if _, err := db.Exec(`DELETE FROM users WHERE id = $1 AND status = $2`, id, UserStatusPending); err != nil {
			return "Greška: " + err.Error()
		}
		logUserEvent(actorID, id, "reject", "")
		return "Zahtev odbijen."

	case "disable":
		if id == actorID {
			return "Ne možeš deaktivirati sopstveni nalog."
		}
		role, _, err := roleOfUser(id)
		if err != nil {
			return "Korisnik nije nađen."
		}
		if role == RoleAdmin && countActiveAdmins(id) == 0 {
			return "Ne možeš deaktivirati poslednjeg aktivnog administratora."
		}
		if err := setUserStatus(id, UserStatusActive, UserStatusDisabled); err != nil {
			return "Greška: " + err.Error()
		}
		logUserEvent(actorID, id, "disable", "")
		return "Nalog deaktiviran."

	case "enable":
		if _, _, err := roleOfUser(id); err != nil {
			return "Korisnik nije nađen."
		}
		if err := setUserStatus(id, UserStatusDisabled, UserStatusActive); err != nil {
			return "Greška: " + err.Error()
		}
		logUserEvent(actorID, id, "enable", "")
		return "Nalog ponovo aktivan."

	case "delete":
		if id == actorID {
			return "Ne možeš obrisati sopstveni nalog."
		}
		var owned int
		_ = db.Get(&owned, `SELECT count(*) FROM protocols WHERE user_id = $1`, id)
		if owned > 0 {
			return fmt.Sprintf("Korisnik ima %d protokola — deaktiviraj nalog umesto brisanja.", owned)
		}
		role, _, err := roleOfUser(id)
		if err != nil {
			return "Korisnik nije nađen."
		}
		if role == RoleAdmin && countActiveAdmins(id) == 0 {
			return "Ne možeš obrisati poslednjeg aktivnog administratora."
		}
		res, err := db.Exec(`DELETE FROM users WHERE id = $1`, id)
		if err != nil {
			return "Greška: " + err.Error()
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return "Korisnik nije nađen."
		}
		logUserEvent(actorID, id, "delete", "")
		return "Nalog obrisan."
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
		return fmt.Errorf("nalog nije u statusu %q", from)
	}
	return nil
}
