package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

// APL kodovi se čitaju iz fotografije „Bild 3 — APL geschlossen / Aufkleber“.
// Na APL-u može biti više nalepnica (npr. westconnect W006PF5Z, W006PF4W,
// W006PF3R, W006PF2M), pa se čuvaju kao lista, a ne kao jedan broj.

func parseAPLCodesJSON(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return normalizeAPLCodes(out)
}

func encodeAPLCodesJSON(codes []string) string {
	codes = normalizeAPLCodes(codes)
	if len(codes) == 0 {
		return ""
	}
	b, err := json.Marshal(codes)
	if err != nil {
		return ""
	}
	return string(b)
}

func normalizeAPLCodes(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.ToUpper(strings.TrimSpace(v))
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
		if len(out) >= 100 {
			break
		}
	}
	return out
}

func decodeAPLCodes(data []byte) []string {
	codes, err := decodeONTImage(data)
	if err != nil {
		return nil
	}
	values := make([]string, 0, len(codes))
	for _, c := range codes {
		values = append(values, c.Value)
	}
	return normalizeAPLCodes(values)
}

func storedBild3Data(r *http.Request, haID int64) []byte {
	var key string
	if err := db.Get(&key, `SELECT object_key FROM ha_photos
		WHERE ha_id = $1 AND kind = 'bild3' LIMIT 1`, haID); err != nil || key == "" {
		return nil
	}
	data, _, err := sourceStore.GetRaw(r.Context(), key)
	if err != nil {
		return nil
	}
	return data
}

// resolveAPLCodes: ako je korisnik menjao listu (touched=1), veruje se formi;
// ako je poslata nova Bild 3 slika, kodovi se dopunjuju dekodiranjem; ako
// kodovi već postoje, sačuvana slika se ne dekodira ponovo (skupo je i troši
// memoriju). Stara HaP-a bez kodova dobija backfill iz sačuvane slike.
func resolveAPLCodes(r *http.Request, bild3 []byte, haID int64) []string {
	codes := parseAPLCodesJSON(r.FormValue("apl_codes"))
	if r.FormValue("apl_codes_touched") == "1" {
		return codes
	}
	if len(bild3) > 0 {
		return normalizeAPLCodes(append(codes, decodeAPLCodes(bild3)...))
	}
	if len(codes) > 0 {
		return codes
	}
	if data := storedBild3Data(r, haID); len(data) > 0 {
		codes = append(codes, decodeAPLCodes(data)...)
	}
	return normalizeAPLCodes(codes)
}
