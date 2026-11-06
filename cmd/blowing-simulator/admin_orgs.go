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
)

// OrgStat je red na /admin/orgs: organizacija + broj protokola po koloni.
type OrgStat struct {
	ID         int            `db:"id"`
	Name       string         `db:"name"`
	Kind       string         `db:"kind"`
	Owned      int            `db:"owned"`
	Credited   int            `db:"credited"`
	Company    int            `db:"company"`
	InviteCode sql.NullString `db:"invite_code"`
	Aliases    []string       `db:"-"`
}

// orgFieldColumn mapira ime polja iz forme na kolonu (whitelist — sprečava SQL
// injection kroz ime kolone).
var orgFieldColumn = map[string]string{
	"owner":    "owner_org_id",
	"provider": "service_provider_org_id",
	"company":  "company_org_id",
}

// AdminOrgsHandler je admin stranica za organizacije: kreiranje, aliasi i
// bulk dodela organizacije protokolima (P4 u OWNERSHIP.md).
//
//	GET  /admin/orgs
//	POST /admin/orgs  action=create|add-alias|set-kind|bulk-assign
func AdminOrgsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		msg := handleOrgAction(r)
		http.Redirect(w, r, "/admin/orgs?msg="+url.QueryEscape(msg), http.StatusFound)
		return
	}

	// Organizacija može biti napravljena i van ove forme (SQL, druga grana);
	// kod se dodeljuje i ovde, da nikad ne ostane bez njega.
	if err := ensureInviteCodes(db); err != nil {
		log.Printf("orgs: invite codes: %v", err)
	}

	orgs, err := orgStats()
	if err != nil {
		http.Error(w, "Query failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var missingOwner, missingProvider, missingCompany int
	_ = db.QueryRow(`SELECT
		count(*) FILTER (WHERE owner_org_id IS NULL),
		count(*) FILTER (WHERE service_provider_org_id IS NULL),
		count(*) FILTER (WHERE company_org_id IS NULL)
		FROM protocols`).Scan(&missingOwner, &missingProvider, &missingCompany)

	tmpl, err := template.ParseFiles("web/templates/admin-orgs.html")
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = tmpl.Execute(w, map[string]any{
		"IsAdmin":         true,
		"IsOrgAdmin":      isOrgAdminRequest(r),
		"UserRole":        userRole(r),
		"Orgs":            orgs,
		"MissingOwner":    missingOwner,
		"MissingProvider": missingProvider,
		"MissingCompany":  missingCompany,
		"Message":         r.URL.Query().Get("msg"),
	})
}

// handleOrgAction izvršava POST i vraća poruku za prikaz.
func handleOrgAction(r *http.Request) string {
	switch r.FormValue("action") {
	case "create":
		name := strings.TrimSpace(r.FormValue("name"))
		kind := r.FormValue("kind")
		if name == "" {
			return "Naziv je obavezan."
		}
		if kind != OrgKindInternal && kind != OrgKindSubcontractor && kind != OrgKindClient {
			return "Nepoznat tip organizacije."
		}
		var id int
		if err := db.Get(&id, `INSERT INTO orgs (name, kind) VALUES ($1,$2)
			ON CONFLICT (name) DO UPDATE SET kind = EXCLUDED.kind RETURNING id`, name, kind); err != nil {
			return "Greška: " + err.Error()
		}
		// Naziv je i alias (normalizovan), da se sledeći PDF prepozna odmah.
		if key := normalizeOrgName(name); key != "" {
			_, _ = db.Exec(`INSERT INTO org_aliases (alias_normalized, org_id) VALUES ($1,$2)
				ON CONFLICT (alias_normalized) DO UPDATE SET org_id = EXCLUDED.org_id`, key, id)
		}
		// Registracioni kod odmah, da nova organizacija ne čeka restart.
		code, err := regenerateInviteCode(int64(id))
		if err != nil {
			return "Organizacija sačuvana: " + name + " (kod nije dodeljen: " + err.Error() + ")"
		}
		return "Organizacija sačuvana: " + name + " — registracioni kod " + code

	case "add-alias":
		orgID, _ := strconv.ParseInt(r.FormValue("org_id"), 10, 64)
		alias := strings.TrimSpace(r.FormValue("alias"))
		key := normalizeOrgName(alias)
		if orgID <= 0 || key == "" {
			return "Izaberi organizaciju i unesi naziv."
		}
		if _, err := db.Exec(`INSERT INTO org_aliases (alias_normalized, org_id) VALUES ($1,$2)
			ON CONFLICT (alias_normalized) DO UPDATE SET org_id = EXCLUDED.org_id`, key, orgID); err != nil {
			return "Greška: " + err.Error()
		}
		return "Alias dodat: " + key

	case "set-kind":
		orgID, _ := strconv.ParseInt(r.FormValue("org_id"), 10, 64)
		kind := r.FormValue("kind")
		if orgID <= 0 || (kind != OrgKindInternal && kind != OrgKindSubcontractor && kind != OrgKindClient) {
			return "Neispravan unos."
		}
		if _, err := db.Exec(`UPDATE orgs SET kind = $1 WHERE id = $2`, kind, orgID); err != nil {
			return "Greška: " + err.Error()
		}
		return "Tip organizacije promenjen."

	case "regenerate-code":
		orgID, _ := strconv.ParseInt(r.FormValue("org_id"), 10, 64)
		code, err := regenerateInviteCode(orgID)
		if err != nil {
			return "Greška: " + err.Error()
		}
		return "Novi registracioni kod: " + code

	case "bulk-assign":
		return bulkAssignOrg(r)
	}
	return "Nepoznata akcija."
}

// bulkAssignOrg dodeljuje organizaciju svim protokolima koji zadovoljavaju
// filter (tip, period, „samo prazni"). Vraća broj izmenjenih redova.
func bulkAssignOrg(r *http.Request) string {
	column, ok := orgFieldColumn[r.FormValue("field")]
	if !ok {
		return "Izaberi kolonu."
	}
	orgID, _ := strconv.ParseInt(r.FormValue("org_id"), 10, 64)
	if orgID <= 0 {
		return "Izaberi organizaciju."
	}

	args := []interface{}{orgID}
	where := " WHERE true"
	add := func(cond string, v interface{}) {
		args = append(args, v)
		where += " AND " + fmt.Sprintf(cond, len(args))
	}

	switch r.FormValue("type") {
	case "fremco", "jetting":
		add("protocol_type = $%d", r.FormValue("type"))
	}
	if from := cleanFilterDate(r.FormValue("from")); from != "" {
		add("protocol_date >= $%d::date", from)
	}
	if to := cleanFilterDate(r.FormValue("to")); to != "" {
		add("protocol_date <= $%d::date", to)
	}
	if r.FormValue("only_missing") == "1" {
		where += " AND " + column + " IS NULL"
	}

	query := fmt.Sprintf("UPDATE protocols SET %s = $1%s", column, where)
	res, err := db.Exec(query, args...)
	if err != nil {
		log.Printf("bulk-assign: %v", err)
		return "Greška: " + err.Error()
	}
	n, _ := res.RowsAffected()
	return fmt.Sprintf("Dodeljeno: %d protokola → %s", n, orgNameByID(orgID))
}

// orgStats vraća organizacije sa brojem protokola po koloni i aliasima.
func orgStats() ([]OrgStat, error) {
	var out []OrgStat
	if err := db.Select(&out, `SELECT o.id, o.name, o.kind, o.invite_code,
			(SELECT count(*) FROM protocols p WHERE p.owner_org_id = o.id) AS owned,
			(SELECT count(*) FROM protocols p WHERE p.service_provider_org_id = o.id) AS credited,
			(SELECT count(*) FROM protocols p WHERE p.company_org_id = o.id) AS company
		FROM orgs o ORDER BY o.kind, o.name`); err != nil {
		return nil, err
	}

	type aliasRow struct {
		Alias string `db:"alias_normalized"`
		OrgID int    `db:"org_id"`
	}
	var aliases []aliasRow
	if err := db.Select(&aliases, `SELECT alias_normalized, org_id FROM org_aliases ORDER BY alias_normalized`); err != nil {
		return nil, err
	}
	byOrg := map[int][]string{}
	for _, a := range aliases {
		byOrg[a.OrgID] = append(byOrg[a.OrgID], a.Alias)
	}
	for i := range out {
		out[i].Aliases = byOrg[out[i].ID]
	}
	return out, nil
}
