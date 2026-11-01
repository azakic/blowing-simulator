package main

import (
	"database/sql"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/jmoiron/sqlx"
)

// Organizacije (vidi OWNERSHIP.md).
//
// Ideja: prava i udeo se računaju iz **naloga** (ko je uploadovao), a
// `company` / `service_provider` iz PDF-a su samo opis dokumenta. Zato partner
// sme da uploaduje i Jetting i Fremco bez obzira šta u dokumentu piše.
const (
	OrgKindInternal      = "internal"
	OrgKindSubcontractor = "subcontractor"
	OrgKindClient        = "client"
)

// ensureOrgSchema kreira tabele i kolone za vlasništvo po organizaciji.
// Idempotentno — poziva se pri svakom startu.
func ensureOrgSchema(db *sqlx.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS orgs (
			id         SERIAL PRIMARY KEY,
			name       TEXT NOT NULL UNIQUE,
			kind       TEXT NOT NULL DEFAULT 'subcontractor',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS org_aliases (
			alias_normalized TEXT PRIMARY KEY,
			org_id           INTEGER NOT NULL REFERENCES orgs(id) ON DELETE CASCADE
		)`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS org_id INTEGER REFERENCES orgs(id)`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active'`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS requested_org TEXT`,
		`ALTER TABLE protocols ADD COLUMN IF NOT EXISTS owner_org_id INTEGER REFERENCES orgs(id)`,
		`ALTER TABLE protocols ADD COLUMN IF NOT EXISTS company_org_id INTEGER REFERENCES orgs(id)`,
		`ALTER TABLE protocols ADD COLUMN IF NOT EXISTS service_provider_org_id INTEGER REFERENCES orgs(id)`,
		`ALTER TABLE protocols ADD COLUMN IF NOT EXISTS org_review BOOLEAN NOT NULL DEFAULT FALSE`,
		`CREATE TABLE IF NOT EXISTS registration_attempts (
			id         SERIAL PRIMARY KEY,
			ip         TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_registration_attempts ON registration_attempts(ip, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_protocols_owner_org ON protocols(owner_org_id)`,
		`CREATE INDEX IF NOT EXISTS idx_protocols_provider_org ON protocols(service_provider_org_id)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("orgs schema: %w", err)
		}
	}
	return nil
}

var legalFormRe = regexp.MustCompile(`(?i)\b(gmbh\s*&\s*co\.?\s*kg|gmbh|kg|ag|ug|gbr|ohg|se|e\.?\s?k\.?|inc\.?|ltd\.?|sarl|s\.?\s?a\.?)\b`)

// normalizeOrgName pravi ključ za poređenje naziva firmi: trim, kolabiranje
// razmaka, uklanjanje pravnog oblika, upper i transliteracija umlauta.
// Isti algoritam koristi i parser i backfill, pa `M.A.X. Bauservice` i
// `M.A.X. Bauservice GmbH` daju isti ključ.
func normalizeOrgName(s string) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if s == "" {
		return ""
	}
	s = strings.ToUpper(s)
	s = strings.NewReplacer("Ä", "AE", "Ö", "OE", "Ü", "UE", "ß", "SS", "ẞ", "SS").Replace(s)
	s = legalFormRe.ReplaceAllString(s, " ")
	// „Wolken-ASM" i „Wolken ASM" su ista firma — crta se tretira kao razmak.
	s = strings.NewReplacer("-", " ", "/", " ").Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	return strings.Trim(s, " ,.&")
}

// defaultOrgs se seju pri startu (idempotentno) da bi atribucija radila odmah
// posle deploy-a, bez ručnog rada u bazi.
var defaultOrgs = []struct {
	Name    string
	Kind    string
	Aliases []string
}{
	{
		Name: "Wolken-ASM GmbH",
		Kind: OrgKindInternal,
		Aliases: []string{
			"Wolken-ASM", "Wolken-ASM GmbH", "Wolken ASM", "Wolken-ASM GMBH",
		},
	},
	{
		Name: "M.A.X. Bauservice GmbH",
		Kind: OrgKindSubcontractor,
		Aliases: []string{
			"M.A.X. Bauservice", "M.A.X. Bauservice GmbH", "MAX Bauservice",
		},
	},
}

// seedOrgs osigurava da interne i poznate partnerske organizacije postoje.
func seedOrgs(db *sqlx.DB) error {
	for _, o := range defaultOrgs {
		var id int
		err := db.Get(&id, `SELECT id FROM orgs WHERE name = $1`, o.Name)
		if err == sql.ErrNoRows {
			if err := db.Get(&id, `INSERT INTO orgs (name, kind) VALUES ($1, $2) RETURNING id`, o.Name, o.Kind); err != nil {
				return fmt.Errorf("seed org %q: %w", o.Name, err)
			}
		} else if err != nil {
			return fmt.Errorf("seed org %q: %w", o.Name, err)
		}
		for _, a := range o.Aliases {
			key := normalizeOrgName(a)
			if key == "" {
				continue
			}
			if _, err := db.Exec(`INSERT INTO org_aliases (alias_normalized, org_id)
				VALUES ($1, $2) ON CONFLICT (alias_normalized) DO UPDATE SET org_id = EXCLUDED.org_id`,
				key, id); err != nil {
				return fmt.Errorf("seed alias %q: %w", a, err)
			}
		}
	}
	return nil
}

// resolveOrgID vraća organizaciju za naziv iz dokumenta (0 = nepoznata).
func resolveOrgID(name string) int {
	key := normalizeOrgName(name)
	if key == "" || db == nil {
		return 0
	}
	var id int
	if err := db.Get(&id, `SELECT org_id FROM org_aliases WHERE alias_normalized = $1`, key); err != nil {
		return 0
	}
	return id
}

// userOrgID vraća organizaciju korisnika (0 = nije dodeljena).
func userOrgID(userID int64) int64 {
	if userID == 0 || db == nil {
		return 0
	}
	var org sql.NullInt64
	if err := db.Get(&org, `SELECT org_id FROM users WHERE id = $1`, userID); err != nil {
		return 0
	}
	if !org.Valid {
		return 0
	}
	return org.Int64
}

// orgKindOf vraća tip organizacije ("" za nepoznat ID).
func orgKindOf(orgID int64) string {
	if orgID == 0 || db == nil {
		return ""
	}
	var kind string
	if err := db.Get(&kind, `SELECT kind FROM orgs WHERE id = $1`, orgID); err != nil {
		return ""
	}
	return kind
}

// Org je red za admin UI.
type Org struct {
	ID   int64  `db:"id"`
	Name string `db:"name"`
	Kind string `db:"kind"`
}

func listOrgs() ([]Org, error) {
	var out []Org
	err := db.Select(&out, `SELECT id, name, kind FROM orgs ORDER BY kind, name`)
	return out, err
}

// orgAttribution su organizacije koje se upisuju uz protokol.
type orgAttribution struct {
	Owner    sql.NullInt64
	Company  sql.NullInt64
	Provider sql.NullInt64
	Review   bool
}

// attributionFor računa atribuciju pri uploadu: vlasnik je organizacija
// naloga koji uploaduje (nikad se ne blokira zbog sadržaja PDF-a), a
// `company`/`service_provider` se prevode u organizacije preko aliasa.
//
// `org_review` se pali samo kad je uploader iz podizvođačke organizacije a
// dokument pripisuje posao **drugoj** firmi — to je signal za staff, ne
// prepreka za upload.
func attributionFor(ownerID int64, company, provider string) orgAttribution {
	var a orgAttribution
	if owner := userOrgID(ownerID); owner != 0 {
		a.Owner = sql.NullInt64{Int64: owner, Valid: true}
	}
	if id := resolveOrgID(company); id != 0 {
		a.Company = sql.NullInt64{Int64: int64(id), Valid: true}
	}
	if id := resolveOrgID(provider); id != 0 {
		a.Provider = sql.NullInt64{Int64: int64(id), Valid: true}
	}
	if a.Owner.Valid && a.Provider.Valid && a.Owner.Int64 != a.Provider.Int64 &&
		orgKindOf(a.Owner.Int64) == OrgKindSubcontractor {
		a.Review = true
	}
	return a
}

// backfillOrgAttribution popunjava organizacije na postojećim redovima.
// Ne menja vidljivost — samo priprema podatke za P3 (prava) i udeo.
func backfillOrgAttribution(db *sqlx.DB) error {
	if _, err := db.Exec(`UPDATE protocols p SET owner_org_id = u.org_id
		FROM users u
		WHERE p.user_id = u.id AND p.owner_org_id IS NULL AND u.org_id IS NOT NULL`); err != nil {
		return fmt.Errorf("backfill owner_org_id: %w", err)
	}

	type row struct {
		ID       int            `db:"id"`
		Company  sql.NullString `db:"company"`
		Provider sql.NullString `db:"service_provider"`
	}
	var rows []row
	if err := db.Select(&rows, `SELECT id, company, service_provider FROM protocols
		WHERE company_org_id IS NULL OR service_provider_org_id IS NULL`); err != nil {
		return fmt.Errorf("backfill select: %w", err)
	}
	var updated int
	for _, r := range rows {
		if r.Company.Valid {
			if id := resolveOrgID(r.Company.String); id != 0 {
				res, err := db.Exec(`UPDATE protocols SET company_org_id = $1
					WHERE id = $2 AND company_org_id IS NULL`, id, r.ID)
				if err != nil {
					return err
				}
				if n, _ := res.RowsAffected(); n > 0 {
					updated++
				}
			}
		}
		if r.Provider.Valid {
			if id := resolveOrgID(r.Provider.String); id != 0 {
				res, err := db.Exec(`UPDATE protocols SET service_provider_org_id = $1
					WHERE id = $2 AND service_provider_org_id IS NULL`, id, r.ID)
				if err != nil {
					return err
				}
				if n, _ := res.RowsAffected(); n > 0 {
					updated++
				}
			}
		}
	}
	if updated > 0 {
		log.Printf("orgs: backfill dodao organizaciju na %d polja", updated)
	}
	return nil
}

// setUserOrg dodeljuje korisnika organizaciji (0 = bez organizacije).
func setUserOrg(userID, orgID int64) error {
	_, err := db.Exec(`UPDATE users SET org_id = $1, updated_at = NOW() WHERE id = $2`,
		sql.NullInt64{Int64: orgID, Valid: orgID != 0}, userID)
	return err
}

// orgNameByID vraća naziv organizacije ("" ako ne postoji).
func orgNameByID(orgID int64) string {
	if orgID == 0 || db == nil {
		return ""
	}
	var name string
	if err := db.Get(&name, `SELECT name FROM orgs WHERE id = $1`, orgID); err != nil {
		return ""
	}
	return name
}

// partnerOrgs vraća podizvođačke organizacije za formu registracije.
func partnerOrgs() ([]Org, error) {
	var out []Org
	err := db.Select(&out, `SELECT id, name, kind FROM orgs
		WHERE kind = 'subcontractor' ORDER BY name`)
	return out, err
}

// internalOrgID vraća internu organizaciju (0 ako ne postoji).
func internalOrgID() int64 {
	if db == nil {
		return 0
	}
	var id int64
	if err := db.Get(&id, `SELECT id FROM orgs WHERE kind = 'internal' ORDER BY id LIMIT 1`); err != nil {
		return 0
	}
	return id
}

func sqlNullInt64(v int64) sql.NullInt64 {
	return sql.NullInt64{Int64: v, Valid: v != 0}
}
