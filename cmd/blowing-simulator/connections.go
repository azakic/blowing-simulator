package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/jmoiron/sqlx"
)

// Objekat / Anschluss (faza 2): jedan red po paru (ulica+broj, NVT).
//
// Zadatak, HaP i importovani protokol su do sada svaki čuvali svoju kopiju
// adrese. `connections` je zajednički objekat na koji se oni vezuju preko
// `connection_id`; postojeće kolone ostaju kao snapshot (PDF se ne sme
// menjati retroaktivno), pa ništa ne čita iz nove tabele dok se ne odluči.

func ensureConnectionsSchema(db *sqlx.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS connections (
			id           SERIAL PRIMARY KEY,
			street       TEXT NOT NULL DEFAULT '',
			house_number TEXT NOT NULL DEFAULT '',
			postcode     TEXT NOT NULL DEFAULT '',
			city         TEXT NOT NULL DEFAULT '',
			address      TEXT NOT NULL DEFAULT '',
			street_key   TEXT NOT NULL DEFAULT '',
			nvt          TEXT NOT NULL DEFAULT '',
			nvt_key      TEXT NOT NULL DEFAULT '',
			external_ref TEXT NOT NULL DEFAULT '',
			notes        TEXT NOT NULL DEFAULT '',
			created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at   TIMESTAMPTZ
		)`,
		// Ključ je par (ulica+broj, NVT) — bez NVT ostaje po jedan objekat
		// na adresu. Grad/PLZ se dopunjuju, ali ne učestvuju u ključu jer
		// Excel uvoz ih često ne nosi.
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_connections_street_nvt
			ON connections (street_key, nvt_key) WHERE street_key <> ''`,
		`CREATE INDEX IF NOT EXISTS idx_connections_nvt ON connections (nvt_key)`,
		`ALTER TABLE tasks ADD COLUMN IF NOT EXISTS connection_id INTEGER REFERENCES connections(id) ON DELETE SET NULL`,
		`ALTER TABLE ha_protocols ADD COLUMN IF NOT EXISTS connection_id INTEGER REFERENCES connections(id) ON DELETE SET NULL`,
		`ALTER TABLE protocols ADD COLUMN IF NOT EXISTS connection_id INTEGER REFERENCES connections(id) ON DELETE SET NULL`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_connection ON tasks (connection_id)`,
		`CREATE INDEX IF NOT EXISTS idx_ha_connection ON ha_protocols (connection_id)`,
		`CREATE INDEX IF NOT EXISTS idx_protocols_connection ON protocols (connection_id)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("connections schema: %w", err)
		}
	}
	return nil
}

// connectionParts deli slobodnu adresu na delove i dopunjuje ono što nije
// popunjeno (tasks ima razdvojena polja, HaP i protokoli samo `address`).
func connectionParts(street, houseNo, postcode, city, address, nvt string) (string, string, string, string, string) {
	ps, pn, pp, pc := parseAddress(address)
	st := strings.TrimSpace(street)
	if st == "" {
		st = ps
	}
	no := strings.TrimSpace(houseNo)
	if no == "" {
		no = pn
	}
	plz := strings.TrimSpace(postcode)
	if plz == "" {
		plz = pp
	}
	ct := strings.TrimSpace(city)
	if ct == "" {
		ct = pc
	}
	return st, no, plz, ct, strings.TrimSpace(nvt)
}

// upsertConnection vraća id objekta za (ulica+broj, NVT), stvarajući ga ako
// ne postoji. Postojeće PLZ/Ort se ne prepisuju praznim vrednostima.
func upsertConnection(ctx context.Context, ext sqlx.ExtContext, street, houseNo, postcode, city, nvt string) (int64, error) {
	street = strings.TrimSpace(street)
	houseNo = strings.TrimSpace(houseNo)
	// Slug (bez tačaka/razmaka) da se „Dammstr.9“ i „Dammstr. 9“ spoje u isti
	// objekat — uvoz iz PDF-a često nema razmak pre broja.
	sk := addressSlug(strings.TrimSpace(street + " " + houseNo))
	if sk == "" {
		return 0, nil
	}
	var id int64
	err := sqlx.GetContext(ctx, ext, &id, `INSERT INTO connections
			(street, house_number, postcode, city, address, street_key, nvt, nvt_key)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (street_key, nvt_key) WHERE street_key <> ''
		DO UPDATE SET
			postcode = CASE WHEN connections.postcode = '' THEN EXCLUDED.postcode ELSE connections.postcode END,
			city     = CASE WHEN connections.city = '' THEN EXCLUDED.city ELSE connections.city END,
			address  = CASE WHEN connections.address = '' THEN EXCLUDED.address ELSE connections.address END,
			updated_at = NOW()
		RETURNING id`,
		street, houseNo, strings.TrimSpace(postcode), strings.TrimSpace(city),
		formatAddress(street, houseNo, postcode, city), sk,
		strings.TrimSpace(nvt), nvtKeyOf(nvt))
	if err != nil {
		return 0, err
	}
	return id, nil
}

// backfillConnections povezuje postojeće zadatke, HaP-ove i importovane
// protokole sa objektima. Idempotentno: radi samo tamo gde je `connection_id`
// prazan, pa je bezbedno zvati na svakom startu.
func backfillConnections(db *sqlx.DB) error {
	ctx := context.Background()

	// 1) Tasks — street/house/postcode/city su već razdvojeni.
	{
		var rows []struct {
			ID          int64  `db:"id"`
			Street      string `db:"street"`
			HouseNumber string `db:"house_number"`
			Postcode    string `db:"postcode"`
			City        string `db:"city"`
			Address     string `db:"address"`
			NVT         string `db:"nvt"`
		}
		if err := db.SelectContext(ctx, &rows, `SELECT id,
				coalesce(street,'') AS street, coalesce(house_number,'') AS house_number,
				coalesce(postcode,'') AS postcode, coalesce(city,'') AS city,
				coalesce(address,'') AS address, coalesce(nvt,'') AS nvt
			FROM tasks WHERE connection_id IS NULL`); err != nil {
			return err
		}
		linked := 0
		for _, r := range rows {
			st, no, plz, ct, nv := connectionParts(r.Street, r.HouseNumber, r.Postcode, r.City, r.Address, r.NVT)
			id, err := upsertConnection(ctx, db, st, no, plz, ct, nv)
			if err != nil {
				log.Printf("connections: task %d: %v", r.ID, err)
				continue
			}
			if id == 0 {
				continue
			}
			if _, err := db.ExecContext(ctx, `UPDATE tasks SET connection_id=$1 WHERE id=$2`, id, r.ID); err == nil {
				linked++
			}
		}
		if linked > 0 {
			log.Printf("connections: %d task(s) linked", linked)
		}
	}

	// 2) HaP — address + city + nvt.
	{
		var rows []struct {
			ID      int64  `db:"id"`
			Address string `db:"address"`
			City    string `db:"city"`
			NVT     string `db:"nvt"`
		}
		if err := db.SelectContext(ctx, &rows, `SELECT id,
				coalesce(address,'') AS address, coalesce(city,'') AS city,
				coalesce(nvt,'') AS nvt
			FROM ha_protocols WHERE connection_id IS NULL`); err != nil {
			return err
		}
		linked := 0
		for _, r := range rows {
			st, no, plz, ct, nv := connectionParts("", "", "", r.City, r.Address, r.NVT)
			id, err := upsertConnection(ctx, db, st, no, plz, ct, nv)
			if err != nil {
				log.Printf("connections: ha %d: %v", r.ID, err)
				continue
			}
			if id == 0 {
				continue
			}
			if _, err := db.ExecContext(ctx, `UPDATE ha_protocols SET connection_id=$1 WHERE id=$2`, id, r.ID); err == nil {
				linked++
			}
		}
		if linked > 0 {
			log.Printf("connections: %d HaP(s) linked", linked)
		}
	}

	// 3) Importovani protokoli — address + NVT iz `section_nvt`.
	{
		var rows []struct {
			ID         int64  `db:"id"`
			Address    string `db:"address"`
			SectionNVT string `db:"section_nvt"`
		}
		if err := db.SelectContext(ctx, &rows, `SELECT id,
				coalesce(address,'') AS address, coalesce(section_nvt,'') AS section_nvt
			FROM protocols WHERE connection_id IS NULL`); err != nil {
			return err
		}
		linked := 0
		for _, r := range rows {
			st, no, plz, ct, nv := connectionParts("", "", "", "", r.Address, nvtFromSection(r.SectionNVT))
			id, err := upsertConnection(ctx, db, st, no, plz, ct, nv)
			if err != nil {
				log.Printf("connections: protocol %d: %v", r.ID, err)
				continue
			}
			if id == 0 {
				continue
			}
			if _, err := db.ExecContext(ctx, `UPDATE protocols SET connection_id=$1 WHERE id=$2`, id, r.ID); err == nil {
				linked++
			}
		}
		if linked > 0 {
			log.Printf("connections: %d protocol(s) linked", linked)
		}
	}
	return nil
}

// backfillConnectionNVTKeys normalizuje `nvt_key` i spaja objekte koji su
// posle normalizacije isti („N-1.31“ i „1.31“). Idempotentno; sve veze
// (zadaci, HaP, protokoli) prelaze na objekat koji ostaje.
func backfillConnectionNVTKeys(db *sqlx.DB) error {
	ctx := context.Background()
	var rows []struct {
		ID        int64  `db:"id"`
		StreetKey string `db:"street_key"`
		NVT       string `db:"nvt"`
		NVTKey    string `db:"nvt_key"`
	}
	if err := db.SelectContext(ctx, &rows, `SELECT id, coalesce(street_key,'') AS street_key,
			coalesce(nvt,'') AS nvt, coalesce(nvt_key,'') AS nvt_key
		FROM connections`); err != nil {
		return err
	}

	merged := 0
	for _, r := range rows {
		want := nvtKeyOf(r.NVT)
		if want == r.NVTKey {
			continue
		}
		var keepID int64
		err := db.GetContext(ctx, &keepID, `SELECT id FROM connections
			WHERE street_key = $1 AND nvt_key = $2 AND id <> $3 LIMIT 1`,
			r.StreetKey, want, r.ID)
		if err == nil && keepID > 0 {
			if mergeConnections(db, r.ID, keepID) {
				merged++
			} else {
				log.Printf("connections: merge %d -> %d failed", r.ID, keepID)
			}
			continue
		}
		if _, err := db.ExecContext(ctx, `UPDATE connections SET nvt_key=$1, updated_at=NOW()
			WHERE id=$2`, want, r.ID); err != nil {
			log.Printf("connections: key %d: %v", r.ID, err)
		}
	}
	if merged > 0 {
		log.Printf("connections: %d duplicate object(s) merged", merged)
	}
	return nil
}

// mergeConnections prebacuje sve veze sa `from` na `keep` i briše duplikat.
func mergeConnections(db *sqlx.DB, from, keep int64) bool {
	tx, err := db.Beginx()
	if err != nil {
		return false
	}
	for _, q := range []string{
		`UPDATE tasks SET connection_id=$1 WHERE connection_id=$2`,
		`UPDATE ha_protocols SET connection_id=$1 WHERE connection_id=$2`,
		`UPDATE protocols SET connection_id=$1 WHERE connection_id=$2`,
	} {
		if _, err := tx.Exec(q, keep, from); err != nil {
			_ = tx.Rollback()
			log.Printf("connections merge: %v", err)
			return false
		}
	}
	if _, err := tx.Exec(`DELETE FROM connections WHERE id=$1`, from); err != nil {
		_ = tx.Rollback()
		return false
	}
	return tx.Commit() == nil
}
