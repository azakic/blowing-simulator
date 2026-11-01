package main

import (
	"encoding/csv"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// orgShare je privremeni agregat po organizaciji (udeo u projektu).
type orgShare struct {
	protocols int
	distance  float64
	seconds   float64
	points    int
}

// orgShareRow je red tabele „Udeo po organizaciji".
type orgShareRow struct {
	Name        string
	Protocols   int
	Distance    string
	DistancePct string
	Duration    string
	DurationPct string
	Points      int
	PointsPct   string
}

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
	OwnerOrg  string
	CreditOrg string

	// sirove vrednosti za CSV/izvoz (nil = nema podatka)
	DistanceM *float64
	Seconds   *float64
}

// reportFilter su zajednički filteri izveštaja (`?org=&worker=&from=&to=`).
// Nevažeći datumi se ignorišu (prazno polje = bez ograničenja).
type reportFilter struct {
	Org    string
	Worker string
	From   string
	To     string
}

func (f reportFilter) HasAny() bool {
	return f.Org != "" || f.Worker != "" || f.From != "" || f.To != ""
}

// Values vraća aktivne filtere kao URL parametre.
func (f reportFilter) Values() url.Values {
	v := url.Values{}
	if f.Org != "" {
		v.Set("org", f.Org)
	}
	if f.Worker != "" {
		v.Set("worker", f.Worker)
	}
	if f.From != "" {
		v.Set("from", f.From)
	}
	if f.To != "" {
		v.Set("to", f.To)
	}
	return v
}

// QueryString vraća filtere kao URL query (bez `?`).
func (f reportFilter) QueryString() string {
	return f.Values().Encode()
}

// exportURL pravi link na CSV/izvoz sa filterima; template.URL sprečava
// html/template da `&` pretvori u `%26` (što bi pokvarilo query).
func exportURL(path string, f reportFilter, extra url.Values) template.URL {
	v := f.Values()
	for k, vals := range extra {
		for _, val := range vals {
			v.Add(k, val)
		}
	}
	if q := v.Encode(); q != "" {
		return template.URL(path + "?" + q)
	}
	return template.URL(path)
}

const maxFilterValueLen = 200

func parseReportFilter(r *http.Request) reportFilter {
	q := r.URL.Query()
	return reportFilter{
		Org:    cleanFilterValue(q.Get("org")),
		Worker: cleanFilterValue(q.Get("worker")),
		From:   cleanFilterDate(q.Get("from")),
		To:     cleanFilterDate(q.Get("to")),
	}
}

func cleanFilterValue(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > maxFilterValueLen {
		v = v[:maxFilterValueLen]
	}
	return v
}

func cleanFilterDate(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if _, err := time.Parse("2006-01-02", v); err != nil {
		return ""
	}
	return v
}

// queryBuilder dodeljuje `$n` placeholdere redom, da scope i filteri mogu da
// se kombinuju bez ručnog brojanja.
type queryBuilder struct {
	args []interface{}
}

func (b *queryBuilder) add(v interface{}) string {
	b.args = append(b.args, v)
	return fmt.Sprintf("$%d", len(b.args))
}

const projectBaseQuery = `
	WITH scoped AS (
		SELECT p.id, p.protocol_type, p.project_number, p.protocol_date, p.start_time,
		       p.section_nvt, p.address, p.operator, p.company, p.service_provider,
		       p.source_sha256, p.parse_source, p.owner_org_id,
		       COALESCE(s.total_distance,
		                (SELECT max(m.length_m) FROM protocol_measurements m WHERE m.protocol_id = p.id)) AS distance,
		       COALESCE(EXTRACT(EPOCH FROM s.blowing_time),
		                (SELECT EXTRACT(EPOCH FROM max(m.time_duration)) FROM protocol_measurements m WHERE m.protocol_id = p.id)) AS seconds,
		       (SELECT count(*) FROM protocol_measurements m WHERE m.protocol_id = p.id) AS points,
		       COALESCE(NULLIF(trim(p.service_provider), ''), NULLIF(trim(p.company), '')) AS org
		FROM protocols p
		LEFT JOIN protocol_summary s ON s.protocol_id = p.id
		%s
	)
`

// orgExpr je izraz koji se koristi i za kolonu `org` i za filter po organizaciji.
const orgExpr = `COALESCE(NULLIF(trim(p.service_provider), ''), NULLIF(trim(p.company), ''))`

// buildScopedQuery vraća CTE `scoped` sa scope-om korisnika i filterima.
func buildScopedQuery(r *http.Request, f reportFilter) (string, []interface{}) {
	b := &queryBuilder{}
	where := "WHERE 1=1"

	if claims := GetAuthClaims(r); claims != nil && !seesAllProtocols(claims.Role) {
		uid := b.add(claims.UserID)
		cond := fmt.Sprintf(`(p.user_id = %[1]s OR EXISTS (
			SELECT 1 FROM protocol_access pa WHERE pa.protocol_id = p.id AND pa.user_id = %[1]s)`, uid)
		// Ista organizacija (vidi OWNERSHIP.md).
		if orgID := userOrgID(claims.UserID); orgID != 0 {
			org := b.add(orgID)
			cond += fmt.Sprintf(" OR p.owner_org_id = %[1]s OR p.service_provider_org_id = %[1]s", org)
		}
		where += " AND " + cond + ")"
	}
	if f.Org != "" {
		where += " AND " + orgExpr + " = " + b.add(f.Org)
	}
	if f.Worker != "" {
		where += " AND trim(p.operator) = " + b.add(f.Worker)
	}
	if f.From != "" {
		where += " AND p.protocol_date >= " + b.add(f.From) + "::date"
	}
	if f.To != "" {
		where += " AND p.protocol_date <= " + b.add(f.To) + "::date"
	}

	return fmt.Sprintf(projectBaseQuery, where), b.args
}

// loadProjects vraća sve projekte (u opsegu korisnika, uz filtere) sa agregatima.
func loadProjects(r *http.Request, f reportFilter) ([]projectRow, float64, float64, int, error) {
	scoped, args := buildScopedQuery(r, f)

	query := scoped + `
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

// loadProjectDetail vraća sve protokole jednog projekta (u opsegu korisnika,
// uz filtere).
func loadProjectDetail(r *http.Request, project string, f reportFilter) ([]protocolRow, float64, float64, []string, []string, error) {
	scoped, args := buildScopedQuery(r, f)
	b := &queryBuilder{args: args}
	projectArg := b.add(project)

	query := scoped + `
		SELECT s.id, s.protocol_type,
		       COALESCE(to_char(s.protocol_date, 'YYYY-MM-DD'), '') AS date,
		       COALESCE(to_char(s.start_time, 'HH24:MI'), '') AS time,
		       COALESCE(s.section_nvt, ''), COALESCE(s.address, ''),
		       COALESCE(s.operator, ''), COALESCE(s.org, ''),
		       s.distance, s.seconds, s.points, s.source_sha256 IS NOT NULL,
		       COALESCE(s.parse_source, ''),
		       COALESCE(owner.name, ''),
		       COALESCE(credit.name, '')
		FROM scoped s
		LEFT JOIN orgs owner ON owner.id = s.owner_org_id
		LEFT JOIN orgs credit ON credit.id = COALESCE(
			(SELECT sp.id FROM orgs sp
			  WHERE sp.id = s.service_provider_org_id AND sp.kind <> 'internal'),
			s.owner_org_id)
		WHERE trim(s.project_number) = ` + projectArg + `
		ORDER BY s.protocol_date, s.start_time, s.id`

	rows, err := db.Queryx(query, b.args...)
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
			&p.Operator, &p.Org, &distance, &seconds, &points, &original, &parseSource,
			&p.OwnerOrg, &p.CreditOrg); err != nil {
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

// buildSharesFromProtocols agregira redove projekta po organizaciji koja nosi
// udeo (`owner_org_id`, tj. nalog koji je uvezao protokol) i računa procente.
func buildSharesFromProtocols(protocols []protocolRow) []orgShareRow {
	shares := map[string]*orgShare{}
	var totalDistance, totalSeconds float64
	var totalPoints int

	for _, p := range protocols {
		owner := p.CreditOrg
		if owner == "" {
			owner = "(no organisation)"
		}
		sh, ok := shares[owner]
		if !ok {
			sh = &orgShare{}
			shares[owner] = sh
		}
		sh.protocols++
		sh.points += p.Points
		totalPoints += p.Points
		if p.DistanceM != nil {
			sh.distance += *p.DistanceM
			totalDistance += *p.DistanceM
		}
		if p.Seconds != nil {
			sh.seconds += *p.Seconds
			totalSeconds += *p.Seconds
		}
	}
	return buildShares(shares, totalDistance, totalSeconds, totalPoints)
}

// buildShares pretvara agregat po organizaciji u redove za prikaz sa udelima.
func buildShares(shares map[string]*orgShare, totalDistance, totalSeconds float64, totalPoints int) []orgShareRow {
	out := make([]orgShareRow, 0, len(shares))
	for name, sh := range shares {
		out = append(out, orgShareRow{
			Name:        name,
			Protocols:   sh.protocols,
			Distance:    formatMeters(sh.distance),
			DistancePct: formatPct(sh.distance, totalDistance),
			Duration:    formatSeconds(sh.seconds),
			DurationPct: formatPct(sh.seconds, totalSeconds),
			Points:      sh.points,
			PointsPct:   formatPct(float64(sh.points), float64(totalPoints)),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Points != out[j].Points {
			return out[i].Points > out[j].Points
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func formatPct(part, total float64) string {
	if total <= 0 {
		return ""
	}
	return fmt.Sprintf("%.0f%%", part*100/total)
}

// loadFilterOptions vraća sve organizacije i radnike vidljive korisniku
// (ignoriše aktivne filtere, da se opcije ne skupljaju pri svakom filtriranju).
func loadFilterOptions(r *http.Request) ([]string, []string, error) {
	scoped, args := buildScopedQuery(r, reportFilter{})

	query := scoped + `
		SELECT kind, value FROM (
			SELECT 'org' AS kind, org AS value
			FROM scoped WHERE org IS NOT NULL
			UNION
			SELECT 'worker' AS kind, trim(operator) AS value
			FROM scoped WHERE operator IS NOT NULL AND trim(operator) <> ''
		) AS options
		ORDER BY kind, value`

	rows, err := db.Queryx(query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var orgs, workers []string
	for rows.Next() {
		var kind, value string
		if err := rows.Scan(&kind, &value); err != nil {
			log.Printf("filter options scan: %v", err)
			continue
		}
		if kind == "org" {
			orgs = append(orgs, value)
		} else {
			workers = append(workers, value)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return orgs, workers, nil
}

// ProjectReportHandler prikazuje sve projekte sa agregatima.
func ProjectReportHandler(w http.ResponseWriter, r *http.Request) {
	f := parseReportFilter(r)

	projects, totalDistance, totalSeconds, totalProtocols, err := loadProjects(r, f)
	if err != nil {
		http.Error(w, "Query failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	orgOptions, workerOptions, err := loadFilterOptions(r)
	if err != nil {
		http.Error(w, "Query failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	renderTemplate(w, "reports-projects.html", map[string]interface{}{
		"Projects":       projects,
		"TotalProjects":  len(projects),
		"TotalProtocols": totalProtocols,
		"TotalDistance":  formatMeters(totalDistance),
		"TotalDuration":  formatSeconds(totalSeconds),
		"OrgOptions":     orgOptions,
		"WorkerOptions":  workerOptions,
		"Filter":         f,
		"QueryString":    f.QueryString(),
		"ExportURL":      exportURL("/reports/projects.csv", f, nil),
		"HasFilter":      f.HasAny(),
		"UserRole":       userRole(r),
		"IsAdmin":        isAdminRole(r),
	})
}

// ProjectReportCSVHandler izvozi listu projekata kao CSV.
func ProjectReportCSVHandler(w http.ResponseWriter, r *http.Request) {
	f := parseReportFilter(r)

	projects, _, _, _, err := loadProjects(r, f)
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
	f := parseReportFilter(r)

	protocols, totalDistance, totalSeconds, workers, orgs, err := loadProjectDetail(r, project, f)
	if err != nil {
		http.Error(w, "Query failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	orgOptions, workerOptions, err := loadFilterOptions(r)
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
		"Shares":        buildSharesFromProtocols(protocols),
		"OrgOptions":    orgOptions,
		"WorkerOptions": workerOptions,
		"Filter":        f,
		"QueryString":   f.QueryString(),
		"ExportURL":     exportURL("/reports/project.csv", f, url.Values{"project": {project}}),
		"HasFilter":     f.HasAny(),
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
	f := parseReportFilter(r)

	protocols, _, _, _, _, err := loadProjectDetail(r, project, f)
	if err != nil {
		http.Error(w, "Query failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	header := []string{
		"ID", "Type", "Date", "Time", "Section", "Address", "Operator",
		"Organisation", "Owner org", "Credit org", "Distance (m)", "Blowing time (h:mm:ss)",
		"Measurements", "Original", "Parse source",
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
			csvSafe(p.OwnerOrg),
			csvSafe(p.CreditOrg),
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
