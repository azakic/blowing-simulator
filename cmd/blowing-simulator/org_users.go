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
)

// Organizacijski administrator (vidi docs/ACCESS.md).
//
// `partner` nalog u podizvođačkoj organizaciji sa `users.is_org_admin` može da
// upravlja **samo** nalozima svoje organizacije:
//
//   - odobri ili odbije `pending` registracije,
//   - napravi jednokratni link za zaboravljenu lozinku,
//   - deaktivira i ponovo aktivira nalog,
//   - promeni registracioni kod organizacije.
//
// Ne menja role, ne premešta korisnike i ne vidi druge organizacije — to
// ostaje na `/admin/users` (globalni admin).

// defaultOrgSeats je broj naloga po organizaciji kada `orgs.max_users` nije
// postavljen. Kod se deli ljudima, pa limit sprečava da se napravi 200 naloga.
const defaultOrgSeats = 10

// OrgUser je red u tabeli „Moji korisnici“.
type OrgUser struct {
	ID          int64          `db:"id"`
	Email       string         `db:"email"`
	Role        string         `db:"role"`
	Status      sql.NullString `db:"status"`
	IsOrgAdmin  bool           `db:"is_org_admin"`
	CreatedAt   time.Time      `db:"created_at"`
	LastLoginAt sql.NullTime   `db:"last_login_at"`
	Protocols   int            `db:"protocols"`
}

// orgHeader su podaci o organizaciji za zaglavlje strane.
type orgHeader struct {
	ID         int64  `db:"id"`
	Name       string `db:"name"`
	Kind       string `db:"kind"`
	InviteCode string `db:"invite_code"`
	MaxUsers   int    `db:"max_users"`
}

// orgScopeFor vraća organizaciju čijim nalozima akter sme da upravlja.
func orgScopeFor(userID int64) (int64, bool) {
	if !isOrgAdminUser(userID) {
		return 0, false
	}
	org := userOrgID(userID)
	return org, org != 0
}

func loadOrgHeader(orgID int64) (orgHeader, error) {
	var h orgHeader
	err := db.Get(&h, `SELECT id, name, kind,
			coalesce(invite_code, '') AS invite_code,
			coalesce(max_users, $2) AS max_users
		FROM orgs WHERE id = $1`, orgID, defaultOrgSeats)
	return h, err
}

// listOrgUsers vraća naloge jedne organizacije. Nema parametra za „sve“ —
// scope je uvek konkretna organizacija, pa se greška u upitu ne može
// pretvoriti u curenje tuđih naloga.
func listOrgUsers(orgID int64) ([]OrgUser, error) {
	var out []OrgUser
	err := db.Select(&out, `SELECT u.id, u.email, u.role, u.status, u.is_org_admin,
			u.created_at, u.last_login_at,
			(SELECT count(*) FROM protocols p WHERE p.user_id = u.id) AS protocols
		FROM users u
		WHERE u.org_id = $1
		ORDER BY (u.status = 'pending') DESC, (u.status = 'disabled'), u.id`, orgID)
	return out, err
}

// orgSeatUsage vraća (zauzeta, dozvoljena) mesta. Zauzeta su aktivni nalozi;
// `pending` ne troši mesto dok se ne odobri.
func orgSeatUsage(orgID int64) (used, max int) {
	max = defaultOrgSeats
	_ = db.Get(&max, `SELECT coalesce(max_users, $2) FROM orgs WHERE id = $1`, orgID, defaultOrgSeats)
	if max <= 0 {
		max = defaultOrgSeats
	}
	_ = db.Get(&used, `SELECT count(*) FROM users
		WHERE org_id = $1 AND coalesce(status, 'active') = 'active'`, orgID)
	return used, max
}

func seatAvailable(used, max int) bool { return used < max }

// countActiveOrgAdmins broji aktivne organizacijske administratore, ne
// računajući `excludeID`. Isti guard kao za globalnog admina: organizacija ne
// sme da ostane bez osobe koja može da odobri nalog.
func countActiveOrgAdmins(orgID, excludeID int64) int {
	var n int
	_ = db.Get(&n, `SELECT count(*) FROM users
		WHERE org_id = $1 AND is_org_admin
		  AND coalesce(status, 'active') = 'active' AND id <> $2`, orgID, excludeID)
	return n
}

// orgTarget je ciljni nalog sa podacima potrebnim za proveru dozvole.
type orgTarget struct {
	Role       string
	OrgID      int64
	IsOrgAdmin bool
	Status     string
}

func managedOrgUser(userID int64) (orgTarget, error) {
	var row struct {
		Role       string         `db:"role"`
		Org        sql.NullInt64  `db:"org_id"`
		IsOrgAdmin bool           `db:"is_org_admin"`
		Status     sql.NullString `db:"status"`
	}
	if err := db.Get(&row, `SELECT role, org_id, is_org_admin, status
		FROM users WHERE id = $1`, userID); err != nil {
		return orgTarget{}, err
	}
	t := orgTarget{Role: row.Role, IsOrgAdmin: row.IsOrgAdmin, Status: row.Status.String}
	if row.Org.Valid {
		t.OrgID = row.Org.Int64
	}
	return t, nil
}

// orgRoleManageable: organizacijski admin upravlja isključivo partnerskim
// nalozima (staff naloge vodi globalni admin).
func orgRoleManageable(role string) bool {
	return strings.EqualFold(strings.TrimSpace(role), RolePartner)
}

func absoluteURL(r *http.Request, path string) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host + path
}

// OrgUsersHandler je strana „Moji korisnici“ — GET lista, POST akcije.
func OrgUsersHandler(w http.ResponseWriter, r *http.Request) {
	c := GetAuthClaims(r)
	if c == nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	scope, ok := orgScopeFor(c.UserID)
	if !ok {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if r.Method == http.MethodPost {
		msg := handleOrgUserAction(r, scope)
		http.Redirect(w, r, "/org/users?msg="+url.QueryEscape(msg), http.StatusFound)
		return
	}

	org, err := loadOrgHeader(scope)
	if err != nil {
		http.Error(w, "Organisation not found", http.StatusNotFound)
		return
	}
	users, err := listOrgUsers(scope)
	if err != nil {
		http.Error(w, "Query failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	used, max := orgSeatUsage(scope)

	tmpl, err := template.New("org-users.html").Funcs(template.FuncMap(csrfTemplateFuncs(r))).ParseFiles("web/templates/org-users.html")
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = tmpl.Execute(w, map[string]any{
		"Org":        org,
		"Users":      users,
		"Used":       used,
		"Max":        max,
		"IsAdmin":    isAdminRole(r),
		"IsOrgAdmin": true,
		"UserRole":   userRole(r),
		"Message":    r.URL.Query().Get("msg"),
	})
}

// handleOrgUserAction izvršava POST i vraća poruku za `?msg=`.
//
// Svaka akcija nad nalogom prolazi kroz `managedOrgUser`, pa je `org_id` iz
// forme nemoguće podmetnuti: scope dolazi iz sesije, a ne iz zahteva.
func handleOrgUserAction(r *http.Request, scope int64) string {
	claims := GetAuthClaims(r)
	var actorID int64
	if claims != nil {
		actorID = claims.UserID
	}

	action := r.FormValue("action")
	if action == "rotate-code" {
		code, err := regenerateInviteCode(scope)
		if err != nil {
			return "Error: " + err.Error()
		}
		logUserEvent(actorID, 0, "rotate-invite-code", orgNameByID(scope))
		return "New registration code: " + code
	}

	id, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
	if id <= 0 {
		return "Missing user."
	}
	target, err := managedOrgUser(id)
	if err != nil {
		return "User not found."
	}
	if target.OrgID != scope {
		return "That account is not in your organisation."
	}
	if !orgRoleManageable(target.Role) {
		return "You can only manage partner accounts."
	}

	switch action {
	case "approve":
		if used, max := orgSeatUsage(scope); !seatAvailable(used, max) {
			return fmt.Sprintf("No free seat (%d/%d). Disable an unused account.", used, max)
		}
		if err := setUserStatus(id, UserStatusPending, UserStatusActive); err != nil {
			return "Error: " + err.Error()
		}
		logUserEvent(actorID, id, "org-approve", orgNameByID(scope))
		notifyAccountApproved(id)
		return "Account approved."

	case "reject":
		res, err := db.Exec(`DELETE FROM users WHERE id = $1 AND org_id = $2 AND status = $3`,
			id, scope, UserStatusPending)
		if err != nil {
			return "Error: " + err.Error()
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return "Account is not pending."
		}
		logUserEvent(actorID, id, "org-reject", "")
		return "Request rejected."

	case "disable":
		if id == actorID {
			return "You cannot disable your own account."
		}
		if target.IsOrgAdmin && countActiveOrgAdmins(scope, id) == 0 {
			return "You cannot disable the last organisation admin."
		}
		if err := setUserStatus(id, UserStatusActive, UserStatusDisabled); err != nil {
			return "Error: " + err.Error()
		}
		logUserEvent(actorID, id, "org-disable", "")
		return "Account disabled."

	case "enable":
		if used, max := orgSeatUsage(scope); !seatAvailable(used, max) {
			return fmt.Sprintf("No free seat (%d/%d).", used, max)
		}
		if err := setUserStatus(id, UserStatusDisabled, UserStatusActive); err != nil {
			return "Error: " + err.Error()
		}
		logUserEvent(actorID, id, "org-enable", "")
		return "Account re-enabled."

	case "set-org-admin":
		makeAdmin := r.FormValue("value") == "1"
		if makeAdmin {
			if target.Status != UserStatusActive {
				return "Only an active account can be an organisation admin."
			}
		} else if target.IsOrgAdmin && countActiveOrgAdmins(scope, id) == 0 {
			// Isti guard kao za globalnog admina: organizacija ne sme da ostane
			// bez osobe koja odobrava naloge.
			return "You cannot remove admin rights from the last organisation admin."
		}
		res, err := db.Exec(`UPDATE users SET is_org_admin = $1, updated_at = NOW()
			WHERE id = $2 AND org_id = $3`, makeAdmin, id, scope)
		if err != nil {
			return "Error: " + err.Error()
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return "User not found."
		}
		detail := "removed"
		if makeAdmin {
			detail = "dodeljeno"
		}
		logUserEvent(actorID, id, "org-set-org-admin", detail)
		return "Organisation admin rights: " + detail + "."

	case "reset-link":
		link, err := createPasswordResetLink(id, actorID)
		if err != nil {
			return "Error: " + err.Error()
		}
		logUserEvent(actorID, id, "org-reset-link", "")
		if notifyResetLink(id, link) {
			return "A one-time link was emailed to the user (valid for 24 h)."
		}
		return "Jednokratni link (važi 24 h): " + absoluteURL(r, link)
	}
	return "Nepoznata akcija."
}
