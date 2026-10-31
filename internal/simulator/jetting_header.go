package simulator

import (
	"regexp"
	"strings"
)

// JettingHeader su polja zaglavlja koja se u „Jettlogger / Einblas-Protokoll"
// layout-u nalaze na prvoj strani. Ta strana je često **raster slika** (text
// layer sadrži samo naziv firme), pa se čita OCR-om.
type JettingHeader struct {
	ProjectNumber string
	SectionNVT    string
	Company       string
	Operator      string
	Remarks       string
	Date          string
	StartTime     string
}

var (
	jettingBauvorhabenRe = regexp.MustCompile(`(?im)^[ \t]*Bauvorhaben[ \t]*(?:Nr\.?)?[ \t]*:?[ \t]*(.+)$`)
	jettingAbschnittRe   = regexp.MustCompile(`(?im)^[ \t]*Streckenabschnitt[^\n]*?NVt[ \t]*:?[ \t]*(.+)$`)
	jettingFirmaRe       = regexp.MustCompile(`(?im)^[ \t]*Firma[ \t]*:?[ \t]*(.+)$`)
	jettingEinblaserRe   = regexp.MustCompile(`(?im)^[ \t]*Einbl[äa]ser[ \t]*:?[ \t]*(.+)$`)
	jettingBemerkungenRe = regexp.MustCompile(`(?im)^[ \t]*Bemerkungen[ \t]*:?[ \t]*(.*)$`)
	// „29.10.2025, 16:26" — Datum + Startzeit (OCR može da ubaci razmak posle zareza).
	jettingDatumRe = regexp.MustCompile(`(\d{1,2}\.\d{1,2}\.\d{4})\s*[,;]?\s*(\d{1,2}:\d{2})`)
)

// extractJettingHeader čita zaglavlje iz (OCR) teksta. Vrednosti su bukvalno
// ono što u dokumentu piše pod tim imenom — semantiku (izvođač vs podizvođač)
// rešava sloj iznad.
func extractJettingHeader(text string) JettingHeader {
	var h JettingHeader

	if m := jettingBauvorhabenRe.FindStringSubmatch(text); len(m) > 1 {
		if v := cleanFieldValue(m[1]); looksLikeProjectNumber(v) {
			h.ProjectNumber = v
		}
	}
	if m := jettingAbschnittRe.FindStringSubmatch(text); len(m) > 1 {
		h.SectionNVT = cleanFieldValue(m[1])
	}
	if m := jettingFirmaRe.FindStringSubmatch(text); len(m) > 1 {
		h.Company = cleanOrgName(m[1])
	}
	if m := jettingEinblaserRe.FindStringSubmatch(text); len(m) > 1 {
		h.Operator = cleanOperator(m[1])
	}
	if m := jettingBemerkungenRe.FindStringSubmatch(text); len(m) > 1 {
		h.Remarks = cleanFieldValue(m[1])
	}
	if m := jettingDatumRe.FindStringSubmatch(text); len(m) > 2 {
		h.Date = m[1]
		h.StartTime = cleanFieldValue(m[2])
	}
	return h
}

// cleanFieldValue uklanja višak razmaka i interpunkciju na ivicama i odbacuje
// očigledno predugačke vrednosti (OCR šum iz susednih kolona).
func cleanFieldValue(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.Trim(s, ":;,|/\\-–—")
	if len(s) > 120 {
		return ""
	}
	return s
}

// looksLikeProjectNumber prihvata npr. SM209214964, a odbacuje OCR ostatke
// kao što je „Nr." ili sam broj stranice.
func looksLikeProjectNumber(s string) bool {
	if len(s) < 5 || len(s) > 40 {
		return false
	}
	digits := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits++
		}
	}
	return digits >= 4
}
