package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sort"
	"strings"
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

// ProjectReportHandler prikazuje sve projekte sa agregatima.
func ProjectReportHandler(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "Query failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var projects []projectRow
	var totalDistance float64
	var totalSeconds float64
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
			p.Project = "(no project number)"
		} else {
			p.Project = project
		}
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

// ProjectReportDetailHandler prikazuje sve protokole jednog projekta.
func ProjectReportDetailHandler(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	if project == "" {
		http.Error(w, "Project required", http.StatusBadRequest)
		return
	}

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
		http.Error(w, "Query failed: "+err.Error(), http.StatusInternalServerError)
		return
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

	data := map[string]interface{}{
		"Project":       project,
		"Protocols":     protocols,
		"Count":         len(protocols),
		"TotalDistance": formatMeters(totalDistance),
		"TotalDuration": formatSeconds(totalSeconds),
		"Workers":       sortedKeys(workers),
		"Orgs":          sortedKeys(orgs),
		"UserRole":      userRole(r),
		"IsAdmin":       isAdminRole(r),
	}
	renderTemplate(w, "reports-project.html", data)
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
