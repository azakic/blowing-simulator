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

// --- redovi za prikaz ---

type taskRow struct {
	ID           int64          `db:"id"`
	ProjectID    int64          `db:"project_id"`
	Project      string         `db:"project"`
	ProjectName  string         `db:"project_name"`
	OrgID        sql.NullInt64  `db:"org_id"`
	OrgName      sql.NullString `db:"org_name"`
	UserID       sql.NullInt64  `db:"user_id"`
	UserEmail    sql.NullString `db:"user_email"`
	Title        string         `db:"title"`
	Description  string         `db:"description"`
	Status       string         `db:"status"`
	Priority     string         `db:"priority"`
	Address      string         `db:"address"`
	NVT          string         `db:"nvt"`
	DueDate      sql.NullString `db:"due_date"`
	DueTime      sql.NullString `db:"due_time"`
	CreatedAt    string         `db:"created_at"`
	UpdatedAt    sql.NullString `db:"updated_at"`
	CompletedAt  sql.NullString `db:"completed_at"`
	CreatorEmail sql.NullString `db:"creator_email"`
	Protocols    int            `db:"protocols"`
	IsOverdue    bool           `db:"is_overdue"`
}

const taskSelect = `
	SELECT t.id, t.project_id, p.number AS project,
	       coalesce(p.name, '') AS project_name,
	       t.org_id, o.name AS org_name,
	       t.user_id, u.email AS user_email,
	       t.title, t.description, t.status, t.priority,
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
	}
	if f.OrgID != 0 {
		v.Set("org", strconv.FormatInt(f.OrgID, 10))
	}
	return v.Encode()
}

func parseTaskFilter(r *http.Request) taskFilter {
	q := r.URL.Query()
	f := taskFilter{
		Status:  strings.TrimSpace(q.Get("status")),
		Project: strings.TrimSpace(q.Get("project")),
		Mine:    q.Get("mine") == "1",
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

// TasksHandler: lista zadataka + kreiranje + brza promena statusa.
//
//	GET  /tasks?status=&project=&mine=1&org=
//	POST /tasks  action=create|set-status|delete
func TasksHandler(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r)
	if claims == nil {
		http.Redirect(w, r, "/login?next=/tasks", http.StatusFound)
		return
	}

	if r.Method == http.MethodPost {
		msg := handleTaskAction(r)
		http.Redirect(w, r, "/tasks?msg="+url.QueryEscape(msg), http.StatusFound)
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

	// Opcije za forme
	type projectOption struct {
		ID     int64  `db:"id"`
		Number string `db:"number"`
		Name   string `db:"name"`
		Status string `db:"status"`
	}
	var projects []projectOption
	projQuery := `SELECT id, number, coalesce(name,'') AS name, status FROM projects
		WHERE status = 'active' ORDER BY number`
	if !canSeeAllTasks(r) {
		if orgID := userOrgID(claims.UserID); orgID != 0 {
			projQuery = `SELECT p.id, p.number, coalesce(p.name,'') AS name, p.status
				FROM projects p JOIN project_orgs po ON po.project_id = p.id
				WHERE p.status = 'active' AND po.org_id = $1 ORDER BY p.number`
			_ = db.Select(&projects, projQuery, orgID)
		}
	} else {
		_ = db.Select(&projects, projQuery)
	}

	orgs, _ := listOrgs()

	type userOption struct {
		ID      int64          `db:"id"`
		Email   string         `db:"email"`
		OrgID   sql.NullInt64  `db:"org_id"`
		OrgName sql.NullString `db:"org_name"`
	}
	var users []userOption
	usersQuery := `SELECT u.id, u.email, u.org_id, o.name AS org_name
		FROM users u LEFT JOIN orgs o ON o.id = u.org_id
		WHERE coalesce(u.status,'active') = 'active' ORDER BY u.email`
	userArgs := []interface{}{}
	if !canSeeAllTasks(r) {
		usersQuery = `SELECT u.id, u.email, u.org_id, o.name AS org_name
			FROM users u LEFT JOIN orgs o ON o.id = u.org_id
			WHERE coalesce(u.status,'active') = 'active' AND u.org_id = $1 ORDER BY u.email`
		userArgs = append(userArgs, userOrgID(claims.UserID))
	}
	_ = db.Select(&users, usersQuery, userArgs...)

	canCreate := false
	for _, p := range projects {
		if canCreateTask(r, p.ID) {
			canCreate = true
			break
		}
	}

	tmpl, err := template.New("tasks.html").Funcs(template.FuncMap(csrfTemplateFuncs(r))).
		Funcs(template.FuncMap{"dotTime": dotTime}).
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

func handleTaskAction(r *http.Request) string {
	claims := GetAuthClaims(r)
	if claims == nil {
		return "Sesija je istekla."
	}
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)

	switch r.FormValue("action") {
	case "create":
		return createTask(r, claims)

	case "set-status":
		t, err := loadTask(id)
		if err != nil {
			return "Zadatak nije nađen."
		}
		if !canChangeTaskStatus(r, t) {
			return "Nemaš pravo da menjaš ovaj zadatak."
		}
		status := r.FormValue("status")
		if !validTaskStatus(status) {
			return "Nepoznat status."
		}
		return applyTaskStatus(id, status)

	case "delete":
		t, err := loadTask(id)
		if err != nil {
			return "Zadatak nije nađen."
		}
		if !canManageTask(r, t) {
			return "Nemaš pravo da obrišeš ovaj zadatak."
		}
		if _, err := db.Exec(`DELETE FROM tasks WHERE id = $1`, id); err != nil {
			return "Greška: " + err.Error()
		}
		logUserEvent(claims.UserID, 0, "task-delete", fmt.Sprintf("task %d", id))
		return "Zadatak obrisan."
	}
	return "Nepoznata akcija."
}

func createTask(r *http.Request, claims *AuthClaims) string {
	projectID, _ := strconv.ParseInt(r.FormValue("project_id"), 10, 64)
	if !canCreateTask(r, projectID) {
		return "Nemaš pravo da kreiraš zadatak na tom projektu."
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		return "Naslov zadatka je obavezan."
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
		if !userBelongsToOrg(userID, orgID) {
			return "Izabrani korisnik nije u toj organizaciji."
		}
	}

	due, dueTime, msg := parseDue(r)
	if msg != "" {
		return msg
	}

	var newID int64
	err := db.Get(&newID, `INSERT INTO tasks
			(project_id, org_id, user_id, title, description, priority,
			 address, nvt, due_date, due_time, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9,'')::date,
		        NULLIF($10,'')::time, $11)
		RETURNING id`,
		projectID, sqlNullInt64(orgID), sqlNullInt64(userID), title, description, priority,
		strings.TrimSpace(r.FormValue("address")), strings.TrimSpace(r.FormValue("nvt")),
		due, dueTime, claims.UserID)
	if err != nil {
		return "Greška: " + err.Error()
	}
	logUserEvent(claims.UserID, userID, "task-create", fmt.Sprintf("task %d project %d", newID, projectID))
	if userID > 0 {
		notifyTaskAssigned(userID, newID)
	}
	return "Zadatak kreiran."
}

func applyTaskStatus(id int64, status string) string {
	_, err := db.Exec(`UPDATE tasks SET status = $1, updated_at = NOW(),
			completed_at = CASE WHEN $1 = 'done' THEN NOW() ELSE NULL END
		WHERE id = $2`, status, id)
	if err != nil {
		return "Greška: " + err.Error()
	}
	return "Status zadatka promenjen."
}

func userBelongsToOrg(userID, orgID int64) bool {
	if userID <= 0 {
		return true
	}
	if orgID <= 0 {
		return true // bez organizacije: bilo koji aktivan korisnik
	}
	var n int
	if err := db.Get(&n, `SELECT count(*) FROM users WHERE id = $1 AND org_id = $2`, userID, orgID); err != nil {
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
	if id <= 0 {
		id, _ = strconv.ParseInt(r.FormValue("id"), 10, 64)
	}
	t, err := loadTask(id)
	if err != nil {
		http.Error(w, "Zadatak nije nađen", http.StatusNotFound)
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
		Funcs(template.FuncMap{"dotTime": dotTime}).
		ParseFiles("web/templates/task-detail.html")
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = tmpl.Execute(w, map[string]any{
		"Task":       t,
		"Linked":     linked,
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
		return "Sesija je istekla."
	}

	switch r.FormValue("action") {
	case "set-status":
		if !canChangeTaskStatus(r, t) {
			return "Nemaš pravo da menjaš status."
		}
		status := r.FormValue("status")
		if !validTaskStatus(status) {
			return "Nepoznat status."
		}
		return applyTaskStatus(t.ID, status)

	case "update":
		if !canManageTask(r, t) {
			return "Nemaš pravo da menjaš ovaj zadatak."
		}
		title := strings.TrimSpace(r.FormValue("title"))
		if title == "" {
			return "Naslov je obavezan."
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
		due, dueTime, msg := parseDue(r)
		if msg != "" {
			return msg
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
			if !userBelongsToOrg(userID, orgID) {
				return "Izabrani korisnik nije u toj organizaciji."
			}
		}

		_, err := db.Exec(`UPDATE tasks SET title=$1, description=$2, priority=$3,
				address=$4, nvt=$5, due_date=NULLIF($6,'')::date,
				due_time=NULLIF($7,'')::time,
				org_id=$8, user_id=$9, updated_at=NOW()
			WHERE id=$10`,
			title, description, priority,
			strings.TrimSpace(r.FormValue("address")), strings.TrimSpace(r.FormValue("nvt")),
			due, dueTime, sqlNullInt64(orgID), sqlNullInt64(userID), t.ID)
		if err != nil {
			return "Greška: " + err.Error()
		}
		// Novo dodeljenom korisniku stiže obaveštenje.
		if userID > 0 && (!t.UserID.Valid || t.UserID.Int64 != userID) {
			notifyTaskAssigned(userID, t.ID)
		}
		return "Zadatak sačuvan."

	case "link-protocol":
		if !canManageTask(r, t) {
			return "Nemaš pravo da menjaš ovaj zadatak."
		}
		pid, err := strconv.Atoi(strings.TrimSpace(r.FormValue("protocol_id")))
		if err != nil || pid <= 0 {
			return "Unesi ispravan ID protokola."
		}
		if !protocolVisibleToUser(claims, pid) {
			return "Taj protokol nije vidljiv tvom nalogu."
		}
		if _, err := db.Exec(`INSERT INTO task_protocols (task_id, protocol_id)
			VALUES ($1,$2) ON CONFLICT DO NOTHING`, t.ID, pid); err != nil {
			return "Greška: " + err.Error()
		}
		return "Protokol povezan sa zadatkom."

	case "unlink-protocol":
		if !canManageTask(r, t) {
			return "Nemaš pravo da menjaš ovaj zadatak."
		}
		pid, _ := strconv.Atoi(r.FormValue("protocol_id"))
		if _, err := db.Exec(`DELETE FROM task_protocols WHERE task_id=$1 AND protocol_id=$2`, t.ID, pid); err != nil {
			return "Greška: " + err.Error()
		}
		return "Veza sa protokolom uklonjena."
	}
	return "Nepoznata akcija."
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

// parseDue čita datum i vreme roka iz forme. Vreme je `HH:MM` (HTML time),
// a vraća se kao `HH:MM` koji PostgreSQL prihvata za TIME.
func parseDue(r *http.Request) (date, clock, errMsg string) {
	date = strings.TrimSpace(r.FormValue("due_date"))
	clock = strings.TrimSpace(r.FormValue("due_time"))
	if date != "" {
		if _, err := time.Parse("2006-01-02", date); err != nil {
			return "", "", "Datum roka nije ispravan."
		}
	}
	if clock != "" {
		clock = strings.ReplaceAll(clock, ".", ":") // 09.30 → 09:30 (nemačka notacija)
		t, err := time.Parse("15:04", clock)
		if err != nil {
			return "", "", "Vreme roka mora biti u formatu HH:MM."
		}
		clock = t.Format("15:04")
	}
	if date == "" && clock != "" {
		return "", "", "Vreme bez datuma nema smisla — unesi i datum."
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
	text := fmt.Sprintf("Dodeljen ti je zadatak.\n\nProjekat: %s\nZadatak: %s\n", t.Project, t.Title)
	if t.Address != "" {
		text += "Adresa: " + t.Address + "\n"
	}
	if t.NVT != "" {
		text += "NVT: " + t.NVT + "\n"
	}
	if t.DueDate.Valid {
		text += "Rok: " + t.DueDate.String
		if t.DueTime.Valid {
			text += " " + strings.ReplaceAll(t.DueTime.String, ":", ".")
		}
		text += "\n"
	}
	text += "\nOtvori: " + mail.link("/tasks/view?id="+strconv.FormatInt(taskID, 10)) + "\n"
	mail.Send([]string{email}, "Novi zadatak: "+t.Title, text)
}
