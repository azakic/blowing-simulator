package main

import (
	"context"
	"database/sql"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/jmoiron/sqlx"
)

// HaP = Hausanschlussprotokoll (Westnetz obrazac 1.5.1) koji pravimo iz forme
// i na kraju generišemo kao PDF u istom rasporedu.
//
// Forma pokriva sva tekstualna polja, izbor boja vlakana, tri potvrdna polja i
// devet priloga (4 fotografije, 2 nalepnice, Licht test, 2 potpisa).

type haPhotoKind struct {
	Key, Label string
}

// haPhotoKinds sadrži i stari fiksni ONT slot (migracija + PDF fallback).
var haPhotoKinds = []haPhotoKind{
	{"bild1", "Bild 1 — Hausanschluss gesamt"},
	{"bild2", "Bild 2 — APL offen"},
	{"bild3", "Bild 3 — APL geschlossen / Aufkleber"},
	{"bild4", "Bild 4 — Foto ONT"},
	{"ont_label", "ONT Aufkleber"},
	{"apl_label", "APL Aufkleber"},
	{"light_test", "Licht test"},
	{"sig_monteur", "Unterschrift Monteur"},
	{"sig_pruefer", "Unterschrift Prüfer"},
}

// haSlotPhotoKinds su fiksni slotovi koje forma prikazuje i čuva. ONT se sada
// unosi kroz ha_onts (više uređaja po HaP-u).
var haSlotPhotoKinds = []haPhotoKind{
	{"bild1", "Bild 1 — Hausanschluss gesamt"},
	{"bild2", "Bild 2 — APL offen"},
	{"bild3", "Bild 3 — APL geschlossen / Aufkleber"},
	{"bild4", "Bild 4 — Foto ONT"},
	{"light_test", "Licht test"},
}

func validHAKind(kind string) bool {
	for _, k := range haPhotoKinds {
		if k.Key == kind {
			return true
		}
	}
	return false
}

func ensureHASchema(db *sqlx.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS ha_protocols (
			id                SERIAL PRIMARY KEY,
			task_id           INTEGER REFERENCES tasks(id) ON DELETE SET NULL,
			project_id        INTEGER REFERENCES projects(id) ON DELETE SET NULL,
			org_id            INTEGER REFERENCES orgs(id) ON DELETE SET NULL,
			auftragnehmer     TEXT NOT NULL DEFAULT '',
			address           TEXT NOT NULL DEFAULT '',
			city              TEXT NOT NULL DEFAULT '',
			bauabschnitt      TEXT NOT NULL DEFAULT '',
			client_last_name  TEXT NOT NULL DEFAULT '',
			client_first_name TEXT NOT NULL DEFAULT '',
			units             INTEGER,
			protocol_date     DATE NOT NULL DEFAULT CURRENT_DATE,
			monteur           TEXT NOT NULL DEFAULT '',
			pruefer           TEXT NOT NULL DEFAULT '',
			project_number    TEXT NOT NULL DEFAULT '',
			gvs               TEXT NOT NULL DEFAULT '',
			kassette_b        TEXT NOT NULL DEFAULT '',
			mux_number        TEXT NOT NULL DEFAULT '',
			mux_fibers        TEXT NOT NULL DEFAULT '',
			splices           INTEGER,
			fiber_colors      TEXT NOT NULL DEFAULT '',
			cable_charge      TEXT NOT NULL DEFAULT '',
			cable_length_m    NUMERIC(10,1),
			check_fiber       BOOLEAN NOT NULL DEFAULT FALSE,
			check_complete    BOOLEAN NOT NULL DEFAULT FALSE,
			check_clean       BOOLEAN NOT NULL DEFAULT FALSE,
			created_by        INTEGER REFERENCES users(id) ON DELETE SET NULL,
			created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at        TIMESTAMPTZ
		)`,
		`ALTER TABLE ha_protocols ADD COLUMN IF NOT EXISTS nvt TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ha_protocols ADD COLUMN IF NOT EXISTS client_phone TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ha_protocols ADD COLUMN IF NOT EXISTS client_mobile TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ha_protocols ADD COLUMN IF NOT EXISTS apl_codes TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ha_protocols ADD COLUMN IF NOT EXISTS geo_address TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ha_protocols ADD COLUMN IF NOT EXISTS rohr INTEGER`,
		`ALTER TABLE ha_protocols ADD COLUMN IF NOT EXISTS rohr_color TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ha_protocols ADD COLUMN IF NOT EXISTS roehrchen_color TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ha_photos ADD COLUMN IF NOT EXISTS geo_address TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ha_photos ADD COLUMN IF NOT EXISTS ocr_text TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ha_photos ADD COLUMN IF NOT EXISTS ocr_time TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ha_photos ADD COLUMN IF NOT EXISTS ocr_at TIMESTAMPTZ`,
		`CREATE INDEX IF NOT EXISTS idx_ha_project ON ha_protocols(project_id)`,
		`CREATE INDEX IF NOT EXISTS idx_ha_org ON ha_protocols(org_id)`,
		`CREATE TABLE IF NOT EXISTS ha_photos (
			id          SERIAL PRIMARY KEY,
			ha_id       INTEGER NOT NULL REFERENCES ha_protocols(id) ON DELETE CASCADE,
			kind        TEXT NOT NULL,
			object_key  TEXT NOT NULL,
			content_type TEXT NOT NULL DEFAULT 'image/jpeg',
			bytes       INTEGER NOT NULL DEFAULT 0,
			lat         DOUBLE PRECISION,
			lon         DOUBLE PRECISION,
			altitude_m  DOUBLE PRECISION,
			bearing_deg DOUBLE PRECISION,
			taken_at    TIMESTAMPTZ,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE (ha_id, kind)
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("ha schema: %w", err)
		}
	}
	return nil
}

type haRow struct {
	ID              int64           `db:"id"`
	TaskID          sql.NullInt64   `db:"task_id"`
	ProjectID       sql.NullInt64   `db:"project_id"`
	OrgID           sql.NullInt64   `db:"org_id"`
	Auftragnehmer   string          `db:"auftragnehmer"`
	Address         string          `db:"address"`
	NVT             string          `db:"nvt"`
	City            string          `db:"city"`
	Bauabschnitt    string          `db:"bauabschnitt"`
	ClientLastName  string          `db:"client_last_name"`
	ClientFirstName string          `db:"client_first_name"`
	ClientPhone     string          `db:"client_phone"`
	ClientMobile    string          `db:"client_mobile"`
	APLCodes        string          `db:"apl_codes"`
	Units           sql.NullInt64   `db:"units"`
	ProtocolDate    string          `db:"protocol_date"`
	Monteur         string          `db:"monteur"`
	Pruefer         string          `db:"pruefer"`
	ProjectNumber   string          `db:"project_number"`
	GVS             string          `db:"gvs"`
	Rohr            sql.NullInt64   `db:"rohr"`
	RohrColor       string          `db:"rohr_color"`
	RoehrchenColor  string          `db:"roehrchen_color"`
	KassetteB       string          `db:"kassette_b"`
	MuxNumber       string          `db:"mux_number"`
	MuxFibers       string          `db:"mux_fibers"`
	Splices         sql.NullInt64   `db:"splices"`
	FiberColors     string          `db:"fiber_colors"`
	CableCharge     string          `db:"cable_charge"`
	CableLength     sql.NullFloat64 `db:"cable_length_m"`
	CheckFiber      bool            `db:"check_fiber"`
	CheckComplete   bool            `db:"check_complete"`
	CheckClean      bool            `db:"check_clean"`
	CreatedBy       sql.NullInt64   `db:"created_by"`
	CreatedAt       string          `db:"created_at"`
	CreatorEmail    sql.NullString  `db:"creator_email"`
	PhotoCount      int             `db:"photos"`
}

const haSelect = `
	SELECT h.id, h.task_id, h.project_id, h.org_id,
	       h.auftragnehmer, h.address, h.city, h.bauabschnitt,
	       h.client_last_name, h.client_first_name, h.client_phone, h.client_mobile,
	       h.apl_codes, h.nvt, h.units,
	       to_char(h.protocol_date, 'YYYY-MM-DD') AS protocol_date,
	       h.monteur, h.pruefer, h.project_number, h.gvs,
	       h.rohr, h.rohr_color, h.roehrchen_color, h.kassette_b,
	       h.mux_number, h.mux_fibers, h.splices, h.fiber_colors,
	       h.cable_charge, h.cable_length_m,
	       h.check_fiber, h.check_complete, h.check_clean,
	       h.created_by, to_char(h.created_at, 'YYYY-MM-DD HH24:MI') AS created_at,
	       u.email AS creator_email,
	       (SELECT count(*) FROM ha_photos p WHERE p.ha_id = h.id) AS photos
	FROM ha_protocols h
	LEFT JOIN users u ON u.id = h.created_by`

type haPhotoRow struct {
	ID          int64           `db:"id"`
	HAID        int64           `db:"ha_id"`
	Kind        string          `db:"kind"`
	ObjectKey   string          `db:"object_key"`
	ContentType string          `db:"content_type"`
	Bytes       int             `db:"bytes"`
	Lat         sql.NullFloat64 `db:"lat"`
	Lon         sql.NullFloat64 `db:"lon"`
	Altitude    sql.NullFloat64 `db:"altitude_m"`
	Bearing     sql.NullFloat64 `db:"bearing_deg"`
	TakenAt     sql.NullString  `db:"taken_at"`
	GeoAddress  string          `db:"geo_address"`
	OCRTime     string          `db:"ocr_time"`
}

func loadHA(id int64) (haRow, error) {
	var h haRow
	err := db.Get(&h, haSelect+" WHERE h.id = $1", id)
	return h, err
}

func listHAPhotos(haID int64) ([]haPhotoRow, error) {
	var out []haPhotoRow
	err := db.Select(&out, `SELECT id, ha_id, kind, object_key, content_type, bytes,
			lat, lon, altitude_m, bearing_deg, geo_address, ocr_time,
			to_char(taken_at, 'YYYY-MM-DD HH24:MI') AS taken_at
		FROM ha_photos WHERE ha_id = $1`, haID)
	return out, err
}

// canSeeHA: staff sve; partner samo HaP svoje organizacije ili svoj.
func canSeeHA(r *http.Request, h haRow) bool {
	claims := GetAuthClaims(r)
	if claims == nil {
		return false
	}
	if seesAllProtocols(claims.Role) {
		return true
	}
	if h.CreatedBy.Valid && h.CreatedBy.Int64 == claims.UserID {
		return true
	}
	orgID := userOrgID(claims.UserID)
	return orgID != 0 && h.OrgID.Valid && h.OrgID.Int64 == orgID
}

func canEditHA(r *http.Request, h haRow) bool {
	return canSeeHA(r, h) // staff + ista organizacija; dalje ograničeno rolom
}

// HAListHandler prikazuje listu HaP-ova.
func HAListHandler(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r)
	if claims == nil {
		http.Redirect(w, r, "/login?next=/ha", http.StatusFound)
		return
	}
	where := " WHERE 1=1"
	args := []interface{}{}
	if !seesAllProtocols(claims.Role) {
		where += " AND (h.created_by = $1"
		args = append(args, claims.UserID)
		if orgID := userOrgID(claims.UserID); orgID != 0 {
			where += " OR h.org_id = $2"
			args = append(args, orgID)
		}
		where += ")"
	}
	var rows []haRow
	if err := db.Select(&rows, haSelect+where+` ORDER BY h.id DESC LIMIT 200`, args...); err != nil {
		http.Error(w, "Query failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl, err := template.New("ha-list.html").Funcs(template.FuncMap(csrfTemplateFuncs(r))).ParseFiles("web/templates/ha-list.html")
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = tmpl.Execute(w, map[string]any{
		"Rows":       rows,
		"IsAdmin":    isAdminRole(r),
		"IsOrgAdmin": isOrgAdminRequest(r),
		"UserRole":   userRole(r),
		"Message":    r.URL.Query().Get("msg"),
	})
}

// HANewHandler: GET forma, POST kreiranje.
func HANewHandler(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r)
	if claims == nil {
		http.Redirect(w, r, "/login?next=/ha/new", http.StatusFound)
		return
	}
	if r.Method == http.MethodPost {
		id, msg := createHA(r, claims)
		if id == 0 {
			pre := haPrefillFromTask(r, 0)
			if v := strings.TrimSpace(r.FormValue("address")); v != "" {
				pre.Address = v
			}
			renderHAForm(w, r, claims, pre, false, msg)
			return
		}
		http.Redirect(w, r, "/ha/view?id="+strconv.FormatInt(id, 10)+"&msg="+url.QueryEscape(msg), http.StatusFound)
		return
	}
	renderHAForm(w, r, claims, haPrefillFromTask(r, 0), false, "")
}

// haPrefillFromTask priprema HaP formu kada se pravi iz zadatka
// (/ha/new?task_id=N): povezuje zadatak, projekat i organizaciju i preuzima
// adresu, da se isti podaci ne prepisuju ručno.
func haPrefillFromTask(r *http.Request, taskID int64) haRow {
	var h haRow
	if taskID <= 0 {
		taskID, _ = strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("task_id")), 10, 64)
	}
	if taskID <= 0 && r.Method == http.MethodPost {
		taskID, _ = strconv.ParseInt(strings.TrimSpace(r.FormValue("task_id")), 10, 64)
	}
	if taskID <= 0 {
		return h
	}
	t, err := loadTask(taskID)
	if err != nil || !canSeeTask(r, t) {
		return h
	}
	h.TaskID = sql.NullInt64{Int64: t.ID, Valid: true}
	if t.ProjectID > 0 {
		h.ProjectID = sql.NullInt64{Int64: t.ProjectID, Valid: true}
	}
	h.OrgID = t.OrgID
	h.Address = t.Address
	h.NVT = t.NVT
	h.ProjectNumber = t.Project

	// Predikcija: ako direktorijum klijenata već ima ovu adresu (ili
	// jedinstven NVT), popuni klijenta i telefone odmah, bez čekanja na JS.
	if c, ok := customerForPrefill(t.NVT, t.Address); ok {
		first, last := splitKunde(c.Kunde)
		h.ClientFirstName = first
		h.ClientLastName = last
		h.ClientPhone = c.Phone
		h.ClientMobile = c.Mobile
		if h.NVT == "" {
			h.NVT = c.NVT
		}
	}
	return h
}

// syncTaskFromHA drži zadatak u "in progress" dok god postoji HaP koji nije
// označen kao potpuno završen; kada su svi HaP-ovi zadatka kompletni, zadatak
// prelazi u "done". Ako se oznaka skine, zadatak se vraća u "in progress".
func syncTaskFromHA(taskID int64) {
	if taskID <= 0 {
		return
	}
	var st struct {
		Total      int `db:"total"`
		Incomplete int `db:"incomplete"`
	}
	if err := db.Get(&st, `SELECT count(*) AS total,
			count(*) FILTER (WHERE NOT check_complete) AS incomplete
		FROM ha_protocols WHERE task_id = $1`, taskID); err != nil {
		return
	}
	if st.Total == 0 {
		return
	}
	if st.Incomplete == 0 {
		_, _ = db.Exec(`UPDATE tasks SET status = 'done', completed_at = NOW(), updated_at = NOW()
			WHERE id = $1 AND status <> 'cancelled'`, taskID)
		return
	}
	_, _ = db.Exec(`UPDATE tasks SET status = 'in_progress', completed_at = NULL, updated_at = NOW()
		WHERE id = $1 AND status IN ('open','in_progress','done')`, taskID)
}

func renderHAForm(w http.ResponseWriter, r *http.Request, claims *AuthClaims, h haRow, isEdit bool, message string) {
	type projectOption struct {
		ID     int64  `db:"id"`
		Number string `db:"number"`
	}
	var projects []projectOption
	_ = db.Select(&projects, `SELECT id, number FROM projects WHERE status = 'active' ORDER BY number`)

	orgs, _ := listOrgs()

	defFirma := h.Auftragnehmer
	if defFirma == "" {
		defFirma = orgNameByID(userOrgID(claims.UserID))
	}
	defMonteur := h.Monteur
	if defMonteur == "" {
		defMonteur = claims.Email
	}
	defDate := h.ProtocolDate
	if defDate == "" {
		defDate = time.Now().Format("2006-01-02")
	}

	var onts []ontRow
	if h.ID > 0 {
		onts, _ = listONTs(h.ID)
	}

	tmpl, err := template.New("ha-form.html").Funcs(template.FuncMap(csrfTemplateFuncs(r))).ParseFiles("web/templates/ha-form.html")
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tmpl.Execute(w, map[string]any{
		"Message":         message,
		"HA":              h,
		"IsEdit":          isEdit,
		"PhotoKinds":      haSlotPhotoKinds,
		"ONTs":            onts,
		"FiberColors":     haFiberColors,
		"RohrColors":      haRohrColors,
		"RoehrchenColors": haRoehrchenColors,
		"Selected":        splitColors(h.FiberColors),
		"Projects":        projects,
		"Orgs":            orgs,
		"DefaultDate":     defDate,
		"DefaultFirma":    defFirma,
		"DefaultMonteur":  defMonteur,
		"IsAdmin":         isAdminRole(r),
		"IsOrgAdmin":      isOrgAdminRequest(r),
		"IsStaff":         claims != nil && seesAllProtocols(claims.Role),
		"UserRole":        userRole(r),
	})
}

// HAEditHandler: GET forma sa postojećim podacima, POST izmena.
func HAEditHandler(w http.ResponseWriter, r *http.Request) {
	claims := GetAuthClaims(r)
	if claims == nil {
		http.Redirect(w, r, "/login?next=/ha", http.StatusFound)
		return
	}
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if id <= 0 && r.Method == http.MethodPost {
		id, _ = strconv.ParseInt(r.FormValue("id"), 10, 64)
	}
	if id <= 0 {
		http.Error(w, "Missing id", http.StatusBadRequest)
		return
	}
	h, err := loadHA(id)
	if err != nil {
		log.Printf("ha edit load %d: %v", id, err)
		http.Error(w, "HaP not found", http.StatusNotFound)
		return
	}
	if !canEditHA(r, h) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodPost {
		msg := updateHA(r, h)
		http.Redirect(w, r, "/ha/view?id="+strconv.FormatInt(h.ID, 10)+"&msg="+url.QueryEscape(msg), http.StatusFound)
		return
	}
	renderHAForm(w, r, claims, h, true, "")
}

func createHA(r *http.Request, claims *AuthClaims) (int64, string) {
	// 8 MB ostaje u memoriji; veći fajlovi idu u privremene fajlove. Ranije
	// je 64 MB držano u RAM-u, što je uz dekodiranje slika dovodilo do OOM.
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		return 0, "Upload failed: " + err.Error()
	}
	defer removeMultipartFiles(r)
	address := strings.TrimSpace(r.FormValue("address"))
	if address == "" {
		return 0, "Address is required."
	}
	date := strings.TrimSpace(r.FormValue("protocol_date"))
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return 0, "Invalid date."
	}

	var taskID, projectID, taskOrgID int64
	if v, err := strconv.ParseInt(r.FormValue("task_id"), 10, 64); err == nil && v > 0 {
		taskID = v
	}
	if taskID > 0 {
		t, err := loadTask(taskID)
		if err != nil {
			return 0, "Task not found."
		}
		if !canSeeTask(r, t) {
			return 0, "You are not allowed to link that task."
		}
		if t.OrgID.Valid {
			taskOrgID = t.OrgID.Int64
		}
	}
	if v, err := strconv.ParseInt(r.FormValue("project_id"), 10, 64); err == nil && v > 0 {
		projectID = v
	}
	orgID := userOrgID(claims.UserID)
	if seesAllProtocols(claims.Role) {
		if v, err := strconv.ParseInt(r.FormValue("org_id"), 10, 64); err == nil && v > 0 {
			orgID = v
		}
	}
	if orgID == 0 && taskOrgID > 0 {
		orgID = taskOrgID
	}

	var id int64
	err := db.Get(&id, `INSERT INTO ha_protocols
			(task_id, project_id, org_id, auftragnehmer, address, city, bauabschnitt,
			 client_last_name, client_first_name, units, protocol_date, monteur, pruefer,
			 project_number, gvs, kassette_b, mux_number, mux_fibers, splices,
			 fiber_colors, cable_charge, cable_length_m,
			 nvt, client_phone, client_mobile,
			 check_fiber, check_complete, check_clean, created_by,
			 rohr, rohr_color, roehrchen_color)
		VALUES ($1, $2, $3, $4,$5,$6,$7,$8,$9,
		        NULLIF($10,'')::int, $11::date, $12,$13,$14,$15,$16,$17,$18,
		        NULLIF($19,'')::int, $20,$21,NULLIF($22,'')::numeric,
		        $23,$24,$25, $26,$27,$28,$29, $30,$31,$32)
		RETURNING id`,
		nullableID(taskID), nullableID(projectID), nullableID(orgID),
		strings.TrimSpace(r.FormValue("auftragnehmer")),
		address,
		strings.TrimSpace(r.FormValue("city")),
		strings.TrimSpace(r.FormValue("bauabschnitt")),
		strings.TrimSpace(r.FormValue("client_last_name")),
		strings.TrimSpace(r.FormValue("client_first_name")),
		strings.TrimSpace(r.FormValue("units")),
		date,
		strings.TrimSpace(r.FormValue("monteur")),
		strings.TrimSpace(r.FormValue("pruefer")),
		strings.TrimSpace(r.FormValue("project_number")),
		strings.TrimSpace(r.FormValue("gvs")),
		strings.TrimSpace(r.FormValue("kassette_b")),
		strings.TrimSpace(r.FormValue("mux_number")),
		strings.TrimSpace(r.FormValue("mux_fibers")),
		strings.TrimSpace(r.FormValue("splices")),
		strings.Join(r.Form["fiber_colors"], ","),
		strings.TrimSpace(r.FormValue("cable_charge")),
		strings.TrimSpace(r.FormValue("cable_length_m")),
		strings.TrimSpace(r.FormValue("nvt")),
		strings.TrimSpace(r.FormValue("client_phone")),
		strings.TrimSpace(r.FormValue("client_mobile")),
		r.FormValue("check_fiber") != "",
		r.FormValue("check_complete") != "",
		r.FormValue("check_clean") != "",
		claims.UserID,
		parseRohr(r.FormValue("rohr")),
		strings.TrimSpace(r.FormValue("rohr_color")),
		strings.TrimSpace(r.FormValue("roehrchen_color")))
	if err != nil {
		return 0, "Could not save: " + err.Error()
	}
	// Zadatak iz kog je HaP nastao ostaje "in progress" dok HaP nije kompletan.
	syncTaskFromHA(taskID)

	saved, failed, bild3 := saveHAPhotos(r, id)
	// Adresa iz GPS-a (OpenStreetMap) — samo prvi put, sa kratkim timeoutom.
	geoctx, geocancel := context.WithTimeout(r.Context(), 10*time.Second)
	applyPhotoGeoAddresses(geoctx, id)
	geocancel()
	go func(haID int64) {
		octx, ocancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer ocancel()
		runPhotoOCR(octx, haID)
	}(id)
	apl := resolveAPLCodes(r, bild3, id)
	if _, err := db.Exec(`UPDATE ha_protocols SET apl_codes=$1 WHERE id=$2`,
		encodeAPLCodesJSON(apl), id); err != nil {
		log.Printf("apl codes %d: %v", id, err)
	}
	ontSaved, ontFailed := saveONTs(r, id)
	logUserEvent(claims.UserID, 0, "hap-create", fmt.Sprintf("hap %d", id))
	msg := "HaP created."
	if saved > 0 {
		msg = fmt.Sprintf("HaP created with %d photo(s).", saved)
	}
	if failed > 0 {
		msg += fmt.Sprintf(" %d photo(s) could not be stored.", failed)
	}
	if ontSaved > 0 {
		msg += fmt.Sprintf(" %d ONT(s) saved.", ontSaved)
	}
	if ontFailed > 0 {
		msg += fmt.Sprintf(" %d ONT(s) failed.", ontFailed)
	}
	return id, msg
}

// dash vraća „—“ za prazan string (za pregled).
func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// parseRohr prihvata samo 1–4; sve ostalo je NULL.
func parseRohr(v string) any {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 1 || n > 4 {
		return nil
	}
	return n
}

// photoCaptionLine vraća natpis ispod fotografije u HTML pregledu: adresa
// dobijena iz GPS-a te slike + vreme snimanja. Ako slika nema ni jedno, natpis
// se ne prikazuje (adresa HaP-a se ne koristi — slike mogu biti sa drugih
// lokacija).
func photoCaptionLine(p haPhotoRow) string {
	var parts []string
	if s := strings.TrimSpace(p.GeoAddress); s != "" {
		parts = append(parts, s)
	}
	if t := strings.TrimSpace(p.OCRTime); t != "" {
		parts = append(parts, t)
	} else if p.TakenAt.Valid && strings.TrimSpace(p.TakenAt.String) != "" {
		parts = append(parts, dateShort(p.TakenAt.String))
	}
	return strings.Join(parts, " · ")
}

// removeMultipartFiles briše privremene fajlove koje je Go napravio za
// multipart delove veće od memorijskog praga.
func removeMultipartFiles(r *http.Request) {
	if r.MultipartForm != nil {
		_ = r.MultipartForm.RemoveAll()
	}
}

// nullableID vraća nil za 0 (SQL NULL) ili int64 — sprečava cast "" u int.
func nullableID(v int64) any {
	if v <= 0 {
		return nil
	}
	return v
}

// saveHAPhotos čuva sve priloge iz multipart forme.
func saveHAPhotos(r *http.Request, haID int64) (saved, failed int, bild3 []byte) {
	for _, k := range haSlotPhotoKinds {
		file, header, err := r.FormFile("photo_" + k.Key)
		if err != nil || file == nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(file, 25<<20))
		_ = file.Close()
		if err != nil || len(data) == 0 {
			failed++
			continue
		}
		if k.Key == "bild3" {
			bild3 = data
		}
		info := parseExif(data)
		ext := strings.ToLower(path.Ext(header.Filename))
		if ext != ".jpg" && ext != ".jpeg" && ext != ".png" {
			ext = ".jpg"
		}
		key := haPhotoKey(haID, k.Key, ext)
		if err := sourceStore.PutRaw(r.Context(), key, data, contentTypeOf(ext)); err != nil {
			log.Printf("ha photo upload %s: %v", k.Key, err)
			failed++
			continue
		}
		// EXIF (koordinate iz kamere) ima prednost; ako ga nema, koristi se
		// geolokacija koju je pretraživač zabeležio pri uploadu.
		var lat, lon any
		if info.HasLocation {
			lat, lon = info.Lat, info.Lon
		} else if la, lo, ok := parseLatLonText(r.FormValue("geo_" + k.Key)); ok {
			lat, lon = la, lo
		}
		var alt, brg, taken any
		if info.HasLocation && info.Altitude != 0 {
			alt = info.Altitude
		}
		if info.HasLocation && info.Bearing != 0 {
			brg = info.Bearing
		}
		if info.HasTimestamp {
			taken = info.TakenAt
		}
		if _, err := db.Exec(`INSERT INTO ha_photos
				(ha_id, kind, object_key, content_type, bytes, lat, lon, altitude_m, bearing_deg, taken_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (ha_id, kind) DO UPDATE SET
				object_key = EXCLUDED.object_key, content_type = EXCLUDED.content_type,
				bytes = EXCLUDED.bytes, lat = EXCLUDED.lat, lon = EXCLUDED.lon,
				altitude_m = EXCLUDED.altitude_m, bearing_deg = EXCLUDED.bearing_deg,
				taken_at = EXCLUDED.taken_at`,
			haID, k.Key, key, contentTypeOf(ext), len(data), lat, lon, alt, brg, taken); err != nil {
			log.Printf("ha photo db %s: %v", k.Key, err)
			failed++
			continue
		}
		saved++
	}
	return saved, failed, bild3
}

func contentTypeOf(ext string) string {
	switch ext {
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	default:
		return "image/jpeg"
	}
}

// DeleteRaw briše objekat po ključu (koristi se pri brisanju HaP-a).
func (s *SourceStore) DeleteRaw(ctx context.Context, key string) error {
	if !s.Enabled() || key == "" {
		return nil
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	return err
}

// HADeleteHandler briše HaP i njegove priloge (staff ili vlasnik/organizacija).
func HADeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	h, err := loadHA(id)
	if err != nil {
		http.Error(w, "HaP not found", http.StatusNotFound)
		return
	}
	claims := GetAuthClaims(r)
	if claims == nil || !canEditHA(r, h) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	photos, _ := listHAPhotos(h.ID)
	for _, p := range photos {
		if err := sourceStore.DeleteRaw(r.Context(), p.ObjectKey); err != nil {
			log.Printf("ha delete object %s: %v", p.ObjectKey, err)
		}
	}
	onts, _ := listONTs(h.ID)
	for _, o := range onts {
		if o.ObjectKey == "" {
			continue
		}
		if err := sourceStore.DeleteRaw(r.Context(), o.ObjectKey); err != nil {
			log.Printf("ha delete ont object %s: %v", o.ObjectKey, err)
		}
	}
	if _, err := db.Exec(`DELETE FROM ha_protocols WHERE id = $1`, h.ID); err != nil {
		http.Error(w, "Delete failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	logUserEvent(claims.UserID, 0, "hap-delete", fmt.Sprintf("hap %d", h.ID))
	http.Redirect(w, r, "/ha?msg="+url.QueryEscape("HaP deleted."), http.StatusFound)
}

// HAViewHandler prikazuje HaP i dozvoljava izmenu tekstualnih polja.
func HAViewHandler(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if id <= 0 && r.Method == http.MethodPost {
		id, _ = strconv.ParseInt(r.FormValue("id"), 10, 64)
	}
	if id <= 0 {
		http.Error(w, "Missing id", http.StatusBadRequest)
		return
	}
	h, err := loadHA(id)
	if err != nil {
		http.Error(w, "HaP not found", http.StatusNotFound)
		return
	}
	claims := GetAuthClaims(r)
	if claims == nil || !canSeeHA(r, h) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	photos, _ := listHAPhotos(h.ID)
	byKind := map[string]haPhotoRow{}
	for _, p := range photos {
		byKind[p.Kind] = p
	}
	onts, _ := listONTs(h.ID)
	tmpl, err := template.New("ha-view.html").
		Funcs(template.FuncMap(csrfTemplateFuncs(r))).
		Funcs(template.FuncMap{
			"dateShort":        dateShort,
			"photoCaptionLine": photoCaptionLine,
			"formatHaDate":     formatHaDate,
			"swatchStyle":      haSwatchStyle,
			"dash":             dash,
		}).
		ParseFiles("web/templates/ha-view.html")
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	orgName := ""
	if h.OrgID.Valid {
		orgName = orgNameByID(h.OrgID.Int64)
	}
	var rohrOpt, roehrOpt *haColorOption
	if h.RohrColor != "" {
		rohrOpt = findHaColor(haRohrColors, h.RohrColor)
	}
	if h.RoehrchenColor != "" {
		roehrOpt = findHaColor(haRoehrchenColors, h.RoehrchenColor)
	}
	uploaded := 0
	for _, k := range haSlotPhotoKinds {
		if _, ok := byKind[k.Key]; ok {
			uploaded++
		}
	}
	_ = tmpl.Execute(w, map[string]any{
		"HA":                h,
		"PhotoUploaded":     uploaded,
		"OrgName":           orgName,
		"RohrColorOpt":      rohrOpt,
		"RoehrchenColorOpt": roehrOpt,
		"SelectedColors":    selectedHAFibers(h.FiberColors),
		"Photos":            photos,
		"PhotoByKind":       byKind,
		"PhotoKinds":        haSlotPhotoKinds,
		"ONTs":              onts,
		"APLCodes":          parseAPLCodesJSON(h.APLCodes),
		"FiberColors":       haFiberColors,
		"Selected":          strings.Split(h.FiberColors, ","),
		"CanEdit":           canEditHA(r, h),
		"IsAdmin":           isAdminRole(r),
		"IsOrgAdmin":        isOrgAdminRequest(r),
		"UserRole":          userRole(r),
		"Message":           r.URL.Query().Get("msg"),
	})
}

func updateHA(r *http.Request, h haRow) string {
	// 8 MB ostaje u memoriji; veći fajlovi idu u privremene fajlove.
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		return "Upload failed: " + err.Error()
	}
	defer removeMultipartFiles(r)
	date := strings.TrimSpace(r.FormValue("protocol_date"))
	if date == "" {
		date = h.ProtocolDate
	}
	_, err := db.Exec(`UPDATE ha_protocols SET
			auftragnehmer=$1, address=$2, city=$3, bauabschnitt=$4,
			client_last_name=$5, client_first_name=$6, units=NULLIF($7,'')::int,
			protocol_date=$8::date, monteur=$9, pruefer=$10, project_number=$11,
			gvs=$12, kassette_b=$13, mux_number=$14, mux_fibers=$15,
			splices=NULLIF($16,'')::int, fiber_colors=$17, cable_charge=$18,
			cable_length_m=NULLIF($19,'')::numeric,
			nvt=$20, client_phone=$21, client_mobile=$22,
			check_fiber=$23, check_complete=$24, check_clean=$25,
			rohr=$27, rohr_color=$28, roehrchen_color=$29,
			updated_at=NOW()
		WHERE id=$26`,
		strings.TrimSpace(r.FormValue("auftragnehmer")),
		strings.TrimSpace(r.FormValue("address")),
		strings.TrimSpace(r.FormValue("city")),
		strings.TrimSpace(r.FormValue("bauabschnitt")),
		strings.TrimSpace(r.FormValue("client_last_name")),
		strings.TrimSpace(r.FormValue("client_first_name")),
		strings.TrimSpace(r.FormValue("units")),
		date,
		strings.TrimSpace(r.FormValue("monteur")),
		strings.TrimSpace(r.FormValue("pruefer")),
		strings.TrimSpace(r.FormValue("project_number")),
		strings.TrimSpace(r.FormValue("gvs")),
		strings.TrimSpace(r.FormValue("kassette_b")),
		strings.TrimSpace(r.FormValue("mux_number")),
		strings.TrimSpace(r.FormValue("mux_fibers")),
		strings.TrimSpace(r.FormValue("splices")),
		strings.Join(r.Form["fiber_colors"], ","),
		strings.TrimSpace(r.FormValue("cable_charge")),
		strings.TrimSpace(r.FormValue("cable_length_m")),
		strings.TrimSpace(r.FormValue("nvt")),
		strings.TrimSpace(r.FormValue("client_phone")),
		strings.TrimSpace(r.FormValue("client_mobile")),
		r.FormValue("check_fiber") != "",
		r.FormValue("check_complete") != "",
		r.FormValue("check_clean") != "",
		h.ID,
		parseRohr(r.FormValue("rohr")),
		strings.TrimSpace(r.FormValue("rohr_color")),
		strings.TrimSpace(r.FormValue("roehrchen_color")))
	if err != nil {
		return "Could not save: " + err.Error()
	}
	saved, failed, bild3 := saveHAPhotos(r, h.ID)
	geoctx, geocancel := context.WithTimeout(r.Context(), 10*time.Second)
	applyPhotoGeoAddresses(geoctx, h.ID)
	geocancel()
	go func(haID int64) {
		octx, ocancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer ocancel()
		runPhotoOCR(octx, haID)
	}(h.ID)
	apl := resolveAPLCodes(r, bild3, h.ID)
	if _, err := db.Exec(`UPDATE ha_protocols SET apl_codes=$1 WHERE id=$2`,
		encodeAPLCodesJSON(apl), h.ID); err != nil {
		log.Printf("apl codes %d: %v", h.ID, err)
	}
	ontSaved, ontFailed := saveONTs(r, h.ID)
	syncTaskFromHA(h.TaskID.Int64)
	if saved == 0 && failed == 0 && ontSaved == 0 && ontFailed == 0 {
		return "HaP saved."
	}
	return fmt.Sprintf("HaP saved. Photos: %d (failed %d). ONTs: %d (failed %d).",
		saved, failed, ontSaved, ontFailed)
}

// HAPhotoHandler servira sliku iz Object Storage-a (uz proveru vidljivosti).
func HAPhotoHandler(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	var p haPhotoRow
	if err := db.Get(&p, `SELECT id, ha_id, kind, object_key, content_type, bytes,
			lat, lon, altitude_m, bearing_deg, NULL::text AS taken_at
		FROM ha_photos WHERE id = $1`, id); err != nil {
		http.Error(w, "Photo not found", http.StatusNotFound)
		return
	}
	h, err := loadHA(p.HAID)
	if err != nil || !canSeeHA(r, h) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	// Objektni ključ se menja pri svakom uploadu, pa je ETag dovoljan da
	// pregledač odmah vidi novu sliku (ranije je max-age=300 držao staru).
	etag := `"` + p.ObjectKey + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	data, ct, err := sourceStore.GetRaw(r.Context(), p.ObjectKey)
	if err != nil {
		http.Error(w, "Photo unavailable", http.StatusBadGateway)
		return
	}
	if ct == "" {
		ct = p.ContentType
	}
	w.Header().Set("Content-Type", ct)
	_, _ = w.Write(data)
}

// HAPDFHandler generiše PDF na zahtev (nema potrebe da se čuva kopija).
func HAPDFHandler(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if id <= 0 {
		http.Error(w, "Missing id", http.StatusBadRequest)
		return
	}
	h, err := loadHA(id)
	if err != nil {
		http.Error(w, "HaP not found", http.StatusNotFound)
		return
	}
	if !canSeeHA(r, h) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	photos, _ := listHAPhotos(h.ID)
	images := map[string][]byte{}
	for _, p := range photos {
		if data, _, err := sourceStore.GetRaw(r.Context(), p.ObjectKey); err == nil {
			images[p.Kind] = data
		}
	}
	onts, _ := listONTs(h.ID)
	ontImages := map[int64][]byte{}
	for _, o := range onts {
		if o.ObjectKey == "" {
			continue
		}
		if data, _, err := sourceStore.GetRaw(r.Context(), o.ObjectKey); err == nil {
			ontImages[o.ID] = data
		}
	}
	pdf, err := buildHAPDF(h, photos, images, onts, ontImages)
	if err != nil {
		http.Error(w, "PDF error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	name := sanitizeHAFilename(fmt.Sprintf("HaP_%s_%s.pdf", h.Address, h.ProtocolDate))
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	_, _ = w.Write(pdf)
}

// dateShort prikazuje "2026-07-21 12:13" kao "21.07.2026 12:13".
func dateShort(v string) string {
	if len(v) >= 16 && v[4] == '-' {
		return v[8:10] + "." + v[5:7] + "." + v[0:4] + " " + v[11:16]
	}
	return v
}

func sanitizeHAFilename(s string) string {
	repl := strings.NewReplacer(" ", "_", "/", "-", "\\", "-", "\"", "", "'", "", ",", "", ";", "")
	out := repl.Replace(s)
	if len(out) > 120 {
		out = out[:120]
	}
	return out
}

// unusedMultipartHead sprečava "declared and not used" ako se potpis promeni.
var _ = multipart.ErrMessageTooLarge
