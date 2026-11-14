package main

import (
	"database/sql"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

// Zadaci sa terena (faza 2 projekata, vidi PROJECTS.md).
//
// Zadatak uvek pripada projektu; opciono je dodeljen organizaciji i/ili
// korisniku. Vidljivost:
//   - staff (admin/editor/viewer): svi zadaci
//   - partner: zadaci svoje organizacije ili lično dodeljeni
//
// Dodela na projekat NE proširuje pristup protokolima (ACCESS.md §2).

const (
	TaskStatusOpen       = "open"
	TaskStatusInProgress = "in_progress"
	TaskStatusDone       = "done"
	TaskStatusCancelled  = "cancelled"
)

const (
	TaskPriorityLow    = "low"
	TaskPriorityNormal = "normal"
	TaskPriorityHigh   = "high"
)

func validTaskStatus(s string) bool {
	switch s {
	case TaskStatusOpen, TaskStatusInProgress, TaskStatusDone, TaskStatusCancelled:
		return true
	}
	return false
}

func validTaskPriority(p string) bool {
	switch p {
	case TaskPriorityLow, TaskPriorityNormal, TaskPriorityHigh:
		return true
	}
	return false
}

func ensureTaskSchema(db *sqlx.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS tasks (
			id           SERIAL PRIMARY KEY,
			project_id   INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			org_id       INTEGER REFERENCES orgs(id) ON DELETE SET NULL,
			user_id      INTEGER REFERENCES users(id) ON DELETE SET NULL,
			title        TEXT NOT NULL,
			description  TEXT NOT NULL DEFAULT '',
			status       TEXT NOT NULL DEFAULT 'open',
			priority     TEXT NOT NULL DEFAULT 'normal',
			address      TEXT NOT NULL DEFAULT '',
			nvt          TEXT NOT NULL DEFAULT '',
			due_date     DATE,
			created_by   INTEGER REFERENCES users(id) ON DELETE SET NULL,
			created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at   TIMESTAMPTZ,
			completed_at TIMESTAMPTZ
		)`,
		// Vreme roka (HH:MM) uz datum — radnik dobija tačan termin u kalendaru.
		`ALTER TABLE tasks ADD COLUMN IF NOT EXISTS due_time TIME`,
		// Ista polja kao HaP (ha_protocols.client_first_name/client_last_name),
		// da se klijent iz zadatka prenese u HaP bez prepisivanja.
		`ALTER TABLE tasks ADD COLUMN IF NOT EXISTS client_first_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN IF NOT EXISTS client_last_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN IF NOT EXISTS client_phone TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN IF NOT EXISTS client_mobile TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN IF NOT EXISTS client_email TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN IF NOT EXISTS client_company TEXT NOT NULL DEFAULT ''`,
		// Normalizovana adresa (isto kao ha_customers); „address“ ostaje
		// izvedeni string za prikaz, Maps i pretragu.
		`ALTER TABLE tasks ADD COLUMN IF NOT EXISTS street TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN IF NOT EXISTS house_number TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN IF NOT EXISTS postcode TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN IF NOT EXISTS city TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_project ON tasks(project_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_org ON tasks(org_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_user ON tasks(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status)`,
		// Veza zadatak ↔ protokol (dokaz da je posao urađen).
		`CREATE TABLE IF NOT EXISTS task_protocols (
			task_id     INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
			protocol_id INTEGER NOT NULL REFERENCES protocols(id) ON DELETE CASCADE,
			PRIMARY KEY (task_id, protocol_id)
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("tasks schema: %w", err)
		}
	}
	return nil
}

// backfillTaskClients popunjava ime, prezime i telefon u zadacima koji su
// nastali pre tih polja. Uzima samo prazna polja iz direktorijuma klijenata
// (ista pravila kao lookup: adresa sa/bez grada, pa jedinstven NVT).
// Idempotentno — posle prvog prolaza nema šta da se menja.
func backfillTaskClients(db *sqlx.DB) error {
	var rows []struct {
		ID      int64  `db:"id"`
		Address string `db:"address"`
		NVT     string `db:"nvt"`
		Phone   string `db:"client_phone"`
		Mobile  string `db:"client_mobile"`
	}
	if err := db.Select(&rows, `SELECT id, coalesce(address,'') AS address,
			coalesce(nvt,'') AS nvt, coalesce(client_phone,'') AS client_phone,
			coalesce(client_mobile,'') AS client_mobile
		FROM tasks
		WHERE client_first_name = '' AND client_last_name = '' AND client_company = ''`); err != nil {
		return err
	}
	filled := 0
	for _, t := range rows {
		c, ok := customerForPrefill(t.NVT, t.Address)
		if !ok {
			continue
		}
		first, last, company := customerNameParts(c.Kunde)
		if first == "" && last == "" && company == "" {
			continue
		}
		phone := t.Phone
		if phone == "" {
			phone = c.Phone
			if phone == "" {
				phone = c.Mobile
			}
		}
		mobile := t.Mobile
		if mobile == "" {
			mobile = c.Mobile
		}
		if _, err := db.Exec(`UPDATE tasks SET client_first_name=$1, client_last_name=$2,
				client_company=$3, client_phone=$4, client_mobile=$5 WHERE id=$6`,
			first, last, company, phone, mobile, t.ID); err != nil {
			return err
		}
		filled++
	}
	if filled > 0 {
		log.Printf("backfill tasks: client fields filled for %d task(s)", filled)
	}
	return nil
}

// backfillAddressParts deli postojeće slobodne adrese u direktorijumu i u
// zadacima na ulicu/broj/PLZ/grad (i računa street_key). Idempotentno.
func backfillAddressParts(db *sqlx.DB) error {
	type row struct {
		ID      int64  `db:"id"`
		Address string `db:"address"`
	}
	var cust []row
	if err := db.Select(&cust, `SELECT id, address FROM ha_customers
		WHERE address <> '' AND street = ''`); err != nil {
		return err
	}
	for _, r := range cust {
		st, no, plz, city := parseAddress(r.Address)
		if strings.TrimSpace(st) == "" {
			st = strings.TrimSpace(r.Address) // nikad ne gubi tekst
		}
		if _, err := db.Exec(`UPDATE ha_customers SET street=$1, house_number=$2,
				postcode=$3, city=$4, street_key=$5 WHERE id=$6`,
			st, no, plz, city, streetKey(st, no), r.ID); err != nil {
			return err
		}
	}
	var tasks []row
	if err := db.Select(&tasks, `SELECT id, address FROM tasks
		WHERE address <> '' AND street = ''`); err != nil {
		return err
	}
	for _, r := range tasks {
		st, no, plz, city := parseAddress(r.Address)
		if strings.TrimSpace(st) == "" {
			st = strings.TrimSpace(r.Address)
		}
		if _, err := db.Exec(`UPDATE tasks SET street=$1, house_number=$2,
				postcode=$3, city=$4 WHERE id=$5`, st, no, plz, city, r.ID); err != nil {
			return err
		}
	}
	if len(cust)+len(tasks) > 0 {
		log.Printf("backfill addresses: customers=%d tasks=%d", len(cust), len(tasks))
	}
	return nil
}

// --- redovi za prikaz ---

type taskRow struct {
	ID            int64          `db:"id"`
	ProjectID     int64          `db:"project_id"`
	Project       string         `db:"project"`
	ProjectName   string         `db:"project_name"`
	OrgID         sql.NullInt64  `db:"org_id"`
	OrgName       sql.NullString `db:"org_name"`
	UserID        sql.NullInt64  `db:"user_id"`
	UserEmail     sql.NullString `db:"user_email"`
	Title         string         `db:"title"`
	ClientFirst   string         `db:"client_first_name"`
	ClientLast    string         `db:"client_last_name"`
	ClientPhone   string         `db:"client_phone"`
	ClientMobile  string         `db:"client_mobile"`
	ClientEmail   string         `db:"client_email"`
	ClientCompany string         `db:"client_company"`
	Street        string         `db:"street"`
	HouseNumber   string         `db:"house_number"`
	StreetNumber  string         `db:"street_number"`
	Postcode      string         `db:"postcode"`
	City          string         `db:"city"`
	Description   string         `db:"description"`
	Status        string         `db:"status"`
	Priority      string         `db:"priority"`
	Address       string         `db:"address"`
	NVT           string         `db:"nvt"`
	DueDate       sql.NullString `db:"due_date"`
	DueTime       sql.NullString `db:"due_time"`
	CreatedAt     string         `db:"created_at"`
	UpdatedAt     sql.NullString `db:"updated_at"`
	CompletedAt   sql.NullString `db:"completed_at"`
	CreatorEmail  sql.NullString `db:"creator_email"`
	Protocols     int            `db:"protocols"`
	IsOverdue     bool           `db:"is_overdue"`
}

const taskSelect = `
	SELECT t.id, t.project_id, p.number AS project,
	       coalesce(p.name, '') AS project_name,
	       t.org_id, o.name AS org_name,
	       t.user_id, u.email AS user_email,
	       t.title, t.description, t.status, t.priority,
	       t.client_first_name, t.client_last_name, t.client_phone,
	       t.client_mobile, t.client_email, t.client_company,
	       t.street, t.house_number, t.postcode, t.city,
	       trim(t.street || ' ' || t.house_number) AS street_number,
	       t.address, t.nvt,
	       to_char(t.due_date, 'YYYY-MM-DD') AS due_date,
	       to_char(t.due_time, 'HH24:MI') AS due_time,
	       to_char(t.created_at, 'YYYY-MM-DD HH24:MI') AS created_at,
	       to_char(t.updated_at, 'YYYY-MM-DD HH24:MI') AS updated_at,
	       to_char(t.completed_at, 'YYYY-MM-DD HH24:MI') AS completed_at,
	       c.email AS creator_email,
	       (SELECT count(*) FROM task_protocols tp WHERE tp.task_id = t.id) AS protocols,
	       (t.due_date IS NOT NULL AND t.due_date < CURRENT_DATE
	        AND t.status NOT IN ('done','cancelled')) AS is_overdue
	FROM tasks t
	JOIN projects p ON p.id = t.project_id
	LEFT JOIN orgs o ON o.id = t.org_id
	LEFT JOIN users u ON u.id = t.user_id
	LEFT JOIN users c ON c.id = t.created_by`

// taskFilter su filteri liste zadataka.
type taskFilter struct {
	Status  string
	Project string
	Mine    bool
	OrgID   int64
}

func (f taskFilter) queryString() string {
	v := url.Values{}
	if f.Status != "" && f.Status != "active" {
		v.Set("status", f.Status)
	}
	if f.Project != "" {
		v.Set("project", f.Project)
	}
	if f.Mine {
		v.Set("mine", "1")
	} else {
		v.Set("mine", "0")
	}
	if f.OrgID != 0 {
		v.Set("org", strconv.FormatInt(f.OrgID, 10))
	}
	return v.Encode()
}

func parseTaskFilter(r *http.Request) taskFilter {
	q := r.URL.Query()
	// Podrazumevano prikazujemo samo zadatke dodeljene prijavljenom korisniku.
	// `mine=0` (odčekiran checkbox u formi) prikazuje i tuđe zadatke.
	mine := true
	if vals, ok := q["mine"]; ok {
		mine = false
		for _, v := range vals {
			if v == "1" {
				mine = true
			}
		}
	}
	f := taskFilter{
		Status:  strings.TrimSpace(q.Get("status")),
		Project: strings.TrimSpace(q.Get("project")),
		Mine:    mine,
	}
	if v, err := strconv.ParseInt(strings.TrimSpace(q.Get("org")), 10, 64); err == nil && v > 0 {
		f.OrgID = v
	}
	return f
}

// --- dozvole ---

// taskScope vraća (orgID, jeOrgAdmin) za partnera.
func taskScope(r *http.Request) (int64, bool) {
	claims := GetAuthClaims(r)
	if claims == nil {
		return 0, false
	}
	if seesAllProtocols(claims.Role) {
		return 0, false
	}
	return orgScopeFor(claims.UserID)
}

func canSeeAllTasks(r *http.Request) bool {
	claims := GetAuthClaims(r)
	return claims != nil && seesAllProtocols(claims.Role)
}

// canCreateTask: staff na aktivnim projektima; org admin samo gde je njegova
// organizacija dodeljena projektu; običan partner ne.
func canCreateTask(r *http.Request, projectID int64) bool {
	claims := GetAuthClaims(r)
	if claims == nil || projectID <= 0 {
		return false
	}
	var status string
	if err := db.Get(&status, `SELECT status FROM projects WHERE id = $1`, projectID); err != nil {
		return false
	}
	if status != ProjectStatusActive {
		return false
	}
	if seesAllProtocols(claims.Role) {
		return true
	}
	orgID, ok := orgScopeFor(claims.UserID)
	if !ok || orgID == 0 {
		return false
	}
	var n int
	if err := db.Get(&n, `SELECT count(*) FROM project_orgs WHERE project_id = $1 AND org_id = $2`, projectID, orgID); err != nil {
		return false
	}
	return n > 0
}

// canManageTask: staff, ili org admin za zadatke svoje organizacije.
func canManageTask(r *http.Request, t taskRow) bool {
	claims := GetAuthClaims(r)
	if claims == nil {
		return false
	}
	if seesAllProtocols(claims.Role) {
		return true
	}
	orgID, ok := orgScopeFor(claims.UserID)
	return ok && orgID != 0 && t.OrgID.Valid && t.OrgID.Int64 == orgID
}

// canChangeTaskStatus: staff, org admin svoje organizacije, ili dodeljeni korisnik.
func canChangeTaskStatus(r *http.Request, t taskRow) bool {
	if canManageTask(r, t) {
		return true
	}
	claims := GetAuthClaims(r)
	return claims != nil && t.UserID.Valid && t.UserID.Int64 == claims.UserID
}

func loadTask(id int64) (taskRow, error) {
	var t taskRow
	err := db.Get(&t, taskSelect+" WHERE t.id = $1", id)
	return t, err
}

// canSeeTask: staff sve; partner svoju organizaciju ili lično dodeljeno.
func canSeeTask(r *http.Request, t taskRow) bool {
	if canSeeAllTasks(r) {
		return true
	}
	claims := GetAuthClaims(r)
	if claims == nil {
		return false
	}
	if t.UserID.Valid && t.UserID.Int64 == claims.UserID {
		return true
	}
	if orgID := userOrgID(claims.UserID); orgID != 0 && t.OrgID.Valid && t.OrgID.Int64 == orgID {
		return true
	}
	return false
}

// --- stranice ---

// taskProjectOption je jedna opcija u izboru projekta na formama zadatka.
type taskProjectOption struct {
	ID     int64  `db:"id"`
	Number string `db:"number"`
	Name   string `db:"name"`
	Status string `db:"status"`
}

// taskProjectOptions vraća aktivne projekte koje korisnik sme da koristi:
// staff sve, org admin samo projekte svoje organizacije.
func taskProjectOptions(r *http.Request) []taskProjectOption {
	var out []taskProjectOption
	if !canSeeAllTasks(r) {
		claims := GetAuthClaims(r)
		if claims == nil {
			return nil
		}
		orgID := userOrgID(claims.UserID)
		if orgID == 0 {
			return nil
		}
		_ = db.Select(&out, `SELECT p.id, p.number, coalesce(p.name,'') AS name, p.status
			FROM projects p JOIN project_orgs po ON po.project_id = p.id
			WHERE p.status = 'active' AND po.org_id = $1 ORDER BY p.number`, orgID)
		return out
	}
	_ = db.Select(&out, `SELECT id, number, coalesce(name,'') AS name, status
		FROM projects WHERE status = 'active' ORDER BY number`)
	return out
}

// TasksHandler: lista zadataka + kreiranje + brza promena statusa.
//
//	GET  /tasks?status=&project=&mine=1&org=
//	POST /tasks  action=create|set-status|delete
type taskUserOption struct {
	ID      int64          `db:"id"`
	Email   string         `db:"email"`
	OrgID   sql.NullInt64  `db:"org_id"`
	OrgName sql.NullString `db:"org_name"`
}

// taskAssignableUsers vraća korisnike koje trenutni korisnik sme da dodeli.
func taskAssignableUsers(r *http.Request) []taskUserOption {
	claims := GetAuthClaims(r)
	var users []taskUserOption
	q := `SELECT u.id, u.email, u.org_id, o.name AS org_name
		FROM users u LEFT JOIN orgs o ON o.id = u.org_id
		WHERE coalesce(u.status,'active') = 'active' ORDER BY u.email`
	var args []interface{}
	if claims != nil && !canSeeAllTasks(r) {
		q = `SELECT u.id, u.email, u.org_id, o.name AS org_name
			FROM users u LEFT JOIN orgs o ON o.id = u.org_id
			WHERE coalesce(u.status,'active') = 'active' AND u.org_id = $1 ORDER BY u.email`
		args = append(args, userOrgID(claims.UserID))
	}
	_ = db.Select(&users, q, args...)
	return users
}

// taskFormOptions priprema opcije za formu zadatka (novi i izmena).
func taskFormOptions(r *http.Request) (projects []taskProjectOption, canCreate bool) {
	projects = taskProjectOptions(r)
	for _, p := range projects {
		if canCreateTask(r, p.ID) {
			canCreate = true
			break
		}
	}
	return projects, canCreate
}

func TasksHandler(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r)
	if claims == nil {
		http.Redirect(w, r, "/login?next=/tasks", http.StatusFound)
		return
	}

	if r.Method == http.MethodPost {
		msg, target := handleTaskAction(r)
		sep := "?"
		if target == "" {
			target = "/tasks"
		} else if strings.Contains(target, "?") {
			sep = "&"
		}
		target += sep + "msg=" + url.QueryEscape(msg)
		http.Redirect(w, r, target, http.StatusFound)
		return
	}

	f := parseTaskFilter(r)

	// Osnovni opseg (vidljivost + projekat + dodela + org) — bez filtera statusa,
	// da KPI kartice uvek prikazuju sve statuse u opsegu.
	base := " WHERE 1=1"
	var baseArgs []interface{}
	arg := 0

	if !canSeeAllTasks(r) {
		orgID := userOrgID(claims.UserID)
		arg++
		baseArgs = append(baseArgs, claims.UserID)
		cond := fmt.Sprintf("(t.user_id = $%d", arg)
		if orgID != 0 {
			arg++
			baseArgs = append(baseArgs, orgID)
			cond += fmt.Sprintf(" OR t.org_id = $%d", arg)
		}
		cond += ")"
		base += " AND " + cond
	}

	if f.Project != "" {
		arg++
		base += fmt.Sprintf(" AND p.number = $%d", arg)
		baseArgs = append(baseArgs, f.Project)
	}
	if f.Mine {
		arg++
		base += fmt.Sprintf(" AND t.user_id = $%d", arg)
		baseArgs = append(baseArgs, claims.UserID)
	}
	if f.OrgID != 0 && canSeeAllTasks(r) {
		arg++
		base += fmt.Sprintf(" AND t.org_id = $%d", arg)
		baseArgs = append(baseArgs, f.OrgID)
	}

	// Filter statusa samo za listu; prazno znači "aktivni".
	where := base
	args := append([]interface{}{}, baseArgs...)
	switch f.Status {
	case TaskStatusOpen, TaskStatusInProgress, TaskStatusDone, TaskStatusCancelled:
		where += fmt.Sprintf(" AND t.status = $%d", arg+1)
		args = append(args, f.Status)
	case "all":
		// bez dodatnog uslova
	default:
		where += " AND t.status IN ('open','in_progress')"
	}

	query := taskSelect + where + `
		ORDER BY (t.status IN ('done','cancelled')) ASC,
		         (t.due_date IS NULL) ASC, t.due_date ASC, t.id DESC
		LIMIT 500`
	var tasks []taskRow
	if err := db.Select(&tasks, query, args...); err != nil {
		http.Error(w, "Query failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// KPI brojevi: ceo opseg, nezavisno od izabranog statusa.
	counts := map[string]int{}
	var countRows []struct {
		Status string `db:"status"`
		N      int    `db:"n"`
	}
	countQuery := `SELECT t.status, count(*) AS n FROM tasks t
		JOIN projects p ON p.id = t.project_id` + base + ` GROUP BY t.status`
	if err := db.Select(&countRows, countQuery, baseArgs...); err != nil {
		log.Printf("tasks counts: %v", err)
	}
	for _, c := range countRows {
		counts[c.Status] = c.N
	}

	projects, canCreate := taskFormOptions(r)
	orgs, _ := listOrgs()
	users := taskAssignableUsers(r)

	tmpl, err := template.New("tasks.html").Funcs(template.FuncMap(csrfTemplateFuncs(r))).
		Funcs(template.FuncMap{
			"dotTime":  dotTime,
			"dueLabel": dueLabel,
			"mapsURL":  mapsURL,
			"hours":    hourValues,
			"minutes":  minuteValues,
			"hourOf":   hourOf,
			"minOf":    minOf,
		}).
		ParseFiles("web/templates/tasks.html")
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = tmpl.Execute(w, map[string]any{
		"Tasks":       tasks,
		"Filter":      f,
		"QueryString": f.queryString(),
		"Counts":      counts,
		"Total":       len(tasks),
		"Projects":    projects,
		"Orgs":        orgs,
		"Users":       users,
		"CanCreate":   canCreate,
		"IsStaff":     canSeeAllTasks(r),
		"UserRole":    userRole(r),
		"IsAdmin":     isAdminRole(r),
		"IsOrgAdmin":  isOrgAdminRequest(r),
		"Message":     r.URL.Query().Get("msg"),
	})
}

// TaskNewHandler prikazuje formu za novi zadatak na zasebnoj strani
// (/tasks/new), da lista zadataka ostane pregled posla koji radnici rade.
func TaskNewHandler(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r)
	if claims == nil {
		http.Redirect(w, r, "/login?next=/tasks/new", http.StatusFound)
		return
	}
	if r.Method == http.MethodPost {
		id, msg := createTask(r, claims)
		if id > 0 {
			http.Redirect(w, r, "/tasks/view?id="+strconv.FormatInt(id, 10)+"&msg="+url.QueryEscape(msg), http.StatusFound)
			return
		}
		renderTaskNew(w, r, msg)
		return
	}
	renderTaskNew(w, r, "")
}

func renderTaskNew(w http.ResponseWriter, r *http.Request, message string) {
	projects, canCreate := taskFormOptions(r)
	if !canCreate {
		http.Error(w, "You are not allowed to create tasks.", http.StatusForbidden)
		return
	}
	orgs, _ := listOrgs()
	tmpl, err := template.New("task-new.html").Funcs(template.FuncMap(csrfTemplateFuncs(r))).
		Funcs(template.FuncMap{
			"hours":   hourValues,
			"minutes": minuteValues,
		}).
		ParseFiles("web/templates/task-new.html")
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = tmpl.Execute(w, map[string]any{
		"Projects":   projects,
		"Orgs":       orgs,
		"Users":      taskAssignableUsers(r),
		"IsStaff":    canSeeAllTasks(r),
		"UserRole":   userRole(r),
		"IsAdmin":    isAdminRole(r),
		"IsOrgAdmin": isOrgAdminRequest(r),
		"Message":    message,
	})
}

func handleTaskAction(r *http.Request) (msg, target string) {
	claims := GetAuthClaims(r)
	if claims == nil {
		return "Your session has expired.", ""
	}
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)

	switch r.FormValue("action") {
	case "create":
		newID, msg := createTask(r, claims)
		if newID > 0 {
			// Sa podrazumevanim filterom "Only mine" novi zadatak dodeljen
			// nekome drugom ne bi bio u listi — zato idemo na njegov detalj.
			return msg, "/tasks/view?id=" + strconv.FormatInt(newID, 10)
		}
		return msg, ""

	case "set-status":
		t, err := loadTask(id)
		if err != nil {
			return "Task not found.", ""
		}
		if !canChangeTaskStatus(r, t) {
			return "You are not allowed to change this task.", ""
		}
		status := r.FormValue("status")
		if !validTaskStatus(status) {
			return "Unknown status.", ""
		}
		return applyTaskStatus(id, status), ""

	case "delete":
		t, err := loadTask(id)
		if err != nil {
			return "Task not found.", ""
		}
		if !canManageTask(r, t) {
			return "You are not allowed to delete this task.", ""
		}
		if _, err := db.Exec(`DELETE FROM tasks WHERE id = $1`, id); err != nil {
			return "Error: " + err.Error(), ""
		}
		logUserEvent(claims.UserID, 0, "task-delete", fmt.Sprintf("task %d", id))
		return "Task deleted.", ""
	}
	return "Unknown action.", ""
}

// validateTaskRequired sprovodi obavezna polja zadatka: organizacija, adresa,
// NVT, datum i vreme. Naslov i opis su opcioni (naslov se izvodi iz adrese).
func validateTaskRequired(orgID int64, address, nvt, due, clock string) string {
	switch {
	case orgID <= 0:
		return "Organisation is required."
	case strings.TrimSpace(address) == "":
		return "Address is required."
	case strings.TrimSpace(nvt) == "":
		return "NVT is required."
	case due == "":
		return "Due date is required."
	case clock == "":
		return "Due time is required."
	}
	return ""
}

func createTask(r *http.Request, claims *AuthClaims) (int64, string) {
	projectID, _ := strconv.ParseInt(r.FormValue("project_id"), 10, 64)
	if projectID <= 0 {
		return 0, "Project is required."
	}
	if !canCreateTask(r, projectID) {
		return 0, "You are not allowed to create tasks on that project."
	}
	first := strings.TrimSpace(r.FormValue("client_first_name"))
	last := strings.TrimSpace(r.FormValue("client_last_name"))
	company := strings.TrimSpace(r.FormValue("client_company"))
	phone := strings.TrimSpace(r.FormValue("client_phone"))
	mobile := strings.TrimSpace(r.FormValue("client_mobile"))
	email := strings.TrimSpace(r.FormValue("client_email"))
	if len(phone) > 40 {
		phone = phone[:40]
	}
	if len(mobile) > 40 {
		mobile = mobile[:40]
	}
	if len(email) > 120 {
		email = email[:120]
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		title = taskClientName(first, last)
	}
	if title == "" {
		title = company
	}
	if len(title) > 200 {
		title = title[:200]
	}
	description := strings.TrimSpace(r.FormValue("description"))
	if len(description) > 4000 {
		description = description[:4000]
	}
	priority := r.FormValue("priority")
	if !validTaskPriority(priority) {
		priority = TaskPriorityNormal
	}

	// Organizacija: staff bira, org admin je vezan za svoju.
	var orgID int64
	if canSeeAllTasks(r) {
		orgID, _ = strconv.ParseInt(r.FormValue("org_id"), 10, 64)
	} else {
		orgID, _ = orgScopeFor(claims.UserID)
	}

	var userID int64
	if v, err := strconv.ParseInt(r.FormValue("user_id"), 10, 64); err == nil && v > 0 {
		userID = v
		if !userAssignable(userID) {
			return 0, "The selected user is not active."
		}
	}

	// Adresa: forma šalje „Straße und Hausnummer“ + PLZ + Ort; razdvajamo na
	// ulicu/broj, a „address“ gradimo za prikaz i pretragu.
	streetRaw := strings.TrimSpace(r.FormValue("street_number"))
	if streetRaw == "" {
		streetRaw = strings.TrimSpace(r.FormValue("address")) // stariji klijenti
	}
	postcode := strings.TrimSpace(r.FormValue("postcode"))
	city := strings.TrimSpace(r.FormValue("city"))
	street, houseNo, plzFromInput, cityFromInput := parseAddress(streetRaw)
	if postcode == "" {
		postcode = plzFromInput
	}
	if city == "" {
		city = cityFromInput
	}
	address := formatAddress(street, houseNo, postcode, city)
	nvt := strings.TrimSpace(r.FormValue("nvt"))
	due, dueTime, msg := parseDue(r)
	if msg != "" {
		return 0, msg
	}
	if msg := validateTaskRequired(orgID, address, nvt, due, dueTime); msg != "" {
		return 0, msg
	}
	// Naslov je opcion: bez naslova zadatak nosi adresu.
	if title == "" {
		title = address
	}

	var newID int64
	err := db.Get(&newID, `INSERT INTO tasks
			(project_id, org_id, user_id, title, description, priority,
			 client_first_name, client_last_name, client_company,
			 client_phone, client_mobile, client_email,
			 street, house_number, postcode, city,
			 address, nvt, due_date, due_time, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
		        $17, $18, NULLIF($19,'')::date, NULLIF($20,'')::time, $21)
		RETURNING id`,
		projectID, sqlNullInt64(orgID), sqlNullInt64(userID), title, description, priority,
		first, last, company, phone, mobile, email,
		street, houseNo, postcode, city,
		address, nvt, due, dueTime, claims.UserID)
	if err != nil {
		return 0, "Error: " + err.Error()
	}
	logUserEvent(claims.UserID, userID, "task-create", fmt.Sprintf("task %d project %d", newID, projectID))
	if userID > 0 {
		notifyTaskAssigned(userID, newID)
	}
	return newID, "Task created."
}

func applyTaskStatus(id int64, status string) string {
	_, err := db.Exec(`UPDATE tasks SET status = $1, updated_at = NOW(),
			completed_at = CASE WHEN $1 = 'done' THEN NOW() ELSE NULL END
		WHERE id = $2`, status, id)
	if err != nil {
		return "Error: " + err.Error()
	}
	return "Task status changed."
}

// userAssignable proverava samo da korisnik postoji i da je aktivan.
// Organizacija zadatka i korisnika ne moraju da se poklapaju: staff često
// dodeljuje zadatke kolegama bez organizacije, a vidljivost je već rešena
// pravilom "dodeljeni korisnik vidi svoj zadatak" (canSeeTask).
func userAssignable(userID int64) bool {
	if userID <= 0 {
		return true
	}
	var n int
	if err := db.Get(&n, `SELECT count(*) FROM users WHERE id = $1 AND coalesce(status,'active') = 'active'`, userID); err != nil {
		return false
	}
	return n > 0
}

// TaskDetailHandler: detalj zadatka + izmene.
//
//	GET  /tasks/view?id=
//	POST /tasks/view  action=update|set-status|link-protocol|unlink-protocol
func TaskDetailHandler(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if id <= 0 && r.Method == http.MethodPost {
		id, _ = strconv.ParseInt(r.FormValue("id"), 10, 64)
	}
	if id <= 0 {
		http.Error(w, "Missing task id", http.StatusBadRequest)
		return
	}
	t, err := loadTask(id)
	if err != nil {
		http.Error(w, "Task not found", http.StatusNotFound)
		return
	}
	if !canSeeTask(r, t) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	claims := GetAuthClaims(r)
	if r.Method == http.MethodPost {
		if claims == nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		msg := handleTaskDetailAction(r, t)
		http.Redirect(w, r, "/tasks/view?id="+strconv.FormatInt(t.ID, 10)+"&msg="+url.QueryEscape(msg), http.StatusFound)
		return
	}

	type linkedHA struct {
		ID            int64  `db:"id"`
		Address       string `db:"address"`
		City          string `db:"city"`
		ProtocolDate  string `db:"protocol_date"`
		CheckComplete bool   `db:"check_complete"`
	}
	var linkedHAs []linkedHA
	_ = db.Select(&linkedHAs, `SELECT id, address, coalesce(city,'') AS city,
			to_char(protocol_date,'YYYY-MM-DD') AS protocol_date, check_complete
		FROM ha_protocols WHERE task_id = $1 ORDER BY id DESC`, t.ID)

	type linkedProtocol struct {
		ID       int    `db:"id"`
		Type     string `db:"protocol_type"`
		Date     string `db:"d"`
		Filename string `db:"source_filename"`
	}
	var linked []linkedProtocol
	_ = db.Select(&linked, `SELECT p.id, p.protocol_type,
			coalesce(to_char(p.protocol_date,'YYYY-MM-DD'),'') AS d,
			coalesce(p.source_filename,'') AS source_filename
		FROM task_protocols tp JOIN protocols p ON p.id = tp.protocol_id
		WHERE tp.task_id = $1 ORDER BY p.protocol_date DESC, p.id DESC`, t.ID)

	projects := taskProjectOptions(r)
	// Trenutni projekat mora uvek biti u listi, čak i ako je zatvoren.
	projectInList := false
	for _, p := range projects {
		if p.ID == t.ProjectID {
			projectInList = true
			break
		}
	}
	if !projectInList {
		var cur taskProjectOption
		if err := db.Get(&cur, `SELECT id, number, coalesce(name,'') AS name, status
			FROM projects WHERE id = $1`, t.ProjectID); err == nil {
			projects = append([]taskProjectOption{cur}, projects...)
		}
	}

	orgs, _ := listOrgs()
	var users []struct {
		ID    int64         `db:"id"`
		Email string        `db:"email"`
		OrgID sql.NullInt64 `db:"org_id"`
	}
	usersQuery := `SELECT id, email, org_id FROM users
		WHERE coalesce(status,'active') = 'active' ORDER BY email`
	userArgs := []interface{}{}
	if !canSeeAllTasks(r) {
		usersQuery = `SELECT id, email, org_id FROM users
			WHERE coalesce(status,'active') = 'active' AND org_id = $1 ORDER BY email`
		userArgs = append(userArgs, userOrgID(claims.UserID))
	}
	_ = db.Select(&users, usersQuery, userArgs...)

	statuses := []struct{ Value, Label string }{
		{TaskStatusOpen, "Open"},
		{TaskStatusInProgress, "In progress"},
		{TaskStatusDone, "Done"},
		{TaskStatusCancelled, "Cancelled"},
	}
	priorities := []struct{ Value, Label string }{
		{TaskPriorityLow, "Low"},
		{TaskPriorityNormal, "Normal"},
		{TaskPriorityHigh, "High"},
	}

	tmpl, err := template.New("task-detail.html").Funcs(template.FuncMap(csrfTemplateFuncs(r))).
		Funcs(template.FuncMap{
			"dotTime":  dotTime,
			"dueLabel": dueLabel,
			"mapsURL":  mapsURL,
			"hours":    hourValues,
			"minutes":  func() []string { return minuteOptions(t.DueTime.String) },
			"hourOf":   hourOf,
			"minOf":    minOf,
		}).
		ParseFiles("web/templates/task-detail.html")
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = tmpl.Execute(w, map[string]any{
		"Task":       t,
		"Linked":     linked,
		"HAs":        linkedHAs,
		"Projects":   projects,
		"Orgs":       orgs,
		"Users":      users,
		"Statuses":   statuses,
		"Priorities": priorities,
		"CanManage":  canManageTask(r, t),
		"CanStatus":  canChangeTaskStatus(r, t),
		"IsStaff":    canSeeAllTasks(r),
		"UserRole":   userRole(r),
		"IsAdmin":    isAdminRole(r),
		"IsOrgAdmin": isOrgAdminRequest(r),
		"Message":    r.URL.Query().Get("msg"),
	})
}

func handleTaskDetailAction(r *http.Request, t taskRow) string {
	claims := GetAuthClaims(r)
	if claims == nil {
		return "Your session has expired."
	}

	switch r.FormValue("action") {
	case "set-status":
		if !canChangeTaskStatus(r, t) {
			return "You are not allowed to change the status."
		}
		status := r.FormValue("status")
		if !validTaskStatus(status) {
			return "Unknown status."
		}
		return applyTaskStatus(t.ID, status)

	case "update":
		if !canManageTask(r, t) {
			return "You are not allowed to change this task."
		}
		first := strings.TrimSpace(r.FormValue("client_first_name"))
		last := strings.TrimSpace(r.FormValue("client_last_name"))
		company := strings.TrimSpace(r.FormValue("client_company"))
		phone := strings.TrimSpace(r.FormValue("client_phone"))
		mobile := strings.TrimSpace(r.FormValue("client_mobile"))
		email := strings.TrimSpace(r.FormValue("client_email"))
		if len(phone) > 40 {
			phone = phone[:40]
		}
		if len(mobile) > 40 {
			mobile = mobile[:40]
		}
		if len(email) > 120 {
			email = email[:120]
		}
		explicitTitle := strings.TrimSpace(r.FormValue("title"))
		description := strings.TrimSpace(r.FormValue("description"))
		if len(description) > 4000 {
			description = description[:4000]
		}
		priority := r.FormValue("priority")
		if !validTaskPriority(priority) {
			priority = TaskPriorityNormal
		}
		due, dueTime, msg := parseDue(r)
		if msg != "" {
			return msg
		}

		projectID, _ := strconv.ParseInt(r.FormValue("project_id"), 10, 64)
		if projectID <= 0 {
			return "Project is required."
		}
		if projectID != t.ProjectID && !canCreateTask(r, projectID) {
			return "You are not allowed to move the task to that project."
		}

		var orgID int64
		if canSeeAllTasks(r) {
			orgID, _ = strconv.ParseInt(r.FormValue("org_id"), 10, 64)
		} else {
			orgID = t.OrgID.Int64
		}
		var userID int64
		if v, err := strconv.ParseInt(r.FormValue("user_id"), 10, 64); err == nil && v > 0 {
			userID = v
			if !userAssignable(userID) {
				return "The selected user is not active."
			}
		}

		streetRaw := strings.TrimSpace(r.FormValue("street_number"))
		if streetRaw == "" {
			streetRaw = strings.TrimSpace(r.FormValue("address"))
		}
		postcode := strings.TrimSpace(r.FormValue("postcode"))
		city := strings.TrimSpace(r.FormValue("city"))
		street, houseNo, plzFromInput, cityFromInput := parseAddress(streetRaw)
		if postcode == "" {
			postcode = plzFromInput
		}
		if city == "" {
			city = cityFromInput
		}
		address := formatAddress(street, houseNo, postcode, city)
		nvt := strings.TrimSpace(r.FormValue("nvt"))
		if msg := validateTaskRequired(orgID, address, nvt, due, dueTime); msg != "" {
			return msg
		}
		// Naslov se izvodi iz imena klijenta. Bez imena ostaje stari naslov
		// (da se ne pregaze postojeći), a ako ga nema — adresa.
		title := explicitTitle
		if title == "" {
			title = taskClientName(first, last)
		}
		if title == "" {
			title = company
		}
		if title == "" {
			title = strings.TrimSpace(t.Title)
		}
		if title == "" {
			title = address
		}
		if len(title) > 200 {
			title = title[:200]
		}

		_, err := db.Exec(`UPDATE tasks SET project_id=$1, title=$2, description=$3,
				priority=$4, address=$5, nvt=$6,
				due_date=NULLIF($7,'')::date, due_time=NULLIF($8,'')::time,
				org_id=$9, user_id=$10,
				client_first_name=$12, client_last_name=$13, client_company=$14,
				client_phone=$15, client_mobile=$16, client_email=$17,
				street=$18, house_number=$19, postcode=$20, city=$21,
				updated_at=NOW()
			WHERE id=$11`,
			projectID, title, description, priority, address, nvt,
			due, dueTime, sqlNullInt64(orgID), sqlNullInt64(userID), t.ID,
			first, last, company, phone, mobile, email,
			street, houseNo, postcode, city)
		if err != nil {
			return "Error: " + err.Error()
		}
		// Novo dodeljenom korisniku stiže obaveštenje.
		if userID > 0 && (!t.UserID.Valid || t.UserID.Int64 != userID) {
			notifyTaskAssigned(userID, t.ID)
		}
		return "Task saved."

	case "link-protocol":
		if !canManageTask(r, t) {
			return "You are not allowed to change this task."
		}
		pid, err := strconv.Atoi(strings.TrimSpace(r.FormValue("protocol_id")))
		if err != nil || pid <= 0 {
			return "Enter a valid protocol ID."
		}
		if !protocolVisibleToUser(claims, pid) {
			return "That protocol is not visible to your account."
		}
		if _, err := db.Exec(`INSERT INTO task_protocols (task_id, protocol_id)
			VALUES ($1,$2) ON CONFLICT DO NOTHING`, t.ID, pid); err != nil {
			return "Error: " + err.Error()
		}
		return "Protocol linked to the task."

	case "unlink-protocol":
		if !canManageTask(r, t) {
			return "You are not allowed to change this task."
		}
		pid, _ := strconv.Atoi(r.FormValue("protocol_id"))
		if _, err := db.Exec(`DELETE FROM task_protocols WHERE task_id=$1 AND protocol_id=$2`, t.ID, pid); err != nil {
			return "Error: " + err.Error()
		}
		return "Protocol link removed."
	}
	return "Unknown action."
}

// protocolVisibleToUser proverava vidljivost protokola istim pravilom kao liste
// (ACCESS.md): staff sve, partner svoje / svoje organizacije / eksplicitno.
func protocolVisibleToUser(claims *AuthClaims, protocolID int) bool {
	if claims == nil || protocolID <= 0 {
		return false
	}
	if seesAllProtocols(claims.Role) {
		var n int
		return db.Get(&n, `SELECT count(*) FROM protocols WHERE id = $1`, protocolID) == nil && n > 0
	}
	orgID := userOrgID(claims.UserID)
	var n int
	err := db.Get(&n, `SELECT count(*) FROM protocols p
		WHERE p.id = $1 AND (
			p.user_id = $2
			OR EXISTS (SELECT 1 FROM protocol_access pa WHERE pa.protocol_id = p.id AND pa.user_id = $2)
			OR ($3 <> 0 AND (p.owner_org_id = $3 OR p.service_provider_org_id = $3))
		)`, protocolID, claims.UserID, orgID)
	return err == nil && n > 0
}

// dotTime prikazuje vreme nemačkom notacijom (09:30 → 09.30).
func dotTime(s string) string { return strings.ReplaceAll(s, ":", ".") }

// taskClientName spaja ime i prezime klijenta u jedan naslov („Ime Prezime“).
func taskClientName(first, last string) string {
	v := strings.TrimSpace(strings.TrimSpace(first) + " " + strings.TrimSpace(last))
	if len(v) > 200 {
		v = v[:200]
	}
	return v
}

// mapsURL pravi Google Maps link za adresu; na telefonu se otvara u Maps
// aplikaciji, na računaru u web verziji.
func mapsURL(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}
	return "https://www.google.com/maps/search/?api=1&query=" + url.QueryEscape(q)
}

// dueLabel ispisuje datum dospeća kao dd.mm.yyyy (baza daje YYYY-MM-DD).
func dueLabel(v string) string {
	if len(v) >= 10 && v[4] == '-' && v[7] == '-' {
		return v[8:10] + "." + v[5:7] + "." + v[0:4]
	}
	return v
}

// hourValues/minuteValues pune padajuće liste u formama (00–23 / 00–59).
// Dva native <select>-a su mobilno prijateljska (točkić) i garantuju 24 h.
func hourValues() []string {
	out := make([]string, 24)
	for i := range out {
		out[i] = fmt.Sprintf("%02d", i)
	}
	return out
}

// minuteValues: minute se biraju na četvrt sata (00/15/30/45).
func minuteValues() []string {
	return []string{"00", "15", "30", "45"}
}

// minuteOptions vraća četvrti, a postojeći minut van njih zadržava kako stara
// vrednost ne bi nestala pri izmeni.
func minuteOptions(clock string) []string {
	out := minuteValues()
	m := minOf(clock)
	if m == "" {
		return out
	}
	for _, o := range out {
		if o == m {
			return out
		}
	}
	out = append(out, m)
	sort.Strings(out)
	return out
}

// hourOf/minOf izvlače delove iz "HH:MM" (prazno ako nema vremena).
func hourOf(clock string) string {
	if len(clock) >= 2 {
		return clock[:2]
	}
	return ""
}

func minOf(clock string) string {
	if len(clock) >= 5 {
		return clock[3:5]
	}
	return ""
}

// parseDue čita datum i vreme roka iz forme. Vreme je `HH:MM` (HTML time),
// a vraća se kao `HH:MM` koji PostgreSQL prihvata za TIME.
func parseDue(r *http.Request) (date, clock, errMsg string) {
	date = strings.TrimSpace(r.FormValue("due_date"))

	// Forme šalju dva select-a (sat + minut); stariji klijenti mogu i `due_time`.
	hour := strings.TrimSpace(r.FormValue("due_hour"))
	minute := strings.TrimSpace(r.FormValue("due_min"))
	if hour != "" || minute != "" {
		if hour == "" {
			return "", "", "Choose an hour as well."
		}
		if minute == "" {
			minute = "00"
		}
		clock = hour + ":" + minute
	} else {
		clock = strings.TrimSpace(r.FormValue("due_time"))
	}
	if date != "" {
		if _, err := time.Parse("2006-01-02", date); err != nil {
			return "", "", "Due date is not valid."
		}
	}
	if clock != "" {
		clock = strings.ReplaceAll(clock, ".", ":") // 09.30 → 09:30 (nemačka notacija)
		t, err := time.Parse("15:04", clock)
		if err != nil {
			return "", "", "Due time must be in HH.MM format (e.g. 09.30 or 14.05)."
		}
		clock = t.Format("15:04")
	}
	if date == "" && clock != "" {
		return "", "", "A time without a date makes no sense — enter a date as well."
	}
	return date, clock, ""
}

// notifyTaskAssigned šalje kratko obaveštenje dodeljenom korisniku.
func notifyTaskAssigned(userID, taskID int64) {
	if !mail.Enabled() {
		return
	}
	email := userEmail(userID)
	if email == "" {
		return
	}
	t, err := loadTask(taskID)
	if err != nil {
		return
	}
	text := fmt.Sprintf("You have been assigned a task.\n\nProject: %s\nTask: %s\n", t.Project, t.Title)
	if t.Address != "" {
		text += "Address: " + t.Address + "\n"
	}
	if t.NVT != "" {
		text += "NVT: " + t.NVT + "\n"
	}
	if t.DueDate.Valid {
		text += "Due: " + t.DueDate.String
		if t.DueTime.Valid {
			text += " " + strings.ReplaceAll(t.DueTime.String, ":", ".")
		}
		text += "\n"
	}
	text += "\nOpen: " + mail.link("/tasks/view?id="+strconv.FormatInt(taskID, 10)) + "\n"
	mail.Send([]string{email}, "New task: "+t.Title, text)
}
