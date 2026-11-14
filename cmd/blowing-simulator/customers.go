package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"math"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/extrame/xls"
	"github.com/jmoiron/sqlx"
	"github.com/xuri/excelize/v2"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
)

// Import direktorijuma klijenata (Adresse, NVT, Kunde, Telefon, Mobil) iz
// Excel/CSV fajlova. Služi kao prediktivni izvor podataka pri izradi HaP-a:
// kada se u formu unese NVT ili adresa, app predlaže klijenta i telefone.
//
// Import je dozvoljen samo punom administratoru (/admin/import).

const maxCustomerRows = 200000

func ensureCustomerSchema(db *sqlx.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS ha_customers (
			id          SERIAL PRIMARY KEY,
			address     TEXT NOT NULL DEFAULT '',
			nvt         TEXT NOT NULL DEFAULT '',
			kunde       TEXT NOT NULL DEFAULT '',
			phone       TEXT NOT NULL DEFAULT '',
			mobile      TEXT NOT NULL DEFAULT '',
			nvt_key     TEXT NOT NULL DEFAULT '',
			address_key TEXT NOT NULL DEFAULT '',
			source      TEXT NOT NULL DEFAULT '',
			imported_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at  TIMESTAMPTZ
		)`,
		// Normalizovana adresa (ulica/broj/PLZ/grad). „address“ ostaje za
		// prikaz; spajanje ide na ulicu+broj (bez grada) i NVT.
		`ALTER TABLE ha_customers ADD COLUMN IF NOT EXISTS street TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ha_customers ADD COLUMN IF NOT EXISTS house_number TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ha_customers ADD COLUMN IF NOT EXISTS postcode TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ha_customers ADD COLUMN IF NOT EXISTS city TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ha_customers ADD COLUMN IF NOT EXISTS email TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ha_customers ADD COLUMN IF NOT EXISTS street_key TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS idx_ha_customers_street_key ON ha_customers (street_key)`,
		// NVT nije jedinstven (npr. „1.25“ deli više adresa) — ključ za
		// spajanje je adresa, a NVT je dodatni podatak i sekundarni filter.
		`DROP INDEX IF EXISTS idx_ha_customers_nvt`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_ha_customers_addr ON ha_customers (address_key) WHERE address_key <> ''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_ha_customers_nvt_kunde ON ha_customers (nvt_key, kunde) WHERE address_key = ''`,
		`CREATE INDEX IF NOT EXISTS idx_ha_customers_nvt ON ha_customers (nvt_key)`,
		`CREATE INDEX IF NOT EXISTS idx_ha_customers_addr_prefix ON ha_customers (address_key text_pattern_ops)`,
		`CREATE TABLE IF NOT EXISTS ha_customer_imports (
			id          SERIAL PRIMARY KEY,
			filename    TEXT NOT NULL DEFAULT '',
			total       INTEGER NOT NULL DEFAULT 0,
			imported    INTEGER NOT NULL DEFAULT 0,
			updated     INTEGER NOT NULL DEFAULT 0,
			skipped     INTEGER NOT NULL DEFAULT 0,
			failed      INTEGER NOT NULL DEFAULT 0,
			imported_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`ALTER TABLE ha_customer_imports ADD COLUMN IF NOT EXISTS unchanged INTEGER NOT NULL DEFAULT 0`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("customer schema: %w", err)
		}
	}
	return nil
}

// --- parsiranje ---

type customerRow struct {
	ID          int64  `db:"id" json:"id,omitempty"`
	Address     string `db:"address" json:"address"`
	NVT         string `db:"nvt" json:"nvt"`
	Kunde       string `db:"kunde" json:"kunde"`
	Phone       string `db:"phone" json:"phone"`
	Mobile      string `db:"mobile" json:"mobile"`
	Street      string `db:"street" json:"street"`
	HouseNumber string `db:"house_number" json:"house_number"`
	Postcode    string `db:"postcode" json:"postcode"`
	City        string `db:"city" json:"city"`
	Email       string `db:"email" json:"email"`
	Source      string `db:"source" json:"-"`
}

type parsedCustomerFile struct {
	Rows           []customerRow
	HeaderDetected bool
	Sheets         int
	Format         string
}

type customerImportResult struct {
	Filename       string
	Format         string
	HeaderDetected bool
	Rows           int
	Imported       int
	Updated        int
	Unchanged      int
	Skipped        int
	Failed         int
	Errors         []string
}

func (r *customerImportResult) addError(row int, err error) {
	r.Failed++
	if len(r.Errors) < 25 {
		r.Errors = append(r.Errors, fmt.Sprintf("row %d: %v", row, err))
	}
}

// parseCustomerFile bira parser po ekstenziji (uz sniffing sadržaja kada
// ekstenzija nije poznata).
func parseCustomerFile(name string, data []byte) (parsedCustomerFile, error) {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".xlsx", ".xlsm", ".xltx", ".xltm":
		return parseCustomerXLSX(data)
	case ".xls":
		return parseCustomerXLS(data)
	case ".csv", ".txt", ".tsv":
		return parseCustomerCSV(name, data)
	}
	switch {
	case len(data) > 2 && data[0] == 'P' && data[1] == 'K':
		return parseCustomerXLSX(data)
	case len(data) > 8 && bytes.HasPrefix(data, []byte{0xD0, 0xCF, 0x11, 0xE0}):
		return parseCustomerXLS(data)
	}
	return parseCustomerCSV(name, data)
}

func parseCustomerXLSX(data []byte) (out parsedCustomerFile, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = parsedCustomerFile{}, fmt.Errorf("xlsx: %v", r)
		}
	}()
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return out, fmt.Errorf("xlsx: %w", err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	out.Format = "xlsx"
	out.Sheets = len(sheets)
	var fallback [][]string
	for _, name := range sheets {
		raw, err := readXLSXSheet(f, name)
		if err != nil {
			return out, err
		}
		if len(raw) == 0 {
			continue
		}
		if _, _, ok := detectHeader(raw); ok {
			rows, detected := rowsFromTable(raw)
			out.Rows, out.HeaderDetected = dedupeRows(rows), detected
			return out, nil
		}
		if fallback == nil {
			fallback = raw
		}
	}
	if fallback == nil {
		return out, errors.New("xlsx: nema podataka ni u jednom listu")
	}
	rows, detected := rowsFromTable(fallback)
	out.Rows, out.HeaderDetected = dedupeRows(rows), detected
	return out, nil
}

func readXLSXSheet(f *excelize.File, name string) ([][]string, error) {
	iter, err := f.Rows(name)
	if err != nil {
		return nil, fmt.Errorf("xlsx %s: %w", name, err)
	}
	defer iter.Close()
	var raw [][]string
	for iter.Next() {
		cols, err := iter.Columns()
		if err != nil {
			return nil, fmt.Errorf("xlsx %s: %w", name, err)
		}
		raw = append(raw, cols)
		if len(raw) > maxCustomerRows {
			return nil, fmt.Errorf("previše redova (max %d)", maxCustomerRows)
		}
	}
	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("xlsx %s: %w", name, err)
	}
	return raw, nil
}

func parseCustomerXLS(data []byte) (out parsedCustomerFile, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = parsedCustomerFile{}, fmt.Errorf("xls: %v", r)
		}
	}()
	wb, err := xls.OpenReader(bytes.NewReader(data), "utf-8")
	if err != nil {
		return out, fmt.Errorf("xls: %w", err)
	}
	out.Format = "xls"
	out.Sheets = wb.NumSheets()
	if out.Sheets == 0 {
		return out, errors.New("xls: nema listova")
	}
	var fallback [][]string
	for i := 0; i < out.Sheets; i++ {
		raw := readXLSSheet(wb.GetSheet(i))
		if len(raw) == 0 {
			continue
		}
		if _, _, ok := detectHeader(raw); ok {
			rows, detected := rowsFromTable(raw)
			out.Rows, out.HeaderDetected = dedupeRows(rows), detected
			return out, nil
		}
		if fallback == nil {
			fallback = raw
		}
	}
	if fallback == nil {
		return out, errors.New("xls: nema podataka ni u jednom listu")
	}
	rows, detected := rowsFromTable(fallback)
	out.Rows, out.HeaderDetected = dedupeRows(rows), detected
	return out, nil
}

func readXLSSheet(sheet *xls.WorkSheet) [][]string {
	if sheet == nil {
		return nil
	}
	var raw [][]string
	for i := 0; i <= int(sheet.MaxRow); i++ {
		row := sheet.Row(i)
		if row == nil {
			continue
		}
		last := row.LastCol()
		if last <= 0 {
			continue
		}
		rec := make([]string, last)
		for c := 0; c < last; c++ {
			rec[c] = row.Col(c)
		}
		raw = append(raw, rec)
	}
	return raw
}

func parseCustomerCSV(name string, data []byte) (parsedCustomerFile, error) {
	out := parsedCustomerFile{Format: "csv", Sheets: 1}
	data = decodeText(data)
	if len(data) == 0 {
		return out, errors.New("prazan fajl")
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = detectDelimiter(data)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	var raw [][]string
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return out, fmt.Errorf("csv: %w", err)
		}
		raw = append(raw, rec)
		if len(raw) > maxCustomerRows {
			return out, fmt.Errorf("previše redova (max %d)", maxCustomerRows)
		}
	}
	rows, detected := rowsFromTable(raw)
	out.Rows, out.HeaderDetected = dedupeRows(rows), detected
	return out, nil
}

// decodeText skida BOM i prevodi Windows-1252/UTF-16 u UTF-8.
func decodeText(data []byte) []byte {
	switch {
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		return data[3:]
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}):
		return decodeUTF16(data[2:], binary.LittleEndian)
	case bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		return decodeUTF16(data[2:], binary.BigEndian)
	}
	if utf8.Valid(data) {
		return data
	}
	if out, _, err := transform.Bytes(charmap.Windows1252.NewDecoder(), data); err == nil {
		return out
	}
	return data
}

func decodeUTF16(data []byte, order binary.ByteOrder) []byte {
	units := make([]uint16, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		units = append(units, order.Uint16(data[i:i+2]))
	}
	return []byte(string(utf16.Decode(units)))
}

// detectDelimiter bira najčešći razdvajač u prvom redu (izvan navodnika).
func detectDelimiter(data []byte) rune {
	line := data
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		line = data[:i]
	}
	best, bestN := ',', 0
	for _, d := range []byte{';', ',', '\t', '|'} {
		n := 0
		inQuote := false
		for _, b := range line {
			if b == '"' {
				inQuote = !inQuote
				continue
			}
			if !inQuote && b == d {
				n++
			}
		}
		if n > bestN {
			best, bestN = rune(d), n
		}
	}
	return best
}

// --- mapiranje kolona ---

type customerCols struct {
	address, nvt, kunde, phone, mobile, postcode, city, email int
}

// Kada zaglavlje nije prepoznato, koristi se stari raspored bez mesta i
// e-pošte (kolone -1 = nema ih).
var customerColDefaults = customerCols{
	address: 0, nvt: 1, kunde: 2, phone: 3, mobile: 4,
	postcode: -1, city: -1, email: -1,
}

func (c customerCols) get(rec []string, idx int) string {
	if idx < 0 || idx >= len(rec) {
		return ""
	}
	return rec[idx]
}

func rowsFromTable(raw [][]string) ([]customerRow, bool) {
	headerIdx, cmap, detected := detectHeader(raw)
	if !detected {
		cmap = customerColDefaults
	}
	out := make([]customerRow, 0, len(raw))
	for i, rec := range raw {
		if detected && i == headerIdx {
			continue
		}
		if isEmptyRow(rec) {
			continue
		}
		row := customerRow{
			Address:  cleanCell(cmap.get(rec, cmap.address)),
			NVT:      cleanCell(cmap.get(rec, cmap.nvt)),
			Kunde:    cleanCell(cmap.get(rec, cmap.kunde)),
			Phone:    cleanCell(cmap.get(rec, cmap.phone)),
			Mobile:   cleanCell(cmap.get(rec, cmap.mobile)),
			Postcode: cleanCell(cmap.get(rec, cmap.postcode)),
			City:     cleanCell(cmap.get(rec, cmap.city)),
			Email:    cleanCell(cmap.get(rec, cmap.email)),
		}
		if row.Address == "" && row.NVT == "" && row.Kunde == "" && row.Phone == "" && row.Mobile == "" {
			continue
		}
		out = append(out, row)
	}
	return out, detected
}

func detectHeader(raw [][]string) (int, customerCols, bool) {
	limit := len(raw)
	if limit > 5 {
		limit = 5
	}
	for i := 0; i < limit; i++ {
		if m, ok := matchHeader(raw[i]); ok {
			return i, m, true
		}
	}
	return -1, customerColDefaults, false
}

func matchHeader(rec []string) (customerCols, bool) {
	m := customerCols{-1, -1, -1, -1, -1, -1, -1, -1}
	found := 0
	for i, cell := range rec {
		k := normalizeHeader(cell)
		if k == "" {
			continue
		}
		switch {
		case m.mobile < 0 && (strings.Contains(k, "mobil") || strings.Contains(k, "handy")):
			m.mobile, found = i, found+1
		case m.phone < 0 && isPhoneHeader(k):
			m.phone, found = i, found+1
		case m.email < 0 && isEmailHeader(k):
			m.email, found = i, found+1
		case m.postcode < 0 && isPostcodeHeader(k):
			m.postcode, found = i, found+1
		case m.city < 0 && isCityHeader(k):
			m.city, found = i, found+1
		case m.address < 0 && isAddressHeader(k):
			m.address, found = i, found+1
		case m.nvt < 0 && strings.Contains(k, "nvt"):
			m.nvt, found = i, found+1
		case m.kunde < 0 && isKundeHeader(k):
			m.kunde, found = i, found+1
		}
	}
	if found < 2 || (m.address < 0 && m.nvt < 0) {
		return customerCols{}, false
	}
	return m, true
}

func normalizeHeader(s string) string {
	s = cleanCell(s)
	s = strings.ToLower(s)
	return strings.NewReplacer(" ", "", ".", "", "_", "", "-", "", "/", "").Replace(s)
}

func isAddressHeader(k string) bool {
	return strings.Contains(k, "adresse") || strings.Contains(k, "adress") ||
		strings.Contains(k, "anschrift") || strings.Contains(k, "straße") ||
		strings.Contains(k, "strasse")
}

func isPhoneHeader(k string) bool {
	switch k {
	case "tel", "fon", "telefon", "telefonnummer", "festnetz", "rufnummer":
		return true
	}
	return strings.Contains(k, "telefon")
}

func isPostcodeHeader(k string) bool {
	switch k {
	case "plz", "postleitzahl", "postcode", "zip", "zipcode", "postleitnummer":
		return true
	}
	return false
}

// isCityHeader prihvata i Ortsteil, jer se u praksi javlja kao „Ort“ kolona.
func isCityHeader(k string) bool {
	switch k {
	case "ort", "ortsteil", "stadt", "gemeinde", "wohnort", "city":
		return true
	}
	return false
}

func isEmailHeader(k string) bool {
	return k == "mail" || strings.Contains(k, "email")
}

// importAddressParts deli adresu, a PLZ/Ort dopunjuje iz zasebnih kolona
// (Excel može da ih ima odvojeno, a ne mora ni jedno ni drugo).
func importAddressParts(row customerRow) (street, houseNo, postcode, city string) {
	street, houseNo, postcode, city = parseAddress(row.Address)
	if postcode == "" {
		postcode = strings.TrimSpace(row.Postcode)
	}
	if city == "" {
		city = strings.TrimSpace(row.City)
	}
	return street, houseNo, postcode, city
}

func isKundeHeader(k string) bool {
	switch k {
	case "name", "kunde", "kundenname", "anschlussnehmer", "teilnehmer":
		return true
	}
	return strings.Contains(k, "kunde") || strings.Contains(k, "anschlussnehmer")
}

func isEmptyRow(rec []string) bool {
	for _, c := range rec {
		if cleanCell(c) != "" {
			return false
		}
	}
	return true
}

var numericCell = regexp.MustCompile(`^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$`)

// cleanCell normalizuje vrednost ćelije. Excel često čuva telefone kao brojeve
// i prikaže ih u naučnoj notaciji (4.923349096E+11) — takve vrednosti vraćamo
// kao cele brojeve, a 0 i #N/A tretiramo kao prazno.
func cleanCell(s string) string {
	s = strings.TrimSpace(strings.TrimPrefix(s, "\ufeff"))
	s = strings.TrimSpace(strings.TrimPrefix(s, "'"))
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "#") {
		return ""
	}
	if numericCell.MatchString(s) {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			if f == 0 {
				return ""
			}
			// Vodeća nula je deo broja (telefon 0171…): ne pretvaraj u int.
			if strings.HasPrefix(s, "0") && len(s) > 1 {
				return s
			}
			if f == math.Trunc(f) && math.Abs(f) < 1e15 {
				return strconv.FormatFloat(f, 'f', -1, 64)
			}
		}
	}
	return s
}

func customerKey(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// addressLookupKeys vraća ključeve za pretragu direktorijuma: ceo unos i deo
// pre prvog zareza. Adrese u zadacima često sadrže grad („Grünewalder Straße
// 11, Volmarstein“), a u Excelu su bez grada — bez ovoga nema pogotka.
func addressLookupKeys(addr string) []string {
	key := customerKey(addr)
	if key == "" {
		return nil
	}
	out := []string{key}
	if i := strings.Index(key, ","); i > 0 {
		if head := strings.TrimSpace(key[:i]); head != "" {
			out = append(out, head)
		}
		if tail := strings.TrimSpace(key[i+1:]); tail != "" {
			out = append(out, tail)
		}
	}
	return out
}

func dedupeRows(rows []customerRow) []customerRow {
	seen := make(map[string]int, len(rows))
	out := make([]customerRow, 0, len(rows))
	for _, r := range rows {
		key := "addr:" + customerKey(r.Address)
		if customerKey(r.Address) == "" {
			key = "nvt:" + customerKey(r.NVT) + "|" + customerKey(r.Kunde)
		}
		if key == "addr:" || key == "nvt:|" {
			out = append(out, r)
			continue
		}
		if i, ok := seen[key]; ok {
			out[i] = r // kasniji red pobeđuje
			continue
		}
		seen[key] = len(out)
		out = append(out, r)
	}
	return out
}

// splitKunde deli „Kunde“ na ime i prezime. Firme (GmbH, Hausverwaltung, WEG…)
// ostaju cele u prezimenu; „Nachname, Vorname“ se poštuje; titule se skidaju.
func splitKunde(s string) (first, last string) {
	s = cleanCell(s)
	if s == "" {
		return "", ""
	}
	words := strings.Fields(strings.ToLower(s))
	for _, w := range words {
		switch w {
		case "gmbh", "mbh", "ohg", "gbr", "hausverwaltung", "immobilien", "verwaltung", "weg":
			return "", s
		}
	}
	for {
		low := strings.ToLower(s)
		trimmed := false
		for _, h := range []string{"herr ", "frau ", "dr. ", "prof. ", "dr ", "prof "} {
			if strings.HasPrefix(low, h) {
				s = strings.TrimSpace(s[len(h):])
				trimmed = true
				break
			}
		}
		if !trimmed {
			break
		}
	}
	if i := strings.Index(s, ","); i > 0 {
		return strings.TrimSpace(s[i+1:]), strings.TrimSpace(s[:i])
	}
	parts := strings.Fields(s)
	if len(parts) <= 1 {
		return "", s
	}
	return strings.Join(parts[:len(parts)-1], " "), parts[len(parts)-1]
}

// --- import ---

func AdminImportHandler(w http.ResponseWriter, r *http.Request) {
	var result *customerImportResult
	if r.Method == http.MethodPost {
		result = importCustomers(r)
	}

	var count int
	if err := db.Get(&count, `SELECT count(*) FROM ha_customers`); err != nil {
		logImportError("count", err)
	}
	var recent []struct {
		ID        int64          `db:"id"`
		Filename  string         `db:"filename"`
		Total     int            `db:"total"`
		Imported  int            `db:"imported"`
		Updated   int            `db:"updated"`
		Unchanged int            `db:"unchanged"`
		Skipped   int            `db:"skipped"`
		Failed    int            `db:"failed"`
		CreatedAt string         `db:"created_at"`
		UserEmail sql.NullString `db:"user_email"`
	}
	if err := db.Select(&recent, `SELECT i.id, i.filename, i.total, i.imported, i.updated,
			i.unchanged, i.skipped, i.failed, to_char(i.created_at,'YYYY-MM-DD HH24:MI') AS created_at,
			u.email AS user_email
		FROM ha_customer_imports i
		LEFT JOIN users u ON u.id = i.imported_by
		ORDER BY i.id DESC LIMIT 10`); err != nil {
		logImportError("recent", err)
	}

	tmpl, err := template.New("admin-import.html").
		Funcs(template.FuncMap(csrfTemplateFuncs(r))).
		ParseFiles("web/templates/admin-import.html")
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = tmpl.Execute(w, map[string]any{
		"IsAdmin":    true,
		"IsOrgAdmin": isOrgAdminRequest(r),
		"UserRole":   userRole(r),
		"Result":     result,
		"Count":      count,
		"Imports":    recent,
	})
}

func logImportError(where string, err error) {
	// Import greške ne smeju da obore stranicu; ostaju u logu.
	if err != nil {
		fmt.Printf("customers %s: %v\n", where, err)
	}
}

func importCustomers(r *http.Request) *customerImportResult {
	res := &customerImportResult{}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		res.addError(0, fmt.Errorf("upload: %w", err))
		return res
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		res.addError(0, errors.New("izaberi fajl"))
		return res
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 64<<20))
	if err != nil {
		res.addError(0, fmt.Errorf("čitanje fajla: %w", err))
		return res
	}
	res.Filename = filepath.Base(hdr.Filename)
	if len(data) == 0 {
		res.addError(0, errors.New("fajl je prazan"))
		return res
	}

	parsed, err := parseCustomerFile(res.Filename, data)
	res.Format, res.HeaderDetected = parsed.Format, parsed.HeaderDetected
	if err != nil {
		res.addError(0, err)
		return res
	}
	res.Rows = len(parsed.Rows)
	if res.Rows == 0 {
		res.addError(0, errors.New("nema prepoznatih redova"))
		return res
	}

	claims := GetAuthClaims(r)
	var userID any
	if claims != nil {
		userID = claims.UserID
	}

	tx, err := db.Beginx()
	if err != nil {
		res.addError(0, err)
		return res
	}
	defer func() { _ = tx.Rollback() }()

	for i, row := range parsed.Rows {
		nvtKey := customerKey(row.NVT)
		addrKey := customerKey(row.Address)
		st, houseNo, plz, city := importAddressParts(row)
		sKey := streetKey(st, houseNo)
		if nvtKey == "" && addrKey == "" {
			res.Skipped++
			continue
		}
		var inserted bool
		if addrKey != "" {
			// Adresa je ključ za spajanje; NVT se samo osvežava.
			err = tx.Get(&inserted, `INSERT INTO ha_customers
					(address, nvt, kunde, phone, mobile,
					 street, house_number, postcode, city, email, street_key,
					 nvt_key, address_key, source, imported_by)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
				ON CONFLICT (address_key) WHERE address_key <> ''
				DO UPDATE SET address=EXCLUDED.address, nvt=EXCLUDED.nvt,
					kunde=EXCLUDED.kunde, phone=EXCLUDED.phone, mobile=EXCLUDED.mobile,
					street=EXCLUDED.street, house_number=EXCLUDED.house_number,
					postcode=EXCLUDED.postcode, city=EXCLUDED.city,
					email=EXCLUDED.email, street_key=EXCLUDED.street_key,
					nvt_key=EXCLUDED.nvt_key, source=EXCLUDED.source,
					imported_by=EXCLUDED.imported_by, updated_at=NOW()
				WHERE ha_customers.address IS DISTINCT FROM EXCLUDED.address
					OR ha_customers.nvt IS DISTINCT FROM EXCLUDED.nvt
					OR ha_customers.kunde IS DISTINCT FROM EXCLUDED.kunde
					OR ha_customers.phone IS DISTINCT FROM EXCLUDED.phone
					OR ha_customers.mobile IS DISTINCT FROM EXCLUDED.mobile
					OR ha_customers.email IS DISTINCT FROM EXCLUDED.email
					OR ha_customers.nvt_key IS DISTINCT FROM EXCLUDED.nvt_key
					OR ha_customers.street_key IS DISTINCT FROM EXCLUDED.street_key
				RETURNING (xmax = 0) AS inserted`,
				row.Address, row.NVT, row.Kunde, row.Phone, row.Mobile,
				st, houseNo, plz, city, row.Email, sKey,
				nvtKey, addrKey, res.Filename, userID)
		} else {
			err = tx.Get(&inserted, `INSERT INTO ha_customers
					(address, nvt, kunde, phone, mobile,
					 street, house_number, postcode, city, email, street_key,
					 nvt_key, address_key, source, imported_by)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
				ON CONFLICT (nvt_key, kunde) WHERE address_key = ''
				DO UPDATE SET phone=EXCLUDED.phone, mobile=EXCLUDED.mobile,
					street=EXCLUDED.street, house_number=EXCLUDED.house_number,
					postcode=EXCLUDED.postcode, city=EXCLUDED.city,
					email=EXCLUDED.email, street_key=EXCLUDED.street_key,
					source=EXCLUDED.source, imported_by=EXCLUDED.imported_by,
					updated_at=NOW()
				WHERE ha_customers.phone IS DISTINCT FROM EXCLUDED.phone
					OR ha_customers.mobile IS DISTINCT FROM EXCLUDED.mobile
					OR ha_customers.email IS DISTINCT FROM EXCLUDED.email
					OR ha_customers.street_key IS DISTINCT FROM EXCLUDED.street_key
				RETURNING (xmax = 0) AS inserted`,
				row.Address, row.NVT, row.Kunde, row.Phone, row.Mobile,
				st, houseNo, plz, city, row.Email, sKey,
				nvtKey, addrKey, res.Filename, userID)
		}
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// DO UPDATE ... WHERE nije pogodio: podaci su isti.
				res.Unchanged++
				continue
			}
			res.addError(i+1, err)
			continue
		}
		if inserted {
			res.Imported++
		} else {
			res.Updated++
		}
	}

	if err := tx.Commit(); err != nil {
		res.addError(0, fmt.Errorf("commit: %w", err))
		return res
	}
	_, _ = db.Exec(`INSERT INTO ha_customer_imports
			(filename, total, imported, updated, unchanged, skipped, failed, imported_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		res.Filename, res.Rows, res.Imported, res.Updated, res.Unchanged, res.Skipped, res.Failed, userID)
	if claims != nil {
		logUserEvent(claims.UserID, 0, "customers-import",
			fmt.Sprintf("%s: rows=%d new=%d updated=%d unchanged=%d failed=%d",
				res.Filename, res.Rows, res.Imported, res.Updated, res.Unchanged, res.Failed))
	}
	return res
}

// --- lookup za HaP formu ---

type lookupMatch struct {
	ID          int64  `json:"id"`
	Address     string `json:"address"`
	Street      string `json:"street"`
	HouseNumber string `json:"house_number"`
	Postcode    string `json:"postcode"`
	City        string `json:"city"`
	NVT         string `json:"nvt"`
	Kunde       string `json:"kunde"`
	Company     string `json:"company"`
	Phone       string `json:"phone"`
	Mobile      string `json:"mobile"`
	Email       string `json:"email,omitempty"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
}

// protocolHistory su podaci iz ranije importovanog protokola (Fremco/Jetting)
// za istu adresu ili NVT — koristi se kad direktorijum nema pogodak.
type protocolHistory struct {
	Address       string  `json:"address"`
	NVT           string  `json:"nvt"`
	ProjectNumber string  `json:"project_number"`
	ProtocolType  string  `json:"protocol_type"`
	ProtocolDate  string  `json:"protocol_date"`
	Lat           float64 `json:"lat,omitempty"`
	Lon           float64 `json:"lon,omitempty"`
}

type lookupResponse struct {
	Found   bool          `json:"found"`
	Source  string        `json:"source,omitempty"`
	Best    *lookupMatch  `json:"best,omitempty"`
	Matches []lookupMatch `json:"matches,omitempty"`
	// Protocol: jedan pogodak iz importovanih protokola (popunjava se sam).
	Protocol *protocolHistory `json:"protocol,omitempty"`
	// Protocols: više adresa za isti NVT — forma prikazuje listu.
	Protocols []protocolHistory `json:"protocols,omitempty"`
}

const customerSelect = `SELECT id, address, nvt, kunde, phone, mobile,
		street, house_number, postcode, city, coalesce(email,'') AS email
		FROM ha_customers`

// nvtKeyOf normalizuje NVT za poređenje: mala slova, bez razmaka
// („1 V 2200“ i „1V2200“ su isti priključak).
func nvtKeyOf(s string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
}

// hasPlace javlja da li red ima PLZ ili grad. Excel uvoz ih zasad ne
// popunjava, pa se poređenje mesta preskače dok se direktorijum ne dopuni.
func hasPlace(r customerRow) bool {
	return strings.TrimSpace(r.Postcode) != "" || strings.TrimSpace(r.City) != ""
}

// rowMatchesPlace poredi red sa upisanim mestom: PLZ je jača od grada, a
// grad se poredi tolerantno („Wetter“ = „Wetter (Ruhr)“).
func rowMatchesPlace(r customerRow, postcode, city string) bool {
	if postcode != "" {
		if p := strings.TrimSpace(r.Postcode); p != "" {
			return p == postcode
		}
	}
	if city != "" {
		if c := strings.TrimSpace(r.City); c != "" {
			return sameCity(c, city)
		}
	}
	return true
}

// rowsSharePlace javlja da li su svi kandidati u istom mestu (ili mesto
// nije poznato u direktorijumu).
func rowsSharePlace(rows []customerRow) bool {
	if len(rows) < 2 {
		return true
	}
	first := rows[0]
	for _, r := range rows[1:] {
		if p, q := strings.TrimSpace(first.Postcode), strings.TrimSpace(r.Postcode); p != "" && q != "" && p != q {
			return false
		}
		if c, d := strings.TrimSpace(first.City), strings.TrimSpace(r.City); c != "" && d != "" && !sameCity(c, d) {
			return false
		}
	}
	return true
}

// narrowCustomerRows sužava kandidate sa iste ulice i broja. Redosled je
// namerno NVT → mesto → svi: NVT je jedini pouzdan razdvajač kada dve
// opštine imaju istu ulicu i broj, jer uvoz iz Excela ne nosi PLZ ni grad.
//
// Drugi rezultat je „pouzdano“: false znači da forma ne sme ništa da
// popuni, nego treba da ponudi izbor — bolje prazno nego pogrešan klijent.
func narrowCustomerRows(rows []customerRow, nvt, postcode, city string) ([]customerRow, bool) {
	if len(rows) == 0 {
		return nil, false
	}
	if k := nvtKeyOf(nvt); k != "" {
		var hit []customerRow
		for _, r := range rows {
			if nvtKeyOf(r.NVT) == k {
				hit = append(hit, r)
			}
		}
		if len(hit) > 0 {
			return hit, len(hit) == 1
		}
	}
	if postcode != "" || city != "" {
		var hit []customerRow
		known := false
		for _, r := range rows {
			if !hasPlace(r) {
				continue
			}
			known = true
			if rowMatchesPlace(r, postcode, city) {
				hit = append(hit, r)
			}
		}
		if len(hit) > 0 {
			return hit, len(hit) == 1
		}
		if known {
			// Upisano mesto se ne poklapa ni sa jednim kandidatom — vrati sve
			// kandidate da ih radnik vidi i potvrdi, umesto da se pogodi red.
			return rows, false
		}
		// Direktorijum ne zna mesta — nastavi sa svim kandidatima.
	}
	return rows, len(rows) == 1
}

// findCustomers traži pogotke: ulica+broj (+NVT, +mesto), tačna adresa, NVT
// (grupa adresa), pa prefiks i sadržaj adrese.
func findCustomers(nvt, addr string) []customerRow {
	keys := addressLookupKeys(addr)
	st, houseNo, postcode, city := parseAddress(addr)
	sk := streetKey(st, houseNo)

	// 1) ulica+broj, pa NVT, pa mesto — najprecizniji pogodak. Kandidati se
	// sužavaju u Go-u da bi grad mogao tolerantno da se poredi.
	if sk != "" {
		var rows []customerRow
		if err := db.Select(&rows, customerSelect+`
			WHERE street_key = $1 LIMIT 5`, sk); err == nil && len(rows) > 0 {
			if narrowed, _ := narrowCustomerRows(rows, nvt, postcode, city); len(narrowed) > 0 {
				return narrowed
			}
		}
	}
	// 2) tačna adresa (ceo string / pre / posle zareza)
	for _, k := range keys {
		var rows []customerRow
		if err := db.Select(&rows, customerSelect+`
			WHERE address_key = $1 LIMIT 1`, k); err == nil && len(rows) > 0 {
			return rows
		}
	}
	// 3) NVT — grupa adresa
	if k := nvtKeyOf(nvt); k != "" {
		var rows []customerRow
		if err := db.Select(&rows, customerSelect+`
			WHERE lower(replace(coalesce(nvt_key,''), ' ', '')) = $1
			ORDER BY address_key LIMIT 25`, k); err == nil && len(rows) > 0 {
			return rows
		}
	}
	for _, k := range keys {
		if len(k) < 3 {
			continue
		}
		var rows []customerRow
		if err := db.Select(&rows, customerSelect+`
			WHERE address_key LIKE $1 || '%'
			ORDER BY length(address_key), address_key LIMIT 5`, k); err == nil && len(rows) > 0 {
			return rows
		}
	}
	// Poslednji pokušaj: adresa u direktorijumu sadrži traženi deo (dodatne
	// reči, „bei …“ i sl.).
	for _, k := range keys {
		if len(k) < 6 {
			continue
		}
		var rows []customerRow
		if err := db.Select(&rows, customerSelect+`
			WHERE address_key LIKE '%' || $1 || '%'
			ORDER BY length(address_key), address_key LIMIT 5`, k); err == nil && len(rows) > 0 {
			return rows
		}
	}
	return nil
}

// customerForPrefill vraća klijenta za server-side popunjavanje forme:
// tačna adresa, ili NVT samo kada je jedinstven (NVT je obično grupa adresa).
func customerForPrefill(nvt, address string) (customerRow, bool) {
	st, houseNo, postcode, city := parseAddress(address)
	if sk := streetKey(st, houseNo); sk != "" {
		var rows []customerRow
		if err := db.Select(&rows, customerSelect+`
			WHERE street_key = $1 LIMIT 5`, sk); err == nil && len(rows) > 0 {
			if hit, ok := narrowCustomerRows(rows, nvt, postcode, city); ok {
				return hit[0], true
			} else if len(hit) > 0 {
				// Više kandidata (ista ulica u drugom mestu) — ne pogađaj.
				return customerRow{}, false
			}
		}
	}
	for _, a := range addressLookupKeys(address) {
		var row customerRow
		if err := db.Get(&row, customerSelect+`
			WHERE address_key = $1 LIMIT 1`, a); err == nil {
			return row, true
		}
	}
	if n := customerKey(nvt); n != "" {
		var rows []customerRow
		if err := db.Select(&rows, customerSelect+`
			WHERE nvt_key = $1 LIMIT 2`, n); err == nil && len(rows) == 1 {
			return rows[0], true
		}
	}
	return customerRow{}, false
}

// protocolHistoryRow je jedan kandidat iz importovanih protokola.
type protocolHistoryRow struct {
	ID            int64   `db:"id"`
	Address       string  `db:"address"`
	ProjectNumber string  `db:"project_number"`
	ProtocolType  string  `db:"protocol_type"`
	ProtocolDate  string  `db:"protocol_date"`
	SectionNVT    string  `db:"section_nvt"`
	Lat           float64 `db:"lat"`
	Lon           float64 `db:"lon"`
}

const protocolHistorySelect = `SELECT p.id,
		coalesce(p.address,'') AS address,
		coalesce(p.project_number,'') AS project_number,
		coalesce(p.protocol_type,'') AS protocol_type,
		coalesce(to_char(p.protocol_date,'YYYY-MM-DD'),'') AS protocol_date,
		coalesce(p.section_nvt,'') AS section_nvt,
		coalesce(s.gps_latitude, 0) AS lat,
		coalesce(s.gps_longitude, 0) AS lon
	FROM protocols p
	LEFT JOIN protocol_summary s ON s.protocol_id = p.id`

// Uslovi pretrage nad importovanim protokolima. NVT se poredi bez razmaka
// („1V3400“, „1 V 3400“, „Holsteiner Weide 13 / 1V3400“), adresa po
// normalizovanom ključu ulice+broja.
const (
	protocolNVTExpr     = `lower(replace(coalesce(p.section_nvt,''), ' ', ''))`
	protocolAddressExpr = `regexp_replace(lower(coalesce(p.address,'')), '[^a-z0-9äöüß]', '', 'g')`
)

// protocolCandidates vraća protokole za zadati uslov (uslov je literal iz
// ovog fajla, vrednosti idu kao parametri), najnovije prvo.
func protocolCandidates(limit int, where string, args ...any) []protocolHistoryRow {
	var rows []protocolHistoryRow
	q := fmt.Sprintf(`%s
		WHERE %s
		ORDER BY p.protocol_date DESC NULLS LAST, p.id DESC
		LIMIT %d`, protocolHistorySelect, where, limit)
	if err := db.Select(&rows, q, args...); err != nil {
		return nil
	}
	return rows
}

func protocolHistoryOf(row protocolHistoryRow, typedNVT string) protocolHistory {
	p := protocolHistory{
		Address:       row.Address,
		NVT:           nvtFromSection(row.SectionNVT),
		ProjectNumber: row.ProjectNumber,
		ProtocolType:  row.ProtocolType,
		ProtocolDate:  row.ProtocolDate,
	}
	if row.Lat != 0 || row.Lon != 0 {
		p.Lat, p.Lon = row.Lat, row.Lon
	}
	if p.NVT == "" {
		p.NVT = strings.TrimSpace(typedNVT)
	}
	return p
}

// protocolHistories pretvara redove u listu za formu: jedna stavka po
// adresi (najnoviji protokol), poređano po adresi. Jedan NVT ume da pokrije
// dvadesetak adresa, pa se u formi prikazuje lista, a ne jedan pogodak.
func protocolHistories(rows []protocolHistoryRow, typedNVT string) []protocolHistory {
	seen := make(map[string]bool, len(rows))
	out := make([]protocolHistory, 0, len(rows))
	for _, row := range rows {
		key := addressMatchKey(row.Address)
		if key == "" {
			key = customerKey(row.Address)
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, protocolHistoryOf(row, typedNVT))
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Address) < strings.ToLower(out[j].Address)
	})
	return out
}

// findProtocolHistory traži ranije importovane protokole (Fremco/Jetting):
// prvo NVT i adresa zajedno, pa sve adrese pod tim NVT-om, pa sama adresa.
// Vraća praznu listu kada nema pogotka; više stavki znači „korisnik bira“ —
// pogrešan projekat je gori od praznog polja.
func findProtocolHistory(nvt, address string) []protocolHistory {
	nvtK := nvtKeyOf(nvt)
	slug := addressMatchKey(address)

	// 1) NVT i adresa — tačan pogodak. Više protokola za istu adresu
	// (različiti datumi) svodi se na najnoviji.
	if nvtK != "" && slug != "" {
		rows := protocolCandidates(1,
			protocolNVTExpr+` LIKE '%'||$1||'%' AND `+protocolAddressExpr+` = $2`, nvtK, slug)
		if len(rows) > 0 {
			return []protocolHistory{protocolHistoryOf(rows[0], nvt)}
		}
	}
	// 2) samo NVT — sve adrese iz importovanih protokola pod tim NVT-om.
	if nvtK != "" {
		rows := protocolCandidates(25, protocolNVTExpr+` LIKE '%'||$1||'%'`, nvtK)
		if len(rows) > 0 {
			return protocolHistories(rows, nvt)
		}
	}
	// 3) samo adresa.
	if slug == "" {
		return nil
	}
	rows := protocolCandidates(5, protocolAddressExpr+` = $1`, slug)
	switch {
	case len(rows) == 0:
		return nil
	case len(rows) == 1:
		return []protocolHistory{protocolHistoryOf(rows[0], nvt)}
	}
	if row, ok := pickProtocolByPlace(rows, address); ok {
		return []protocolHistory{protocolHistoryOf(row, nvt)}
	}
	return protocolHistories(rows, nvt)
}

// pickProtocolByPlace bira protokol čije GPS mesto odgovara upisanom
// PLZ/Ort-u. Koristi se samo kada ista ulica+broj postoji više puta, pa su
// koordinate jedini pouzdan razdvajač; odgovori su keširani.
func pickProtocolByPlace(rows []protocolHistoryRow, address string) (protocolHistoryRow, bool) {
	_, _, postcode, city := parseAddress(address)
	if postcode == "" && city == "" {
		return protocolHistoryRow{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	tried := 0
	for _, row := range rows {
		if row.Lat == 0 && row.Lon == 0 {
			continue
		}
		if tried >= 3 {
			break
		}
		tried++
		place := placeByCoords(ctx, row.Lat, row.Lon)
		if place.Ambiguous {
			continue
		}
		okPost := postcode == "" || place.Postcode == "" || place.Postcode == postcode
		okCity := city == "" || place.City == "" || sameCity(place.City, city)
		if okPost && okCity {
			return row, true
		}
	}
	return protocolHistoryRow{}, false
}

// linkMatchingProtocols povezuje zadatak sa importovanim protokolima
// (Fremco/Jetting) koji imaju istu adresu (ulica+broj) i NVT. Vraća broj
// novih veza; ne pravi grešku ako nema pogodaka.
func linkMatchingProtocols(taskID int64, address, nvt string) int {
	slug := addressMatchKey(address)
	nvtKey := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(nvt), " ", ""))
	if taskID <= 0 || slug == "" || nvtKey == "" {
		return 0
	}
	var ids []int64
	if err := db.Select(&ids, `SELECT id FROM protocols
		WHERE regexp_replace(lower(coalesce(address,'')), '[^a-z0-9äöüß]', '', 'g') = $1
		  AND lower(replace(coalesce(section_nvt,''), ' ', '')) LIKE '%' || $2 || '%'
		ORDER BY protocol_date DESC NULLS LAST, id DESC
		LIMIT 10`, slug, nvtKey); err != nil {
		log.Printf("task %d link protocols: %v", taskID, err)
		return 0
	}
	linked := 0
	for _, pid := range ids {
		res, err := db.Exec(`INSERT INTO task_protocols (task_id, protocol_id)
			VALUES ($1,$2) ON CONFLICT DO NOTHING`, taskID, pid)
		if err != nil {
			log.Printf("task %d link protocol %d: %v", taskID, pid, err)
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			linked++
		}
	}
	if linked > 0 {
		log.Printf("task %d linked to %d imported protocol(s)", taskID, linked)
	}
	return linked
}

// linkTasksToProtocol veže postojeće zadatke koji imaju istu ulicu+broj i
// NVT kao novi protokol (obrnuti smer od linkMatchingProtocols).
func linkTasksToProtocol(protocolID int, address, nvt string) int {
	slug := addressMatchKey(address)
	nvtKey := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(nvt), " ", ""))
	if protocolID <= 0 || slug == "" || nvtKey == "" {
		return 0
	}
	var ids []int64
	if err := db.Select(&ids, `SELECT t.id FROM tasks t
		WHERE coalesce(t.nvt,'') <> ''
		  AND regexp_replace(lower(coalesce(t.street,'') || coalesce(t.house_number,'')), '[^a-z0-9äöüß]', '', 'g') = $1
		  AND lower(replace(t.nvt, ' ', '')) LIKE '%' || $2 || '%'
		ORDER BY t.id DESC
		LIMIT 10`, slug, nvtKey); err != nil {
		log.Printf("protocol %d link tasks: %v", protocolID, err)
		return 0
	}
	linked := 0
	for _, tid := range ids {
		res, err := db.Exec(`INSERT INTO task_protocols (task_id, protocol_id)
			VALUES ($1,$2) ON CONFLICT DO NOTHING`, tid, protocolID)
		if err != nil {
			log.Printf("protocol %d link task %d: %v", protocolID, tid, err)
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			linked++
		}
	}
	if linked > 0 {
		log.Printf("protocol %d linked to %d task(s)", protocolID, linked)
	}
	return linked
}

// HALookupHandler vraća najbolji pogodak iz direktorijuma za zadati NVT ili
// adresu; HaP forma njime popunjava prazna polja klijenta.
func HALookupHandler(w http.ResponseWriter, r *http.Request) {
	if GetAuthClaims(r) == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	nvt := customerKey(r.URL.Query().Get("nvt"))
	addr := customerKey(r.URL.Query().Get("address"))
	resp := lookupResponse{}
	if nvt == "" && len(addr) < 3 {
		writeJSON(w, resp)
		return
	}

	rows := findCustomers(nvt, addr)
	if len(rows) == 0 {
		// Nema u direktorijumu — probaj ranije importovane protokole
		// (Fremco/Jetting): nose projektni broj i NVT za istu adresu.
		switch hist := findProtocolHistory(nvt, addr); len(hist) {
		case 0:
			log.Printf("ha lookup: no match nvt=%q address=%q", nvt, addr)
			writeJSON(w, resp)
		case 1:
			p := hist[0]
			writeJSON(w, lookupResponse{Found: true, Source: "protocol", Protocol: &p})
		default:
			writeJSON(w, lookupResponse{Found: true, Source: "protocol", Protocols: hist})
		}
		return
	}

	matches := make([]lookupMatch, 0, len(rows))
	for _, row := range rows {
		// last_name ostaje kompatibilan sa HaP formom (firma ide u prezime),
		// a company je odvojeno polje za zadatke.
		first, last := splitKunde(row.Kunde)
		_, _, company := customerNameParts(row.Kunde)
		matches = append(matches, lookupMatch{
			ID: row.ID, Address: row.Address, NVT: row.NVT, Kunde: row.Kunde,
			Street: row.Street, HouseNumber: row.HouseNumber,
			Postcode: row.Postcode, City: row.City,
			Company: company,
			Phone:   row.Phone, Mobile: row.Mobile, Email: row.Email,
			FirstName: first, LastName: last,
		})
	}
	resp.Found = true
	resp.Best = &matches[0]
	resp.Matches = matches
	writeJSON(w, resp)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
