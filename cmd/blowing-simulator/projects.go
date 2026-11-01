package main

import (
	"database/sql"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"
)

// Projekti (prva faza): projekat je prvorazredni objekat, dodeljuje se
// organizacijama i korisnicima.
//
// VAŽNO: dodela na projekat **ne daje** pristup tuđim protokolima. Vidljivost
// protokola ostaje kako je u ACCESS.md (svoj upload / svoja organizacija /
// `protocol_access`). Projekat je kontekst za zadatke i izveštaje sa terena.
const (
	ProjectStatusActive = "active"
	ProjectStatusClosed = "closed"
)

const (
	ProjectOrgRoleContractor = "contractor"
	ProjectOrgRoleClient     = "client"
	ProjectOrgRoleInternal   = "internal"
)

func ensureProjectSchema(db *sqlx.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS projects (
			id         SERIAL PRIMARY KEY,
			number     TEXT NOT NULL UNIQUE,
			name       TEXT NOT NULL DEFAULT '',
			status     TEXT NOT NULL DEFAULT 'active',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS project_orgs (
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			org_id     INTEGER NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
			role       TEXT NOT NULL DEFAULT 'contractor',
			PRIMARY KEY (project_id, org_id)
		)`,
		`CREATE TABLE IF NOT EXISTS project_users (
			project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			PRIMARY KEY (project_id, user_id)
		)`,
		`ALTER TABLE protocols ADD COLUMN IF NOT EXISTS project_id INTEGER REFERENCES projects(id)`,
		`CREATE INDEX IF NOT EXISTS idx_protocols_project ON protocols(project_id)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("projects schema: %w", err)
		}
	}
	return nil
}

// backfillProjects pravi projekat za svaki postojeći `project_number` i
// povezuje protokole. Idempotentno.
func backfillProjects(db *sqlx.DB) error {
	var numbers []string
	if err := db.Select(&numbers, `SELECT DISTINCT trim(project_number) FROM protocols
		WHERE coalesce(trim(project_number), '') <> ''`); err != nil {
		return fmt.Errorf("backfill projects: %w", err)
	}
	for _, n := range numbers {
		var id int64
		if err := db.Get(&id, `INSERT INTO projects (number) VALUES ($1)
			ON CONFLICT (number) DO UPDATE SET number = EXCLUDED.number
			RETURNING id`, n); err != nil {
			return fmt.Errorf("backfill project %q: %w", n, err)
		}
		if _, err := db.Exec(`UPDATE protocols SET project_id = $1
			WHERE project_id IS NULL AND trim(project_number) = $2`, id, n); err != nil {
			return err
		}
	}
	return nil
}

// --- upiti za admin stranu ---

type projectRow2 struct {
	ID        int64  `db:"id"`
	Number    string `db:"number"`
	Name      string `db:"name"`
	Status    string `db:"status"`
	Protocols int    `db:"protocols"`
	Orgs      int    `db:"orgs"`
	Users     int    `db:"users"`
}

type projectOrgRow struct {
	OrgID int64  `db:"org_id"`
	Name  string `db:"name"`
	Role  string `db:"role"`
}

type projectUserRow struct {
	UserID int64  `db:"user_id"`
	Email  string `db:"email"`
	Role   string `db:"role"`
}

func listProjects() ([]projectRow2, error) {
	var out []projectRow2
	err := db.Select(&out, `SELECT p.id, p.number, p.name, p.status,
			(SELECT count(*) FROM protocols pr WHERE pr.project_id = p.id) AS protocols,
			(SELECT count(*) FROM project_orgs po WHERE po.project_id = p.id) AS orgs,
			(SELECT count(*) FROM project_users pu WHERE pu.project_id = p.id) AS users
		FROM projects p
		ORDER BY (p.status = 'active') DESC, p.number`)
	return out, err
}

// AdminProjectsHandler upravlja projektima i dodelama.
//
//	GET  /admin/projects
//	POST /admin/projects  action=create|add-org|remove-org|add-user|remove-user|set-status
func AdminProjectsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		msg := handleProjectAction(r)
		http.Redirect(w, r, "/admin/projects?msg="+url.QueryEscape(msg), http.StatusFound)
		return
	}

	projects, err := listProjects()
	if err != nil {
		http.Error(w, "Query failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	orgs, _ := listOrgs()
	users, _ := listAllUsers()

	// dodele po projektu
	orgsByProject := map[int64][]projectOrgRow{}
	usersByProject := map[int64][]projectUserRow{}
	var po []struct {
		ProjectID int64  `db:"project_id"`
		OrgID     int64  `db:"org_id"`
		Name      string `db:"name"`
		Role      string `db:"role"`
	}
	if err := db.Select(&po, `SELECT po.project_id, po.org_id, o.name, po.role
		FROM project_orgs po JOIN orgs o ON o.id = po.org_id ORDER BY o.name`); err != nil {
		log.Printf("project orgs: %v", err)
	}
	for _, x := range po {
		orgsByProject[x.ProjectID] = append(orgsByProject[x.ProjectID], projectOrgRow{OrgID: x.OrgID, Name: x.Name, Role: x.Role})
	}
	var pu []struct {
		ProjectID int64  `db:"project_id"`
		UserID    int64  `db:"user_id"`
		Email     string `db:"email"`
		Role      string `db:"role"`
	}
	if err := db.Select(&pu, `SELECT pu.project_id, pu.user_id, u.email, u.role
		FROM project_users pu JOIN users u ON u.id = pu.user_id ORDER BY u.email`); err != nil {
		log.Printf("project users: %v", err)
	}
	for _, x := range pu {
		usersByProject[x.ProjectID] = append(usersByProject[x.ProjectID], projectUserRow{UserID: x.UserID, Email: x.Email, Role: x.Role})
	}

	tmpl, err := template.ParseFiles("web/templates/admin-projects.html")
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = tmpl.Execute(w, map[string]any{
		"IsAdmin":        true,
		"UserRole":       userRole(r),
		"Projects":       projects,
		"Orgs":           orgs,
		"Users":          users,
		"OrgsByProject":  orgsByProject,
		"UsersByProject": usersByProject,
		"Message":        r.URL.Query().Get("msg"),
	})
}

func handleProjectAction(r *http.Request) string {
	projectID, _ := strconv.ParseInt(r.FormValue("project_id"), 10, 64)

	switch r.FormValue("action") {
	case "create":
		number := strings.TrimSpace(r.FormValue("number"))
		name := strings.TrimSpace(r.FormValue("name"))
		if number == "" {
			return "Broj projekta je obavezan."
		}
		var id int64
		if err := db.Get(&id, `INSERT INTO projects (number, name) VALUES ($1, $2)
			ON CONFLICT (number) DO UPDATE SET name = EXCLUDED.name RETURNING id`, number, name); err != nil {
			return "Greška: " + err.Error()
		}
		if _, err := db.Exec(`UPDATE protocols SET project_id = $1
			WHERE project_id IS NULL AND trim(project_number) = $2`, id, number); err != nil {
			return "Projekat sačuvan, ali povezivanje protokola nije uspelo: " + err.Error()
		}
		return "Projekat sačuvan: " + number

	case "add-org":
		orgID, _ := strconv.ParseInt(r.FormValue("org_id"), 10, 64)
		role := r.FormValue("org_role")
		if projectID <= 0 || orgID <= 0 {
			return "Izaberi projekat i organizaciju."
		}
		if role != ProjectOrgRoleContractor && role != ProjectOrgRoleClient && role != ProjectOrgRoleInternal {
			role = ProjectOrgRoleContractor
		}
		if _, err := db.Exec(`INSERT INTO project_orgs (project_id, org_id, role) VALUES ($1,$2,$3)
			ON CONFLICT (project_id, org_id) DO UPDATE SET role = EXCLUDED.role`, projectID, orgID, role); err != nil {
			return "Greška: " + err.Error()
		}
		return "Organizacija dodeljena projektu."

	case "remove-org":
		orgID, _ := strconv.ParseInt(r.FormValue("org_id"), 10, 64)
		if _, err := db.Exec(`DELETE FROM project_orgs WHERE project_id = $1 AND org_id = $2`, projectID, orgID); err != nil {
			return "Greška: " + err.Error()
		}
		return "Organizacija uklonjena sa projekta."

	case "add-user":
		userID, _ := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
		if projectID <= 0 || userID <= 0 {
			return "Izaberi projekat i korisnika."
		}
		if _, err := db.Exec(`INSERT INTO project_users (project_id, user_id) VALUES ($1,$2)
			ON CONFLICT DO NOTHING`, projectID, userID); err != nil {
			return "Greška: " + err.Error()
		}
		return "Korisnik dodeljen projektu."

	case "remove-user":
		userID, _ := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
		if _, err := db.Exec(`DELETE FROM project_users WHERE project_id = $1 AND user_id = $2`, projectID, userID); err != nil {
			return "Greška: " + err.Error()
		}
		return "Korisnik uklonjen sa projekta."

	case "set-status":
		status := r.FormValue("status")
		if status != ProjectStatusActive && status != ProjectStatusClosed {
			return "Nepoznat status."
		}
		if _, err := db.Exec(`UPDATE projects SET status = $1 WHERE id = $2`, status, projectID); err != nil {
			return "Greška: " + err.Error()
		}
		return "Status projekta promenjen."
	}
	return "Nepoznata akcija."
}

// projectIDForNumber vraća ID projekta za broj (0 ako ne postoji).
func projectIDForNumber(number string) int64 {
	number = strings.TrimSpace(number)
	if number == "" || db == nil {
		return 0
	}
	var id int64
	if err := db.Get(&id, `SELECT id FROM projects WHERE number = $1`, number); err != nil {
		if err != sql.ErrNoRows {
			log.Printf("project lookup %q: %v", number, err)
		}
		return 0
	}
	return id
}

// sqlNullProjectID / sqlNullProjectIDPtr prevode broj projekta u project_id
// (NULL kada projekat još ne postoji u tabeli `projects`).
func sqlNullProjectID(number string) sql.NullInt64 {
	id := projectIDForNumber(number)
	return sql.NullInt64{Int64: id, Valid: id != 0}
}

func sqlNullProjectIDPtr(number *string) sql.NullInt64 {
	if number == nil {
		return sql.NullInt64{}
	}
	return sqlNullProjectID(*number)
}
