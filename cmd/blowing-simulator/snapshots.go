package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

// Snapshot-i na protokolu: adresa i klijent tačno onako kako su bili u
// trenutku izdavanja dokumenta. Protokol se čita iz svojih kolona i danas,
// ali kada se čitanja prebace na `connections` (i kada se adresa u objektu
// ispravi), PDF mora da ostane isti kao potpisani.
//
// Snapshot se osvežava pri svakom snimanju protokola (izmena dokumenta je
// nova verzija), ali ne i kada se menja objekat ili direktorijum.

type addressSnapshot struct {
	Street       string `json:"street"`
	HouseNumber  string `json:"house_number"`
	Postcode     string `json:"postcode"`
	City         string `json:"city"`
	Address      string `json:"address"`
	NVT          string `json:"nvt"`
	ExternalRef  string `json:"external_ref,omitempty"`
	ConnectionID int64  `json:"connection_id,omitempty"`
	TakenAt      string `json:"taken_at"`
}

type customerSnapshot struct {
	Company   string `json:"company,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	Phone     string `json:"phone,omitempty"`
	Mobile    string `json:"mobile,omitempty"`
	Email     string `json:"email,omitempty"`
}

// ensureSnapshotColumns dodaje JSONB kolone na obe tabele protokola.
func ensureSnapshotColumns(db *sqlx.DB) error {
	stmts := []string{
		`ALTER TABLE protocols ADD COLUMN IF NOT EXISTS address_snapshot JSONB`,
		`ALTER TABLE protocols ADD COLUMN IF NOT EXISTS customer_snapshot JSONB`,
		`ALTER TABLE ha_protocols ADD COLUMN IF NOT EXISTS address_snapshot JSONB`,
		`ALTER TABLE ha_protocols ADD COLUMN IF NOT EXISTS customer_snapshot JSONB`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("snapshot schema: %w", err)
		}
	}
	return nil
}

// buildAddressSnapshot vraća JSON za `address_snapshot` (uvek, čak i prazan —
// bolje prazan snapshot nego NULL koji bi se kasnije tumačio kao „nema“).
func buildAddressSnapshot(street, houseNo, postcode, city, address, nvt, externalRef string, connectionID int64) []byte {
	snap := addressSnapshot{
		Street:       strings.TrimSpace(street),
		HouseNumber:  strings.TrimSpace(houseNo),
		Postcode:     strings.TrimSpace(postcode),
		City:         strings.TrimSpace(city),
		Address:      strings.TrimSpace(address),
		NVT:          strings.TrimSpace(nvt),
		ExternalRef:  strings.TrimSpace(externalRef),
		ConnectionID: connectionID,
		TakenAt:      time.Now().UTC().Format(time.RFC3339),
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return nil
	}
	return raw
}

// buildCustomerSnapshot vraća JSON za `customer_snapshot`; kada klijent nije
// poznat (importovani PDF-ovi ga ne nose) vraća NULL.
func buildCustomerSnapshot(company, first, last, phone, mobile, email string) []byte {
	snap := customerSnapshot{
		Company:   strings.TrimSpace(company),
		FirstName: strings.TrimSpace(first),
		LastName:  strings.TrimSpace(last),
		Phone:     strings.TrimSpace(phone),
		Mobile:    strings.TrimSpace(mobile),
		Email:     strings.TrimSpace(email),
	}
	if snap == (customerSnapshot{}) {
		return nil
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return nil
	}
	return raw
}

// backfillSnapshots popunjava snapshot-e za protokole izdate pre uvođenja
// kolona (importovani PDF-ovi i HaP-ovi). Idempotentno.
func backfillSnapshots(db *sqlx.DB) error {
	ctx := context.Background()

	// Importovani protokoli (Fremco/Jetting).
	{
		var rows []struct {
			ID           int64         `db:"id"`
			Address      string        `db:"address"`
			SectionNVT   string        `db:"section_nvt"`
			ConnectionID sql.NullInt64 `db:"connection_id"`
		}
		if err := db.SelectContext(ctx, &rows, `SELECT id, coalesce(address,'') AS address,
				coalesce(section_nvt,'') AS section_nvt, connection_id
			FROM protocols WHERE address_snapshot IS NULL`); err != nil {
			return err
		}
		for _, r := range rows {
			st, no, plz, ct := parseAddress(r.Address)
			nv := nvtFromSection(r.SectionNVT)
			connID := int64(0)
			if r.ConnectionID.Valid {
				connID = r.ConnectionID.Int64
			}
			snap := buildAddressSnapshot(st, no, plz, ct, r.Address, nv, "", connID)
			if _, err := db.ExecContext(ctx, `UPDATE protocols SET address_snapshot=$1::jsonb
				WHERE id=$2`, snap, r.ID); err != nil {
				log.Printf("snapshots: protocol %d: %v", r.ID, err)
			}
		}
	}

	// HaP protokoli.
	{
		var rows []struct {
			ID              int64         `db:"id"`
			Address         string        `db:"address"`
			City            string        `db:"city"`
			NVT             string        `db:"nvt"`
			ClientCompany   string        `db:"client_company"`
			ClientFirstName string        `db:"client_first_name"`
			ClientLastName  string        `db:"client_last_name"`
			ClientPhone     string        `db:"client_phone"`
			ClientMobile    string        `db:"client_mobile"`
			ClientEmail     string        `db:"client_email"`
			ConnectionID    sql.NullInt64 `db:"connection_id"`
		}
		if err := db.SelectContext(ctx, &rows, `SELECT id,
				coalesce(address,'') AS address, coalesce(city,'') AS city,
				coalesce(nvt,'') AS nvt,
				coalesce(client_company,'') AS client_company,
				coalesce(client_first_name,'') AS client_first_name,
				coalesce(client_last_name,'') AS client_last_name,
				coalesce(client_phone,'') AS client_phone,
				coalesce(client_mobile,'') AS client_mobile,
				coalesce(client_email,'') AS client_email,
				connection_id
			FROM ha_protocols WHERE address_snapshot IS NULL`); err != nil {
			return err
		}
		for _, r := range rows {
			st, no, plz, _ := parseAddress(r.Address)
			connID := int64(0)
			if r.ConnectionID.Valid {
				connID = r.ConnectionID.Int64
			}
			addr := buildAddressSnapshot(st, no, plz, r.City, r.Address, r.NVT, "", connID)
			cust := buildCustomerSnapshot(r.ClientCompany, r.ClientFirstName, r.ClientLastName,
				r.ClientPhone, r.ClientMobile, r.ClientEmail)
			if _, err := db.ExecContext(ctx, `UPDATE ha_protocols
				SET address_snapshot=$1::jsonb, customer_snapshot=$2::jsonb WHERE id=$3`,
				addr, cust, r.ID); err != nil {
				log.Printf("snapshots: ha %d: %v", r.ID, err)
			}
		}
	}
	return nil
}
