package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"
)

// Više ONT uređaja po HaP-u. Svaki ONT ima SN i opcionu fotografiju
// nalepnice. Stari fiksni slot `ont_label` migrira se u prvi ONT.
const maxONTsPerHA = 20

func ensureONTSchema(db *sqlx.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS ha_onts (
			id           SERIAL PRIMARY KEY,
			ha_id        INTEGER NOT NULL REFERENCES ha_protocols(id) ON DELETE CASCADE,
			position     INTEGER NOT NULL DEFAULT 1,
			sn           TEXT NOT NULL DEFAULT '',
			object_key   TEXT NOT NULL DEFAULT '',
			content_type TEXT NOT NULL DEFAULT 'image/jpeg',
			bytes        INTEGER NOT NULL DEFAULT 0,
			codes        TEXT NOT NULL DEFAULT '',
			created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at   TIMESTAMPTZ
		)`,
		`ALTER TABLE ha_onts ADD COLUMN IF NOT EXISTS codes TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS idx_ha_onts_ha ON ha_onts(ha_id)`,
		// Stare ONT nalepnice (jedan fiksni slot u ha_photos) postaju prvi ONT.
		`INSERT INTO ha_onts (ha_id, position, sn, object_key, content_type, bytes)
		 SELECT p.ha_id, 1, '', p.object_key, p.content_type, p.bytes
		 FROM ha_photos p
		 WHERE p.kind = 'ont_label'
		   AND NOT EXISTS (SELECT 1 FROM ha_onts o WHERE o.object_key = p.object_key)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("ont schema: %w", err)
		}
	}
	return nil
}

type ontRow struct {
	ID          int64     `db:"id"`
	HAID        int64     `db:"ha_id"`
	Position    int       `db:"position"`
	SN          string    `db:"sn"`
	ObjectKey   string    `db:"object_key"`
	ContentType string    `db:"content_type"`
	Bytes       int       `db:"bytes"`
	Codes       string    `db:"codes"`
	CodeList    []ontCode `db:"-"`
}

func listONTs(haID int64) ([]ontRow, error) {
	var out []ontRow
	err := db.Select(&out, `SELECT id, ha_id, position, sn, object_key, content_type, bytes, codes
		FROM ha_onts WHERE ha_id = $1 ORDER BY position, id`, haID)
	for i := range out {
		out[i].CodeList = parseONTCodesJSON(out[i].Codes)
	}
	return out, err
}

func loadONT(id int64) (ontRow, error) {
	var o ontRow
	err := db.Get(&o, `SELECT id, ha_id, position, sn, object_key, content_type, bytes, codes
		FROM ha_onts WHERE id = $1`, id)
	o.CodeList = parseONTCodesJSON(o.Codes)
	return o, err
}

// saveONTs obrađuje ont_id_N / ont_sn_N / ont_photo_N polja. Red koji nije
// poslat smatra se obrisanim; njegova slika se briše iz Object Storage-a.
func saveONTs(r *http.Request, haID int64) (saved, failed int) {
	if r.MultipartForm == nil {
		return 0, 0
	}
	existing, _ := listONTs(haID)
	byID := make(map[int64]ontRow, len(existing))
	for _, o := range existing {
		byID[o.ID] = o
	}

	var indices []int
	for key := range r.MultipartForm.Value {
		if !strings.HasPrefix(key, "ont_id_") {
			continue
		}
		if v, err := strconv.Atoi(strings.TrimPrefix(key, "ont_id_")); err == nil && v >= 0 {
			indices = append(indices, v)
		}
	}
	sort.Ints(indices)

	kept := make(map[int64]bool, len(existing))
	position := 0
	for _, idx := range indices {
		if position >= maxONTsPerHA {
			failed++
			continue
		}
		id, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue(fmt.Sprintf("ont_id_%d", idx))), 10, 64)
		sn := strings.TrimSpace(r.FormValue(fmt.Sprintf("ont_sn_%d", idx)))
		file, hdr, err := r.FormFile(fmt.Sprintf("ont_photo_%d", idx))
		hasFile := err == nil
		if id <= 0 && sn == "" && !hasFile {
			continue
		}
		position++

		var raw []byte
		if hasFile {
			raw, err = io.ReadAll(io.LimitReader(file, 25<<20))
			_ = file.Close()
			if err != nil {
				log.Printf("ont read %d: %v", idx, err)
				failed++
				hasFile = false
			} else if len(raw) == 0 {
				hasFile = false
			}
		}

		codes := parseONTCodesJSON(r.FormValue(fmt.Sprintf("ont_codes_%d", idx)))
		if len(codes) == 0 && hasFile {
			if decoded, derr := decodeONTImage(raw); derr == nil && len(decoded) > 0 {
				codes = decoded
			}
		}
		if sn == "" {
			sn = pickONTSN(codes)
		}
		codesJSON := encodeONTCodesJSON(codes)

		if id > 0 {
			old, ok := byID[id]
			if !ok {
				failed++
				continue
			}
			// Greška pri čuvanju ne sme da obriše postojeći red.
			kept[id] = true
			key, ct, size := old.ObjectKey, old.ContentType, old.Bytes
			if hasFile {
				k, c, n, err := uploadONTData(r.Context(), haID, idx, raw, hdr.Filename)
				if err != nil {
					log.Printf("ont photo %d: %v", idx, err)
					failed++
				} else {
					if old.ObjectKey != "" {
						if err := sourceStore.DeleteRaw(r.Context(), old.ObjectKey); err != nil {
							log.Printf("ont replace object %s: %v", old.ObjectKey, err)
						}
					}
					key, ct, size = k, c, n
				}
			}
			if _, err := db.Exec(`UPDATE ha_onts SET position=$1, sn=$2, object_key=$3,
					content_type=$4, bytes=$5, codes=$6, updated_at=NOW()
				WHERE id=$7 AND ha_id=$8`, position, sn, key, ct, size, codesJSON, id, haID); err != nil {
				log.Printf("ont update %d: %v", id, err)
				failed++
				continue
			}
			saved++
			continue
		}

		var key, ct string
		var size int
		if hasFile {
			k, c, n, err := uploadONTData(r.Context(), haID, idx, raw, hdr.Filename)
			if err != nil {
				log.Printf("ont photo %d: %v", idx, err)
				failed++
			} else {
				key, ct, size = k, c, n
			}
		}
		if _, err := db.Exec(`INSERT INTO ha_onts (ha_id, position, sn, object_key, content_type, bytes, codes)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`, haID, position, sn, key, ct, size, codesJSON); err != nil {
			log.Printf("ont insert: %v", err)
			failed++
			continue
		}
		saved++
	}

	for _, o := range existing {
		if kept[o.ID] {
			continue
		}
		if o.ObjectKey != "" {
			if err := sourceStore.DeleteRaw(r.Context(), o.ObjectKey); err != nil {
				log.Printf("ont delete object %s: %v", o.ObjectKey, err)
			}
		}
		if _, err := db.Exec(`DELETE FROM ha_onts WHERE id=$1 AND ha_id=$2`, o.ID, haID); err != nil {
			log.Printf("ont delete %d: %v", o.ID, err)
		}
	}
	return saved, failed
}

func uploadONTData(ctx context.Context, haID int64, idx int, data []byte, filename string) (key, ct string, size int, err error) {
	if len(data) == 0 {
		return "", "", 0, errors.New("empty image")
	}
	ext := strings.ToLower(path.Ext(filename))
	if ext != ".jpg" && ext != ".jpeg" && ext != ".png" {
		ext = ".jpg"
	}
	key = haPhotoKey(haID, fmt.Sprintf("ont-%d", idx), ext)
	ct = contentTypeOf(ext)
	if err := sourceStore.PutRaw(ctx, key, data, ct); err != nil {
		return "", "", 0, err
	}
	return key, ct, len(data), nil
}

// HANTScanHandler dekodira barkodove sa fotografije ONT nalepnice i vraća
// sve pronađene vrednosti + predlog SN-a.
func HANTScanHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "Upload failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Missing file", http.StatusBadRequest)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 32<<20))
	if err != nil || len(data) == 0 {
		http.Error(w, "Empty file", http.StatusBadRequest)
		return
	}
	codes, err := decodeONTImage(data)
	if err != nil {
		writeJSON(w, map[string]any{"error": "Could not decode image: " + err.Error()})
		return
	}
	info := classifyONTCodes(codes)
	writeJSON(w, map[string]any{
		"codes": codes,
		"sn":    info.SN,
		"mac":   info.MAC,
		"pn":    info.PN,
	})
}

// ontAnnexLines priprema tekst za aneks: SN, MAC, P/N i ostale kodove.
func ontAnnexLines(o ontRow) []string {
	info := classifyONTCodes(o.CodeList)
	var lines []string
	add := func(s string) {
		if s != "" {
			lines = append(lines, s)
		}
	}
	if o.SN != "" {
		add("SN: " + o.SN)
	}
	if info.MAC != "" && info.MAC != o.SN {
		add("MAC: " + info.MAC)
	}
	if info.PN != "" && info.PN != o.SN {
		add("P/N: " + info.PN)
	}
	for _, c := range o.CodeList {
		if c.Value == "" || c.Value == o.SN || c.Value == info.MAC || c.Value == info.PN {
			continue
		}
		add(c.Value)
	}
	if len(lines) == 0 {
		lines = append(lines, "—")
	}
	if len(lines) > 7 {
		lines = lines[:7]
	}
	return lines
}

// HANTOPhotoHandler servira fotografiju ONT nalepnice uz proveru vidljivosti.
func HANTOPhotoHandler(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	o, err := loadONT(id)
	if err != nil {
		http.Error(w, "Photo not found", http.StatusNotFound)
		return
	}
	h, err := loadHA(o.HAID)
	if err != nil || !canSeeHA(r, h) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	data, ct, err := sourceStore.GetRaw(r.Context(), o.ObjectKey)
	if err != nil {
		http.Error(w, "Photo unavailable", http.StatusBadGateway)
		return
	}
	if ct == "" {
		ct = o.ContentType
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write(data)
}
