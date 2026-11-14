package main

import (
	"regexp"
	"strings"
)

// Adresa se čuva razdvojeno (ulica, broj, PLZ, grad), a „address“ je izvedeni
// string za prikaz, PDF, Google Maps i pretragu. Razdvajanje je namerno
// jednostavno: nemačke adrese su „Ulica broj[, PLZ Grad]“, a sve što ne može
// da se razdvoji ostaje u ulici (nikad se ne gubi tekst).

var (
	// „Goethestraße 28“, „Hauptstraße 3-5“, „Am Weg 13a“
	streetNoRe = regexp.MustCompile(`^(.+?)\s+(\d+[a-zA-Z]?(?:\s*[-–/]\s*\d+[a-zA-Z]?)?)$`)
	// „58300 Wetter“ (deo posle zareza)
	plzCityRe = regexp.MustCompile(`^(\d{5})\s+(.+)$`)
	// „Goethestraße 28 58300 Wetter“ (bez zareza)
	plzTailRe = regexp.MustCompile(`^(.*\S)\s+(\d{5})\s+(\D.*)$`)
)

// splitStreetNumber deli „Ulica broj“ na ulicu i broj.
func splitStreetNumber(s string) (street, number string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	if m := streetNoRe.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
	}
	return s, ""
}

// parseAddress deli slobodan unos na delove. Podržava:
//
//	„Goethestraße 28“                → ulica + broj
//	„Goethestraße 28, 58300 Wetter“  → + PLZ i grad
//	„Goethestraße 28, Volmarstein“   → + grad
//	„Goethestraße 28 58300 Wetter“   → bez zareza
//	„Volmarstein“                    → sve u gradu (ulica prazna)
func parseAddress(raw string) (street, number, postcode, city string) {
	s := strings.TrimSpace(strings.Join(strings.Fields(raw), " "))
	if s == "" {
		return "", "", "", ""
	}

	if i := strings.Index(s, ","); i > 0 {
		street, number = splitStreetNumber(strings.TrimSpace(s[:i]))
		right := strings.TrimSpace(s[i+1:])
		if m := plzCityRe.FindStringSubmatch(right); m != nil {
			postcode, city = m[1], strings.TrimSpace(m[2])
		} else {
			city = right
		}
		return street, number, postcode, city
	}

	if m := plzTailRe.FindStringSubmatch(s); m != nil {
		street, number = splitStreetNumber(strings.TrimSpace(m[1]))
		return street, number, m[2], strings.TrimSpace(m[3])
	}

	street, number = splitStreetNumber(s)
	return street, number, "", ""
}

// formatAddress gradi prikaz iz delova (ono što ide u „address“).
func formatAddress(street, number, postcode, city string) string {
	line := strings.TrimSpace(street)
	if n := strings.TrimSpace(number); n != "" {
		line = strings.TrimSpace(line + " " + n)
	}
	loc := strings.TrimSpace(postcode)
	if c := strings.TrimSpace(city); c != "" {
		loc = strings.TrimSpace(loc + " " + c)
	}
	switch {
	case line != "" && loc != "":
		return line + ", " + loc
	case line != "":
		return line
	default:
		return loc
	}
}

// streetKey je ključ za spajanje sa direktorijumom: ulica + broj, bez grada.
func streetKey(street, number string) string {
	if strings.TrimSpace(street) == "" {
		return ""
	}
	return customerKey(strings.TrimSpace(street + " " + number))
}

// companyWords su pokazatelji da „kunde“ nije osoba nego firma/uprava.
var companyWords = map[string]bool{
	"gmbh": true, "mbh": true, "ohg": true, "gbr": true, "kg": true,
	"ev": true, "hausverwaltung": true, "haus-": true, "mietverwaltung": true,
	"wohnungsverwaltung": true, "verwaltung": true, "immobilien": true,
	"genossenschaft": true, "weg": true,
}

func isCompanyName(s string) bool {
	for _, w := range strings.Fields(strings.ToLower(s)) {
		if companyWords[w] {
			return true
		}
	}
	return false
}

// customerNameParts deli „kunde“ na ime, prezime ili firmu. Firma ide u
// company (imena ostaju prazna), jer protokoli imaju odvojeno polje za firmu.
func customerNameParts(kunde string) (first, last, company string) {
	if isCompanyName(kunde) {
		return "", "", strings.TrimSpace(cleanCell(kunde))
	}
	first, last = splitKunde(kunde)
	return first, last, ""
}

// nvtFromSection vadi NVT iz spojenog polja importovanih protokola
// („Mühlenstr 13f / NVt 1V2800“, „Dammstr.3 / 1V2200_095658“).
var (
	nvtVRe    = regexp.MustCompile(`(?i)(\d{1,2}\s*v\s*\d{3,6})`)
	nvtDotted = regexp.MustCompile(`\b(\d{1,2}[.,]\d{1,2})\b`)
)

func nvtFromSection(s string) string {
	if m := nvtVRe.FindStringSubmatch(s); m != nil {
		return strings.ToUpper(strings.ReplaceAll(m[1], " ", ""))
	}
	if m := nvtDotted.FindStringSubmatch(s); m != nil {
		return strings.ReplaceAll(m[1], ",", ".")
	}
	return ""
}

// addressSlug normalizuje adresu za poređenje sa importovanim protokolima:
// mala slova, bez razmaka i interpunkcije („Dammstr. 9“ → „dammstr9“).
func addressSlug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == 'ä' || r == 'ö' || r == 'ü' || r == 'ß':
			b.WriteRune(r)
		}
	}
	return b.String()
}
