package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"

	"github.com/jmoiron/sqlx"
)

// ensureProtocolAccess kreira tabelu koja daje pristup protokolu koji je
// učitao neko drugi (isti dokument, drugi nalog).
//
// Zašto: `protocols.user_id` je jedan vlasnik (onaj koji je uploadovao), a
// partner sme da vidi samo svoje. Kad staff uveze dokument koji izvođač
// kasnije pošalje, ne želimo ni duplikat reda ni da partner ostane bez
// pristupa — zato eksplicitna tabela pristupa.
func ensureProtocolAccess(db *sqlx.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS protocol_access (
		protocol_id INTEGER NOT NULL REFERENCES protocols(id) ON DELETE CASCADE,
		user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		granted_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (protocol_id, user_id)
	)`)
	if err != nil {
		return fmt.Errorf("protocol_access: %w", err)
	}
	return nil
}

// hasProtocolAccess: vlasnik ili rola koja vidi sve, plus eksplicitno dodeljen
// pristup kroz `protocol_access`. Koristi se za čitanje (detalj, merenja,
// API, original). Brisanje ostaje samo za vlasnika/admina/editora.
func hasProtocolAccess(db *sqlx.DB, role string, userID int64, owner sql.NullInt64, protocolID int) bool {
	if canAccessProtocol(role, userID, owner) {
		return true
	}
	if db == nil || userID == 0 || protocolID <= 0 {
		return false
	}

	var ok bool
	if err := db.Get(&ok, `SELECT EXISTS (
		SELECT 1 FROM protocol_access WHERE protocol_id = $1 AND user_id = $2)`,
		protocolID, userID); err != nil {
		log.Printf("protocol_access: check for protocol %d failed: %v", protocolID, err)
		return false
	}
	if ok {
		return true
	}

	// Ista organizacija: protokol pripada firmi korisnika — ili ga je uvezao
	// neko iz firme (owner_org_id), ili ga dokument pripisuje firmi kao
	// podizvođaču (service_provider_org_id). Vidi OWNERSHIP.md.
	if orgID := userOrgID(userID); orgID != 0 {
		var sameOrg bool
		if err := db.Get(&sameOrg, `SELECT EXISTS (
			SELECT 1 FROM protocols p WHERE p.id = $1
			  AND (p.owner_org_id = $2 OR p.service_provider_org_id = $2))`,
			protocolID, orgID); err != nil {
			log.Printf("org access: check for protocol %d failed: %v", protocolID, err)
			return false
		}
		return sameOrg
	}
	return false
}

// grantProtocolAccess dodaje pristup (idempotentno).
func grantProtocolAccess(db *sqlx.DB, protocolID int, userID int64) error {
	if db == nil || protocolID <= 0 || userID == 0 {
		return nil
	}
	_, err := db.Exec(`INSERT INTO protocol_access (protocol_id, user_id)
		VALUES ($1, $2) ON CONFLICT DO NOTHING`, protocolID, userID)
	return err
}

// handleExistingUpload se poziva kad `skipExisting` naiđe na fajl sa istim
// imenom u bazi. **Ne pravi novi red.**
//
//   - pronalazi red koji odgovara sadržaju (isti SHA-256), ili — za protokole
//     bez originala (sve pre 2026-10-31) — jedini red sa tim imenom;
//   - ako je uploader drugi korisnik, dodeljuje mu pristup kroz
//     `protocol_access`;
//   - ako original nije sačuvan, čuva ga i povezuje sa tim redom.
//
// Vraća `handled=false` kad u bazi postoje samo fajlovi sa istim imenom ali
// **različitim sadržajem** — tada je to drugi dokument i ide u normalnu obradu
// (novi protokol).
// `content` se čita jednom u handleru: `multipart.File` je potrošen posle prvog
// čitanja, pa bi drugo čitanje vratilo prazan sadržaj.
func handleExistingUpload(filename string, ownerID int64, content []byte) (bool, string) {
	sha := sourceSHA256(content)

	type existingRow struct {
		ID     int            `db:"id"`
		UserID sql.NullInt64  `db:"user_id"`
		SHA    sql.NullString `db:"source_sha256"`
	}

	var rows []existingRow
	if err := db.Select(&rows, `SELECT id, user_id, source_sha256 FROM protocols
		WHERE source_filename = $1 ORDER BY id`, filename); err != nil {
		return true, "File already exists (could not verify: " + err.Error() + ")"
	}
	if len(rows) == 0 {
		return false, ""
	}

	var match *existingRow
	for i := range rows {
		if rows[i].SHA.Valid && rows[i].SHA.String == sha {
			match = &rows[i]
			break
		}
	}
	if match == nil {
		for i := range rows {
			if !rows[i].SHA.Valid {
				match = &rows[i]
				break
			}
		}
	}
	if match == nil {
		return false, ""
	}

	notes := []string{"File already exists"}

	if sourceStore.Enabled() && (!match.SHA.Valid || match.SHA.String == "") {
		shaNew, size, storeErr := prepareOriginal(context.Background(), content)
		if storeErr != nil {
			notes = append(notes, "storing the original failed: "+storeErr.Error())
		} else {
			linkOriginal(match.ID, shaNew, size)
			notes = append(notes, "original stored and linked")
		}
	}

	if ownerID != 0 && (!match.UserID.Valid || match.UserID.Int64 != ownerID) {
		if err := grantProtocolAccess(db, match.ID, ownerID); err != nil {
			notes = append(notes, "granting access failed: "+err.Error())
		} else {
			notes = append(notes, "access granted to your account")
		}
	}

	return true, strings.Join(notes, " — ")
}
