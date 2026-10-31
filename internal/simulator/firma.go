package simulator

import (
	"regexp"
	"strings"
)

// FirmaInfo je sadržaj „Firma" bloka u zaglavlju protokola.
//
// `pdftotext -layout` daje blok u dve linije:
//
//	Firma                Wolken-ASM    GmbH
//	                     M.A.X. Bauservice GmbH        Einbläser: Marko
//
// Prva firma je izvođač radova (Company), druga je podizvođač
// (ServiceProvider), a „Einbläser" je rukovalac (Operator). Ranije su
// parseri gledali svaku liniju zasebno, pa je `company` ostajao razlomljen
// („Wolken-ASM  GmbHGmbH"), a `service_provider` je bio hardkodovan.
type FirmaInfo struct {
	Company         string
	ServiceProvider string
	Operator        string
}

var (
	firmaLabelRe = regexp.MustCompile(`(?i)^\s*Firma\b`)
	// Oznake polja koja prekidaju višelinijski blok (Firma, Bemerkungen, ...).
	fieldLabelRe = regexp.MustCompile(`(?i)^\s*(Firma|Bemerkungen|Bauvorhaben|Streckenabschnitt|Datum|Adresse|Auftrag|Kunde|Objekt|Rohrparameter|Kabel|Einblasgerät|Hersteller|Rohr|Zusammenfassung|Ort|Projekt|Anlage|Messwerte|Nr\.)\b`)
	einblaserRe  = regexp.MustCompile(`(?i)Einbl(?:ä|ae)ser\s*:\s*([^,;/]+)`)
	// Pravne forme — koriste se da se prepozna gde se završava naziv firme.
	orgSuffixRe = regexp.MustCompile(`(?i)\b(GmbH|KG|AG|UG|GbR|OHG|SE|e\.\s?K\.|Inc\.?|Ltd\.?|SARL|S\.\s?A\.)\b`)
	wolkenRe    = regexp.MustCompile(`(?i)wolken[\s\-]*asm`)
	// Više razmaka u -layout izlazu razdvaja kolone.
	columnGapRe = regexp.MustCompile(`\s{3,}`)
)

// extractFirmaInfo pronalazi „Firma" blok i vraća očišćene vrednosti.
// Prazan FirmaInfo znači da blok nije nađen (npr. stariji layout).
func extractFirmaInfo(lines []string) FirmaInfo {
	for i, raw := range lines {
		loc := firmaLabelRe.FindStringIndex(raw)
		if loc == nil {
			continue
		}
		chunks := []string{raw[loc[1]:]}
		for j := i + 1; j < len(lines) && j <= i+3; j++ {
			next := lines[j]
			if strings.TrimSpace(next) == "" || fieldLabelRe.MatchString(next) {
				break
			}
			// Nastavak je uvučen (-layout) ili očigledno naziv firme.
			if startsWithSpace(next) || orgSuffixRe.MatchString(next) || wolkenRe.MatchString(next) {
				chunks = append(chunks, next)
				continue
			}
			break
		}
		if info := classifyFirmaChunks(chunks); info.Company != "" || info.ServiceProvider != "" || info.Operator != "" {
			return info
		}
	}
	return FirmaInfo{}
}

func startsWithSpace(s string) bool {
	return len(s) > 0 && (s[0] == ' ' || s[0] == '\t')
}

// classifyFirmaChunks deli blok na nazive firmi i rukovaoca.
func classifyFirmaChunks(chunks []string) FirmaInfo {
	var out FirmaInfo
	for _, chunk := range chunks {
		if m := einblaserRe.FindStringSubmatch(chunk); len(m) > 1 && out.Operator == "" {
			out.Operator = cleanOperator(m[1])
			if idx := strings.Index(chunk, m[0]); idx > 0 {
				chunk = chunk[:idx]
			} else {
				chunk = ""
			}
		}
		for _, part := range splitOrgChunk(chunk) {
			name := cleanOrgName(part)
			if name == "" {
				continue
			}
			if wolkenRe.MatchString(name) {
				if out.Company == "" {
					out.Company = name
				}
				continue
			}
			if out.ServiceProvider == "" {
				out.ServiceProvider = name
			}
		}
	}
	return out
}

// splitOrgChunk razdvaja dve firme iz iste linije (kolone razdvojene sa 3+
// razmaka), ali spaja delove istog naziva („Wolken-ASM" + „GmbH").
func splitOrgChunk(chunk string) []string {
	parts := columnGapRe.Split(strings.TrimSpace(chunk), -1)
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if len(out) == 0 {
			out = append(out, p)
			continue
		}
		prev := out[len(out)-1]
		if !orgSuffixRe.MatchString(prev) && !wolkenRe.MatchString(p) {
			out[len(out)-1] = prev + " " + p
			continue
		}
		out = append(out, p)
	}
	return out
}

// cleanOrgName uklanja višak razmaka, interpunkciju na ivicama i slučajno
// udvajanje pravne forme („GmbHGmbH" -> „GmbH").
func cleanOrgName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, ":;,/|")
	s = strings.Join(strings.Fields(s), " ")
	for _, suffix := range []string{"GmbH", "KG", "AG", "UG", "SE"} {
		re := regexp.MustCompile(`(?i)\b` + suffix + `\s*[/,;\-]*\s*` + suffix + `\b`)
		s = re.ReplaceAllString(s, suffix)
	}
	return strings.TrimSpace(s)
}

// cleanOperator skraćuje „Einbläser" vrednost na ime (do tri reči).
func cleanOperator(s string) string {
	fields := strings.Fields(strings.TrimSpace(strings.Trim(s, ",;/")))
	if len(fields) > 3 {
		fields = fields[:3]
	}
	return strings.Join(fields, " ")
}
