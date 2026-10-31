package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"blowing-simulator/internal/simulator"
)

// reparseResult opisuje šta je re-parse promenio za jedan protokol.
type reparseResult struct {
	ID         int               `json:"id"`
	Changed    map[string]string `json:"changed,omitempty"`
	Unchanged  bool              `json:"unchanged,omitempty"`
	Error      string            `json:"error,omitempty"`
	ParseLevel string            `json:"parse_source,omitempty"`
}

type protocolMeta struct {
	ID              int
	ProtocolType    string
	SourceFilename  sql.NullString
	SourceSHA       sql.NullString
	ProtocolDate    sql.NullString
	StartTime       sql.NullString
	ProjectNumber   sql.NullString
	SectionNVT      sql.NullString
	Address         sql.NullString
	Company         sql.NullString
	ServiceProvider sql.NullString
	Operator        sql.NullString
	Remarks         sql.NullString
	ParserVersion   sql.NullString
}

// AdminReparseHandler ponovo parsira sačuvani original (S3) i ažurira
// **metapodatke** reda. Merenja se ne diraju — ona se poklapaju sa originalom
// (provereno nad korpusom od 15 PDF-ova), pa bi ponovni upis bio samo rizik.
//
//	POST /admin/reparse?id=123
//	POST /admin/reparse?all=1&limit=20
func AdminReparseHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.FormValue("confirm") != "yes" {
		http.Error(w, "Dodaj confirm=yes (menja podatke u bazi)", http.StatusBadRequest)
		return
	}
	if sourceStore == nil || !sourceStore.Enabled() {
		http.Error(w, "Object Storage nije konfigurisan", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()
	var results []reparseResult

	if idStr := r.FormValue("id"); idStr != "" {
		id, err := strconv.Atoi(idStr)
		if err != nil || id <= 0 {
			http.Error(w, "Invalid protocol ID", http.StatusBadRequest)
			return
		}
		results = append(results, reparseOne(ctx, id))
	} else if r.FormValue("all") == "1" {
		limit := 20
		if v, err := strconv.Atoi(r.FormValue("limit")); err == nil && v > 0 && v <= 200 {
			limit = v
		}
		var ids []int
		if err := db.Select(&ids, `SELECT id FROM protocols
			WHERE source_sha256 IS NOT NULL AND source_sha256 <> ''
			ORDER BY id LIMIT $1`, limit); err != nil {
			http.Error(w, "DB: "+err.Error(), http.StatusInternalServerError)
			return
		}
		for _, id := range ids {
			results = append(results, reparseOne(ctx, id))
		}
	} else {
		http.Error(w, "Daj id=N ili all=1&limit=N", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"results": results,
	})
}

func reparseOne(ctx context.Context, id int) reparseResult {
	res := reparseResult{ID: id, Changed: map[string]string{}}

	var meta protocolMeta
	err := db.QueryRow(`
		SELECT id, protocol_type, source_filename, source_sha256,
		       protocol_date::text, start_time::text, project_number,
		       section_nvt, coalesce(address, ''), company, service_provider,
		       operator, remarks, parser_version
		FROM protocols WHERE id = $1`, id).Scan(
		&meta.ID, &meta.ProtocolType, &meta.SourceFilename, &meta.SourceSHA,
		&meta.ProtocolDate, &meta.StartTime, &meta.ProjectNumber,
		&meta.SectionNVT, &meta.Address, &meta.Company, &meta.ServiceProvider, &meta.Operator,
		&meta.Remarks, &meta.ParserVersion)
	if err != nil {
		res.Error = "protocol nije nađen: " + err.Error()
		return res
	}
	if !meta.SourceSHA.Valid || meta.SourceSHA.String == "" {
		res.Error = "nema sačuvanog originala (source_sha256 je prazan)"
		return res
	}

	data, err := sourceStore.Get(ctx, meta.SourceSHA.String)
	if err != nil {
		res.Error = err.Error()
		return res
	}

	tmp, err := os.CreateTemp("", "reparse_*.pdf")
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		res.Error = err.Error()
		return res
	}
	tmp.Close()

	text, textSource, err := extractPDFText(tmp.Name())
	if err != nil {
		res.Error = "ekstrakcija teksta: " + err.Error()
		return res
	}

	detected := simulator.DetectFormat(text)
	res.ParseLevel = string(detected)
	format := simulator.PDFFormat(strings.ToLower(meta.ProtocolType))
	if format != simulator.FormatFremco && format != simulator.FormatJetting {
		format = detected
	}
	if detected != simulator.FormatUnknown && detected != format {
		log.Printf("reparse %d: sačuvan tip %q, detektovano %q — koristim sačuvani tip", id, format, detected)
	}

	// Novi podaci iz PDF-a; prazna polja ne brišu postojeće vrednosti.
	next := map[string]string{}
	measurements := 0

	switch format {
	case simulator.FormatFremco:
		p := simulator.ParseFremcoProtocol(simulator.NormalizeFremcoTxt(text))
		if p == nil {
			res.Error = "Fremco parser vratio nil"
			return res
		}
		info := p.ProtocolInfo
		next["protocol_date"] = info.Date
		next["start_time"] = info.StartTime
		next["project_number"] = info.ProjectNumber
		next["section_nvt"] = info.SectionNVT
		next["address"] = extractAddressFromSectionNVT(info.SectionNVT)
		next["company"] = info.Company
		next["service_provider"] = info.ServiceProvider
		next["operator"] = info.Operator
		next["remarks"] = info.Remarks
		next["parser_version"] = p.ExportMetadata.ParserVersion
		measurements = len(p.Measurements.DataPoints)

	case simulator.FormatJetting:
		p := simulator.ParseJettingProtocol(simulator.NormalizeJettingTxt(text))
		if p == nil {
			res.Error = "Jetting parser vratio nil"
			return res
		}
		info := p.ProtocolInfo
		next["protocol_date"] = info.Date
		next["start_time"] = info.StartTime
		if info.ProjectNumber != nil {
			next["project_number"] = *info.ProjectNumber
		}
		next["section_nvt"] = info.SectionNVT
		next["address"] = extractAddressFromSectionNVT(info.SectionNVT)
		next["company"] = info.Company
		next["service_provider"] = info.ServiceProvider
		if info.Operator != nil {
			next["operator"] = *info.Operator
		}
		next["remarks"] = info.Remarks
		next["parser_version"] = p.ExportMetadata.ParserVersion
		measurements = len(p.Measurements.DataPoints)

	default:
		res.Error = "format nije prepoznat ni iz PDF-a ni iz baze"
		return res
	}

	current := map[string]string{
		"protocol_date":    meta.ProtocolDate.String,
		"start_time":       meta.StartTime.String,
		"project_number":   meta.ProjectNumber.String,
		"section_nvt":      meta.SectionNVT.String,
		"address":          meta.Address.String,
		"company":          meta.Company.String,
		"service_provider": meta.ServiceProvider.String,
		"operator":         meta.Operator.String,
		"remarks":          meta.Remarks.String,
		"parser_version":   meta.ParserVersion.String,
	}

	for field, newVal := range next {
		newVal = strings.TrimSpace(newVal)
		if newVal == "" {
			continue // ne brišemo ono što je već upisano
		}
		oldVal := strings.TrimSpace(current[field])

		// Datum, vreme i adresa se na uploadu uzimaju iz imena fajla, koje
		// generiše uređaj i nema OCR grešaka. PDF (OCR) ih popunjava samo ako
		// u bazi nema ničega; razliku samo logujemo.
		if filenameAuthoritative[field] && oldVal != "" {
			if !sameValue(field, oldVal, newVal) {
				log.Printf("reparse %d: %s se razlikuje (baza %q, PDF %q) — zadržavam postojeće", id, field, oldVal, newVal)
			}
			continue
		}

		if sameValue(field, oldVal, newVal) {
			continue
		}
		res.Changed[field] = oldVal + " -> " + newVal
	}

	// Telemetrija se osvežava i kada nema izmena — opisuje poslednje parsiranje.
	saveParseTelemetry(db, id, parseTelemetry{
		Source:   textSource,
		Warnings: buildWarnings(parseTelemetry{Source: textSource}, len(text), measurements, next, nil),
	})

	if len(res.Changed) == 0 {
		res.Unchanged = true
		return res
	}

	section := firstNonEmpty(next["section_nvt"], current["section_nvt"])
	_, err = db.Exec(`
		UPDATE protocols SET
			protocol_date = $2,
			start_time = $3,
			project_number = $4,
			section_nvt = $5,
			address = $6,
			company = $7,
			service_provider = $8,
			operator = $9,
			remarks = $10,
			parser_version = $11
		WHERE id = $1`,
		id,
		parseDate(firstNonEmpty(next["protocol_date"], current["protocol_date"])),
		parseTime(firstNonEmpty(next["start_time"], current["start_time"])),
		nullIfEmpty(firstNonEmpty(next["project_number"], current["project_number"])),
		nullIfEmpty(section),
		nullIfEmpty(firstNonEmpty(next["address"], current["address"])),
		nullIfEmpty(firstNonEmpty(next["company"], current["company"])),
		nullIfEmpty(firstNonEmpty(next["service_provider"], current["service_provider"])),
		nullIfEmpty(firstNonEmpty(next["operator"], current["operator"])),
		nullIfEmpty(firstNonEmpty(next["remarks"], current["remarks"])),
		nullIfEmpty(firstNonEmpty(next["parser_version"], current["parser_version"])),
	)
	if err != nil {
		res.Error = "UPDATE: " + err.Error()
		return res
	}
	return res
}

// filenameAuthoritative su polja koja se na uploadu pune iz imena fajla.
// Re-parse ih ne prepisuje OCR vrednošću (OCR ume da pogreši, npr. „1vV2200").
var filenameAuthoritative = map[string]bool{
	"protocol_date": true,
	"start_time":    true,
	"section_nvt":   true,
}

// sameValue poredi vrednosti po normalizovanom obliku (datum/vreme se u bazi
// vraćaju u drugom formatu nego što piše u PDF-u).
func sameValue(field, a, b string) bool {
	norm := func(s string) string {
		s = strings.TrimSpace(s)
		switch field {
		case "protocol_date":
			if t := parseDate(s); t != nil {
				return t.Format("2006-01-02")
			}
		case "start_time":
			if t := parseTime(s); t != nil {
				return t.Format("15:04")
			}
		}
		return s
	}
	return norm(a) == norm(b)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func nullIfEmpty(s string) interface{} {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.TrimSpace(s)
}
