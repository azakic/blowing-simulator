package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

// parseTelemetry opisuje kako je red nastao i šta je pri parsiranju bilo sumnjivo.
// Upisuje se u `protocols.parse_source`, `parse_warnings` i `parsed_at`.
type parseTelemetry struct {
	Source   string   // "text" | "ocr" | "mixed" | ""
	Warnings []string // ljudski čitljive poruke, redom nastanka
}

// ensureParseTelemetry dodaje kolone koje opisuju kvalitet parsiranja.
func ensureParseTelemetry(db *sqlx.DB) error {
	stmts := []string{
		`ALTER TABLE protocols ADD COLUMN IF NOT EXISTS parse_source TEXT`,
		`ALTER TABLE protocols ADD COLUMN IF NOT EXISTS parse_warnings JSONB`,
		`ALTER TABLE protocols ADD COLUMN IF NOT EXISTS parsed_at TIMESTAMPTZ`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("parse telemetry schema: %w", err)
		}
	}
	return nil
}

// ensureDetailColumns dodaje kolone za Jetting parametre koji ranije nisu imali
// mesto u bazi (Rohrinnendurchmesser, Rohraußendurchmesser, Verlegungsart,
// Kabeltrommel-Nr).
func ensureDetailColumns(db *sqlx.DB) error {
	stmts := []string{
		`ALTER TABLE protocol_equipment ADD COLUMN IF NOT EXISTS pipe_inner_diameter NUMERIC(5,2)`,
		`ALTER TABLE protocol_equipment ADD COLUMN IF NOT EXISTS pipe_outer_diameter NUMERIC(5,2)`,
		`ALTER TABLE protocol_equipment ADD COLUMN IF NOT EXISTS pipe_laying_type TEXT`,
		`ALTER TABLE protocol_equipment ADD COLUMN IF NOT EXISTS cable_drum_no TEXT`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("detail schema: %w", err)
		}
	}
	return nil
}

// buildWarnings pravi listu upozorenja.
//
// `pdf` su vrednosti pročitane iz dokumenta, a `hints` one iz imena fajla
// (uređaj ih generiše, pa su pouzdane). Razlika između njih nije greška
// parsera, ali je bitna — znači da ime fajla i sadržaj ne opisuju isti posao.
func buildWarnings(t parseTelemetry, textLen, measurements int, pdf, hints map[string]string) []string {
	warnings := append([]string{}, t.Warnings...)

	if t.Source == "ocr" || t.Source == "mixed" {
		warnings = append(warnings, "ocr: tekst je delimično iz OCR-a (proveri brojeve i umlaute)")
	}
	if textLen < 200 {
		warnings = append(warnings, fmt.Sprintf("short_text: samo %d znakova teksta", textLen))
	}
	if measurements == 0 {
		warnings = append(warnings, "no_measurements: no measurement points")
	}

	for _, field := range []string{"date", "time", "section", "project"} {
		p := strings.TrimSpace(pdf[field])
		h := strings.TrimSpace(hints[field])
		if p == "" || h == "" {
			continue
		}
		if !sameHint(field, p, h) {
			warnings = append(warnings, fmt.Sprintf("mismatch:%s: filename %q ≠ document %q", field, h, p))
		}
	}

	for _, field := range []string{"project", "operator", "company", "section"} {
		if strings.TrimSpace(pdf[field]) == "" {
			warnings = append(warnings, "empty:"+field)
		}
	}
	return warnings
}

// sameHint poredi vrednosti iz imena fajla i iz dokumenta po normalizovanom
// obliku (datum u ISO, vreme u HH:MM, tekst bez viška razmaka).
func sameHint(field, a, b string) bool {
	norm := func(s string) string {
		s = strings.TrimSpace(s)
		switch field {
		case "date":
			if t := parseDate(s); t != nil {
				return t.Format("2006-01-02")
			}
		case "time":
			if t := parseTime(s); t != nil {
				return t.Format("15:04")
			}
		case "section":
			// "Eiermarkt 14 B / 1V2200" (ime fajla) i
			// "Eiermarkt 14 B NVT 1V2200" (dokument) su isti podatak.
			s = strings.ToUpper(s)
			s = strings.ReplaceAll(s, "NVT", "")
			var b strings.Builder
			for _, r := range s {
				if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
					b.WriteRune(r)
				}
			}
			return b.String()
		}
		return strings.ToUpper(strings.Join(strings.Fields(s), " "))
	}
	return norm(a) == norm(b)
}

// saveParseTelemetry upisuje telemetriju za sačuvani red.
func saveParseTelemetry(db *sqlx.DB, protocolID int, t parseTelemetry) {
	if db == nil || protocolID <= 0 {
		return
	}
	payload := t.Warnings
	if payload == nil {
		payload = []string{}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("telemetry %d: json: %v", protocolID, err)
		return
	}
	if _, err := db.Exec(`UPDATE protocols
		SET parse_source = $2, parse_warnings = $3::jsonb, parsed_at = $4
		WHERE id = $1`, protocolID, nullIfEmpty(t.Source), string(data), time.Now()); err != nil {
		log.Printf("telemetry %d: update: %v", protocolID, err)
	}
}
