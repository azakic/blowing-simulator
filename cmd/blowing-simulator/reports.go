package main

import (
	"encoding/csv"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// projectRow je jedan red u izveštaju „po projektu" (spaja Fremco i Jetting).
type projectRow struct {
	Project   string
	Protocols int
	Fremco    int
	Jetting   int
	FirstDate string
	LastDate  string
	Distance  string
	Duration  string
	Orgs      int
	Workers   int
	Points    int
	Quality   string

	// sirove vrednosti za CSV/izvoz
	DistanceM float64
	Seconds   float64
}

type protocolRow struct {
	ID        int
	Type      string
	Date      string
	Time      string
	Section   string
	Address   string
	Operator  string
	Org       string
	Distance  string
	Duration  string
	Points    int
	Original  bool
	ParseInfo string

	// sirove vrednosti za CSV/izvoz (nil = nema podatka)
	DistanceM *float64
	Seconds   *float64
}

// scopeFilter vraća SQL uslov i argumente za vidljivost (partner vidi svoje +
// dodeljeno kroz `protocol_access`; staff vidi sve).
func scopeFilter(r *http.Request, argStart int) (string, []interface{}) {
	claims := GetAuthClaims(r)
	if claims == nil || seesAllProtocols(claims.Role) {
		return "", nil
	}
	return fmt.Sprintf(` AND (p.user_id = $%[1]d OR EXISTS (
		SELECT 1 FROM protocol_access pa WHERE pa.protocol_id = p.id AND pa.user_id = $%[1]d))`, argStart),
		[]interface{}{claims.UserID}
}

const projectBaseQuery = `
	WITH scoped AS (
		SELECT p.id, p.protocol_type, p.project_number, p.protocol_date, p.start_time,
		       p.section_nvt, p.address, p.operator, p.company, p.service_provider,
		       p.source_sha256, p.parse_source,
		       COALESCE(s.total_distance,
		                (SELECT max(m.length_m) FROM protocol_measurements m WHERE m.protocol_id = p.id)) AS distance,
		       COALESCE(EXTRACT(EPOCH FROM s.blowing_time),
		                (SELECT EXTRACT(EPOCH FROM max(m.time_duration)) FROM protocol_measurements m WHERE m.protocol_id = p.id)) AS seconds,
		       (SELECT count(*) FROM protocol_measurements m WHERE m.protocol_id = p.id) AS points,
		       COALESCE(NULLIF(trim(p.service_provider), ''), NULLIF(trim(p.company), '')) AS org
		FROM protocols p
		LEFT JOIN protocol_summary s ON s.protocol_id = p.id
		WHERE 1=1 %s
	)
`

// loadProjects vraća sve projekte (u opsegu korisnika) sa agregatima.
func loadProjects(r *http.Request) ([]projectRow, float64, float64, int, error) {
	scope, args := scopeFilter(r, 1)

	query := fmt.Sprintf(projectBaseQuery, scope) + `
		SELECT COALESCE(trim(project_number), '') AS project,
		       count(*) AS protocols,
		       count(*) FILTER (WHERE protocol_type = 'fremco') AS fremco,
		       count(*) FILTER (WHERE protocol_type = 'jetting') AS jetting,
		       COALESCE(to_char(min(protocol_date), 'YYYY-MM-DD'), '') AS first_date,
		       COALESCE(to_char(max(protocol_date), 'YYYY-MM-DD'), '') AS last_date,
		       COALESCE(sum(distance), 0) AS distance,
		       COALESCE(sum(seconds), 0) AS seconds,
		       count(DISTINCT org) AS orgs,
		       count(DISTINCT NULLIF(trim(operator), '')) AS workers,
		       sum(points) AS points
		FROM scoped
		GROUP BY 1
		ORDER BY distance DESC NULLS LAST, 1`

	rows, err := db.Queryx(query, args...)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	defer rows.Close()

	var projects []projectRow
	var totalDistance, totalSeconds float64
	var totalProtocols int

	for rows.Next() {
		var project string
		var p projectRow
		var distance, seconds float64
		var points *int64

		if err := rows.Scan(&project, &p.Protocols, &p.Fremco, &p.Jetting,
			&p.FirstDate, &p.LastDate, &distance, &seconds, &p.Orgs, &p.Workers, &points); err != nil {
			log.Printf("project report scan: %v", err)
			continue
		}
		if strings.TrimSpace(project) == "" {
			p.Project = noProjectNumber
		} else {
			p.Project = project
		}
		p.DistanceM = distance
		p.Seconds = seconds
		p.Distance = formatMeters(distance)
		p.Duration = formatSeconds(seconds)
		if points != nil {
			p.Points = int(*points)
		}
		p.Quality = fmt.Sprintf("%d/%d", p.Protocols, p.Protocols)
		projects = append(projects, p)
		totalDistance += distance
		totalSeconds += seconds
		totalProtocols += p.Protocols
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, 0, err
	}
	return projects, totalDistance, totalSeconds, totalProtocols, nil
}

const noProjectNumber = "(no project number)"

// loadProjectDetail vraća sve protokole jednog projekta u opsegu korisnika.
func loadProjectDetail(r *http.Request, project string) ([]protocolRow, float64, float64, []string, []string, error) {
	scope, args := scopeFilter(r, 2)
	args = append([]interface{}{project}, args...)

	query := fmt.Sprintf(projectBaseQuery, scope) + `
		SELECT id, protocol_type,
		       COALESCE(to_char(protocol_date, 'YYYY-MM-DD'), '') AS date,
		       COALESCE(to_char(start_time, 'HH24:MI'), '') AS time,
		       COALESCE(section_nvt, ''), COALESCE(address, ''),
		       COALESCE(operator, ''), COALESCE(org, ''),
		       distance, seconds, points, source_sha256 IS NOT NULL,
		       COALESCE(parse_source, '')
		FROM scoped
		WHERE trim(project_number) = $1
		ORDER BY protocol_date, start_time, id`

	rows, err := db.Queryx(query, args...)
	if err != nil {
		return nil, 0, 0, nil, nil, err
	}
	defer rows.Close()

	var protocols []protocolRow
	var totalDistance, totalSeconds float64
	workers := map[string]bool{}
	orgs := map[string]bool{}

	for rows.Next() {
		var p protocolRow
		var distance, seconds *float64
		var points *int64
		var original bool
		var parseSource string

		if err := rows.Scan(&p.ID, &p.Type, &p.Date, &p.Time, &p.Section, &p.Address,
			&p.Operator, &p.Org, &distance, &seconds, &points, &original, &parseSource); err != nil {
			log.Printf("project detail scan: %v", err)
			continue
		}
		p.DistanceM = distance
		p.Seconds = seconds
		if distance != nil {
			p.Distance = formatMeters(*distance)
			totalDistance += *distance
		} else {
			p.Distance = "—"
		}
		if seconds != nil {
			p.Duration = formatSeconds(*seconds)
			totalSeconds += *seconds
		} else {
			p.Duration = "—"
		}
		if points != nil {
			p.Points = int(*points)
		}
		p.Original = original
		p.ParseInfo = parseSource
		if p.Operator != "" {
			workers[p.Operator] = true
		}
		if p.Org != "" {
			orgs[p.Org] = true
		}
		protocols = append(protocols, p)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, nil, nil, err
	}
	return protocols, totalDistance, totalSeconds, sortedKeys(workers), sortedKeys(orgs), nil
}

// ProjectReportHandler prikazuje sve projekte sa agregatima.
func ProjectReportHandler(w http.ResponseWriter, r *http.Request) {
	projects, totalDistance, totalSeconds, totalProtocols, err := loadProjects(r)
	if err != nil {
		http.Error(w, "Query failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	data := map[string]interface{}{
		"Projects":       projects,
		"TotalProjects":  len(projects),
		"TotalProtocols": totalProtocols,
		"TotalDistance":  formatMeters(totalDistance),
		"TotalDuration":  formatSeconds(totalSeconds),
		"UserRole":       userRole(r),
		"IsAdmin":        isAdminRole(r),
	}
	renderTemplate(w, "reports-projects.html", data)
}

// ProjectReportCSVHandler izvozi listu projekata kao CSV.
func ProjectReportCSVHandler(w http.ResponseWriter, r *http.Request) {
	projects, _, _, _, err := loadProjects(r)
	if err != nil {
		http.Error(w, "Query failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	header := []string{
		"Project", "Protocols", "Fremco", "Jetting", "First date", "Last date",
		"Distance (m)", "Blowing time (h:mm:ss)", "Organisations", "Workers",
	}
	rows := make([][]string, 0, len(projects))
	for _, p := range projects {
		rows = append(rows, []string{
			csvSafe(p.Project),
			strconv.Itoa(p.Protocols),
			strconv.Itoa(p.Fremco),
			strconv.Itoa(p.Jetting),
			p.FirstDate,
			p.LastDate,
			fmt.Sprintf("%.0f", p.DistanceM),
			formatClock(p.Seconds),
			strconv.Itoa(p.Orgs),
			strconv.Itoa(p.Workers),
		})
	}
	writeCSVResponse(w, "project-report-"+time.Now().Format("2006-01-02")+".csv", header, rows)
}

// ProjectReportDetailHandler prikazuje sve protokole jednog projekta.
func ProjectReportDetailHandler(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	if project == "" {
		http.Error(w, "Project required", http.StatusBadRequest)
		return
	}

	protocols, totalDistance, totalSeconds, workers, orgs, err := loadProjectDetail(r, project)
	if err != nil {
		http.Error(w, "Query failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	data := map[string]interface{}{
		"Project":       project,
		"Protocols":     protocols,
		"Count":         len(protocols),
		"TotalDistance": formatMeters(totalDistance),
		"TotalDuration": formatSeconds(totalSeconds),
		"Workers":       workers,
		"Orgs":          orgs,
		"UserRole":      userRole(r),
		"IsAdmin":       isAdminRole(r),
	}
	renderTemplate(w, "reports-project.html", data)
}

// ProjectReportDetailCSVHandler izvozi protokole jednog projekta kao CSV.
func ProjectReportDetailCSVHandler(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	if project == "" {
		http.Error(w, "Project required", http.StatusBadRequest)
		return
	}

	protocols, _, _, _, _, err := loadProjectDetail(r, project)
	if err != nil {
		http.Error(w, "Query failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	header := []string{
		"ID", "Type", "Date", "Time", "Section", "Address", "Operator",
		"Organisation", "Distance (m)", "Blowing time (h:mm:ss)", "Measurements",
		"Original", "Parse source",
	}
	rows := make([][]string, 0, len(protocols))
	for _, p := range protocols {
		distance := ""
		if p.DistanceM != nil {
			distance = fmt.Sprintf("%.0f", *p.DistanceM)
		}
		seconds := ""
		if p.Seconds != nil {
			seconds = formatClock(*p.Seconds)
		}
		original := ""
		if p.Original {
			original = "yes"
		}
		rows = append(rows, []string{
			strconv.Itoa(p.ID),
			p.Type,
			p.Date,
			p.Time,
			csvSafe(p.Section),
			csvSafe(p.Address),
			csvSafe(p.Operator),
			csvSafe(p.Org),
			distance,
			seconds,
			strconv.Itoa(p.Points),
			original,
			p.ParseInfo,
		})
	}
	writeCSVResponse(w, "project-"+csvFilenameSlug(project)+"-"+time.Now().Format("2006-01-02")+".csv", header, rows)
}

// writeCSVResponse piše CSV sa UTF-8 BOM-om i `;` kao separatorom (Excel na
// nemačkom sistemu otvara takav fajl direktno u kolonama).
func writeCSVResponse(w http.ResponseWriter, filename string, header []string, rows [][]string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write([]byte("\uFEFF")); err != nil {
		return
	}
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	if err := cw.Write(header); err != nil {
		log.Printf("csv header: %v", err)
		return
	}
	for _, row := range rows {
		if err := cw.Write(row); err != nil {
			log.Printf("csv row: %v", err)
			return
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		log.Printf("csv flush: %v", err)
	}
}

// csvSafe sprečava da vrednost iz PDF-a bude protumačena kao formula u Excel-u.
func csvSafe(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > 1 && strings.ContainsRune("=+-@", rune(v[0])) {
		return "'" + v
	}
	return v
}

// csvFilenameSlug zadržava samo bezbedne znake za ime fajla.
func csvFilenameSlug(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_.")
	if out == "" {
		return "project"
	}
	return out
}

func renderTemplate(w http.ResponseWriter, name string, data map[string]interface{}) {
	funcMap := template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
	}
	tmpl, err := template.New(name).Funcs(funcMap).ParseFiles("web/templates/" + name)
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("template %s: %v", name, err)
	}
}

func formatMeters(v float64) string {
	return fmt.Sprintf("%.0f m", v)
}

func formatSeconds(v float64) string {
	if v <= 0 {
		return "—"
	}
	total := int64(v)
	h := total / 3600
	m := (total % 3600) / 60
	if h > 0 {
		return fmt.Sprintf("%dh %02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// formatClock vraća HH:MM:SS (prazno kad vrednosti nema) — za CSV izvoz.
func formatClock(v float64) string {
	if v <= 0 {
		return ""
	}
	total := int64(v)
	return fmt.Sprintf("%02d:%02d:%02d", total/3600, (total%3600)/60, total%60)
}

func userRole(r *http.Request) string {
	if c := GetAuthClaims(r); c != nil {
		return c.Role
	}
	return ""
}

func isAdminRole(r *http.Request) bool {
	c := GetAuthClaims(r)
	return c != nil && strings.EqualFold(c.Role, RoleAdmin)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
