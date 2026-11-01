package simulator

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// JettingDetails su podaci iz „Rohrparameter / Kabelparameter / Einblasgerät"
// tabele i iz summary bloka Jettlogger zaglavlja. U dokumentu postoje samo kao
// slika, pa se čitaju iz OCR koordinata (kolone po `x`, redovi po `y`).
type JettingDetails struct {
	PipeManufacturer string
	PipeType         string
	PipeColors       []string
	PipeInnerWall    string
	PipeLayingType   string
	PipeInnerDia     *float64
	PipeOuterDia     *float64
	PipeTemp         *float64

	CableManufacturer string
	CableDesignation  string
	CableDrumNo       string
	CableDiameter     *float64
	CableFiberCount   *int
	CableTemp         *float64

	DeviceModel     string
	Lubricant       string
	CompressorModel string

	BlowingTime  string
	Distance     *int
	MeterStart   *int
	MeterEnd     *int
	WeatherTemp  *float64
	WeatherHumid *float64
	GPSLat       *float64
	GPSLon       *float64
}

var (
	reHersteller    = regexp.MustCompile(`(?i)Hersteller\b`)
	reRohrtyp       = regexp.MustCompile(`(?i)Rohrtyp\b`)
	reFarbe         = regexp.MustCompile(`(?i)Farbe\s*[/\\]\s*Kennung\b`)
	reVerlegung     = regexp.MustCompile(`(?i)Verlegungsart(?:\s+des\s+Rohres)?`)
	reRohrInnenDia  = regexp.MustCompile(`(?i)Rohrinnendurchmesser\b`)
	reRohrAussenDia = regexp.MustCompile(`(?i)Rohrau\w{0,4}endurchmesser`)
	reRohrTemp      = regexp.MustCompile(`(?i)Rohrtemperatur\b`)
	reRohrInnenWand = regexp.MustCompile(`(?i)Rohrinnenwand\b`)

	reKabeltyp     = regexp.MustCompile(`(?i)Kabeltyp\b`)
	reKabelDia     = regexp.MustCompile(`(?i)Kabel-?\s*[Ø®©@O0oQ]`)
	reFaseranzahl  = regexp.MustCompile(`(?i)Faseranzahl\b`)
	reKabeltrommel = regexp.MustCompile(`(?i)Kabeltrommel-?\s*Nr\.?\b`)
	reKabelTemp    = regexp.MustCompile(`(?i)Kabeltemperatur\b`)

	reEinblasgeraet = regexp.MustCompile(`(?i)Einblasger(?:ä|ae)t\b`)
	reGleitmittel   = regexp.MustCompile(`(?i)Gleitmittel\b`)
	reKompressor    = regexp.MustCompile(`(?i)Kompressor\b`)

	reZeitDauer   = regexp.MustCompile(`(?i)Zeit\s*-?\s*Dauer`)
	reStreckenl   = regexp.MustCompile(`(?i)Streckenl(?:ä|ae)nge\b`)
	reStart       = regexp.MustCompile(`(?i)Start\b`)
	reEnde        = regexp.MustCompile(`(?i)Ende\b`)
	reUmgebung    = regexp.MustCompile(`(?i)Umgebungsluft\s*temp`)
	reLuftfeuchte = regexp.MustCompile(`(?i)Luftfeuchtigkeit\b`)
	reOrtGPS      = regexp.MustCompile(`(?i)Ort\s*\(?\s*GPS`)

	reDuration = regexp.MustCompile(`\b(\d{1,2}:\d{2}:\d{2})\b`)
	reNumber   = regexp.MustCompile(`-?\d+(?:[.,]\d+)?`)
)

// svi labeli — koristi se da se vrednost ne uzme iz sledeće linije ako je ona
// zapravo novo polje.
var jettingDetailLabels = []*regexp.Regexp{
	reHersteller, reRohrtyp, reFarbe, reVerlegung, reRohrInnenDia, reRohrAussenDia,
	reRohrTemp, reRohrInnenWand, reKabeltyp, reKabelDia, reFaseranzahl, reKabeltrommel,
	reKabelTemp, reEinblasgeraet, reGleitmittel, reKompressor, reStreckenl, reStart,
	reEnde, reUmgebung, reLuftfeuchte,
}

// ExtractJettingDetails čita tabelu i summary iz OCR reči.
func ExtractJettingDetails(words []OCRWord) JettingDetails {
	var d JettingDetails
	anchors := jettingColumnAnchors(words)
	if len(anchors) < 2 {
		return d
	}

	cols := make([][]OCRWord, len(anchors))
	for _, w := range words {
		cols[nearestAnchor(float64(w.Left), anchors)] = append(cols[nearestAnchor(float64(w.Left), anchors)], w)
	}

	pipe := columnLines(cols[0])
	cable := columnLines(cols[1])
	equip := []string{}
	if len(cols) > 2 {
		equip = columnLines(cols[2])
	}
	all := columnLines(words)

	// Cev
	d.PipeManufacturer = cleanOCRValue(labelValue(pipe, reHersteller))
	d.PipeType = normalizePipeType(cleanOCRValue(labelValue(pipe, reRohrtyp)))
	if c := cleanOCRValue(labelValue(pipe, reFarbe)); c != "" {
		d.PipeColors = cleanColors(strings.Fields(c))
	}
	d.PipeLayingType = cleanOCRValue(labelValue(pipe, reVerlegung))
	d.PipeInnerDia = parseNumber(labelValue(pipe, reRohrInnenDia))
	d.PipeOuterDia = parseNumber(labelValue(pipe, reRohrAussenDia))
	d.PipeTemp = parseNumber(labelValue(pipe, reRohrTemp))
	d.PipeInnerWall = cleanOCRValue(labelValue(pipe, reRohrInnenWand))

	// Kabl
	d.CableManufacturer = cleanOCRValue(labelValue(cable, reHersteller))
	d.CableDesignation = cleanOCRValue(labelValue(cable, reKabeltyp))
	d.CableDiameter = parseNumber(labelValue(cable, reKabelDia))
	d.CableFiberCount = parseInt(labelValue(cable, reFaseranzahl))
	d.CableDrumNo = cleanDrumNo(labelValue(cable, reKabeltrommel))
	d.CableTemp = parseNumber(labelValue(cable, reKabelTemp))

	// Uređaj
	d.DeviceModel = cleanOCRValue(labelValue(equip, reEinblasgeraet))
	d.Lubricant = cleanOCRValue(labelValue(equip, reGleitmittel))
	d.CompressorModel = cleanOCRValue(labelValue(equip, reKompressor))

	// Summary: „Metrierung / Umgebung" blok je u srednjoj koloni; ako ga tamo
	// nema, traži u svim linijama.
	summary := cable
	d.BlowingTime = firstMatch(labelValue(all, reZeitDauer), reDuration)
	d.Distance = firstInt(labelValue(summary, reStreckenl), labelValue(all, reStreckenl))
	d.MeterStart = firstInt(labelValue(summary, reStart), labelValue(all, reStart))
	d.MeterEnd = firstInt(labelValue(summary, reEnde), labelValue(all, reEnde))
	d.WeatherTemp = parseNumber(firstNonEmptyStr(labelValue(summary, reUmgebung), labelValue(all, reUmgebung)))
	d.WeatherHumid = parseNumber(firstNonEmptyStr(labelValue(summary, reLuftfeuchte), labelValue(all, reLuftfeuchte)))
	gps := labelValueAnywhere(all, reOrtGPS)
	nums := reNumber.FindAllString(gps, -1)
	if len(nums) < 2 {
		// OCR ume da razdvoji labelu od vrednosti: traži liniju sa
		// koordinatama (dva broja razdvojena tačkom-zarezom).
		for _, line := range all {
			if strings.Contains(line, ";") {
				if found := reNumber.FindAllString(line, -1); len(found) >= 2 {
					nums = found
					break
				}
			}
		}
	}
	if len(nums) >= 2 {
		d.GPSLat = parseNumber(nums[0])
		d.GPSLon = parseNumber(nums[1])
	}
	return d
}

// jettingColumnAnchors vraća x pozicije tri kolone iz zaglavlja tabele.
func jettingColumnAnchors(words []OCRWord) []float64 {
	var anchors []float64
	for _, w := range words {
		t := strings.TrimSpace(w.Text)
		switch {
		case strings.EqualFold(t, "Rohrparameter"):
			anchors = append(anchors, float64(w.Left))
		case strings.EqualFold(t, "Kabelparameter"):
			anchors = append(anchors, float64(w.Left))
		case strings.HasPrefix(strings.ToLower(t), "einblasger") && len(anchors) == 2:
			anchors = append(anchors, float64(w.Left))
		}
		if len(anchors) == 3 {
			break
		}
	}
	sort.Float64s(anchors)
	return anchors
}

// columnLines grupiše reči u redove po y i vraća tekst redova.
func columnLines(words []OCRWord) []string {
	if len(words) == 0 {
		return nil
	}
	sorted := append([]OCRWord(nil), words...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Top != sorted[j].Top {
			return sorted[i].Top < sorted[j].Top
		}
		return sorted[i].Left < sorted[j].Left
	})

	const tolerance = 14 // piksela na 300 dpi
	var lines []string
	var current []OCRWord
	for _, w := range sorted {
		if len(current) == 0 || absInt(w.Top-current[0].Top) <= tolerance {
			current = append(current, w)
			continue
		}
		lines = append(lines, joinWords(current))
		current = []OCRWord{w}
	}
	if len(current) > 0 {
		lines = append(lines, joinWords(current))
	}
	return lines
}

func joinWords(words []OCRWord) string {
	sort.SliceStable(words, func(i, j int) bool { return words[i].Left < words[j].Left })
	parts := make([]string, 0, len(words))
	for _, w := range words {
		parts = append(parts, w.Text)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

// labelValue vraća vrednost posle labele (u istoj liniji, ili u sledećoj ako
// ova nema vrednost). Sledeća linija se ne uzima ako je i sama labela.
func labelValue(lines []string, label *regexp.Regexp) string {
	for i, line := range lines {
		loc := label.FindStringIndex(line)
		if loc == nil {
			continue
		}
		// „Einblasgerät / Kompressor" je zaglavlje kolone — preskoči ga.
		if strings.HasSuffix(strings.TrimSpace(line[:loc[0]]), "/") {
			continue
		}
		rest := cleanDetailValue(line[loc[1]:])
		// „Einblasgerät / Kompressor" je zaglavlje kolone, ne vrednost.
		if strings.HasPrefix(rest, "/") {
			continue
		}
		if rest != "" {
			return rest
		}
		if i+1 < len(lines) && !isDetailLabel(lines[i+1]) {
			return cleanDetailValue(lines[i+1])
		}
		return ""
	}
	return ""
}

func isDetailLabel(line string) bool {
	for _, re := range jettingDetailLabels {
		if re.MatchString(strings.TrimSpace(line)) {
			return true
		}
	}
	return false
}

// cleanDetailValue uklanja separatore i OCR đubre na ivicama.
func cleanDetailValue(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, "|:;,-–— ")
	s = strings.TrimRight(s, "|")
	return strings.TrimSpace(s)
}

func firstMatch(s string, re *regexp.Regexp) string {
	if m := re.FindString(s); m != "" {
		return m
	}
	return ""
}

func parseNumber(s string) *float64 {
	if s == "" {
		return nil
	}
	m := reNumber.FindString(s)
	if m == "" {
		return nil
	}
	v, err := strconv.ParseFloat(strings.ReplaceAll(m, ",", "."), 64)
	if err != nil {
		return nil
	}
	return &v
}

func parseInt(s string) *int {
	f := parseNumber(s)
	if f == nil {
		return nil
	}
	v := int(*f)
	return &v
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// labelValueAnywhere traži labelu bilo gde u liniji (npr. „… | Ort(GPS) 52.4°…").
func labelValueAnywhere(lines []string, label *regexp.Regexp) string {
	for _, line := range lines {
		loc := label.FindStringIndex(line)
		if loc == nil {
			continue
		}
		if v := cleanDetailValue(line[loc[1]:]); v != "" {
			return v
		}
	}
	return ""
}

func firstInt(values ...string) *int {
	for _, v := range values {
		if n := parseInt(v); n != nil {
			return n
		}
	}
	return nil
}

func firstNonEmptyStr(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// DebugJettingColumns vraća linije tri kolone — za dijagnostiku i testove.
func DebugJettingColumns(words []OCRWord) [][]string {
	anchors := jettingColumnAnchors(words)
	if len(anchors) == 0 {
		return nil
	}
	cols := make([][]OCRWord, len(anchors))
	for _, w := range words {
		idx := nearestAnchor(float64(w.Left), anchors)
		cols[idx] = append(cols[idx], w)
	}
	out := make([][]string, len(cols))
	for i := range cols {
		out[i] = columnLines(cols[i])
	}
	return out
}

// cleanOCRValue uklanja OCR đubre: višak razmaka, kolonske separatore i
// checkbox oznake na kraju vrednosti („glatt gerieft U", „LS Cable | System").
func cleanOCRValue(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, " | ", " ")
	s = strings.TrimSpace(strings.TrimSuffix(s, " |"))
	fields := strings.Fields(s)
	// Vodeći jednoslovni šum iz OCR-a: "I Gabocom", "l E9/125".
	if len(fields) > 1 && len(fields[0]) == 1 && strings.ContainsAny(fields[0], "Il|!1") {
		fields = fields[1:]
	}
	// Završni jednoslovni šum: „glatt gerieft D", „… U)".
	if len(fields) > 1 {
		last := fields[len(fields)-1]
		if len(last) == 1 && strings.ContainsAny(last, "DIUOQ[]()|") {
			fields = fields[:len(fields)-1]
		}
	}
	return strings.Join(fields, " ")
}

// normalizePipeType skida OCR šum ispred broja ("l7x1,5" → "7x1,5").
func normalizePipeType(s string) string {
	if len(s) > 1 && strings.ContainsAny(s[:1], "lI|") && s[1] >= '0' && s[1] <= '9' {
		return s[1:]
	}
	return s
}

// cleanColors normalizuje česte OCR varijante naziva boja.
func cleanColors(colors []string) []string {
	out := make([]string, 0, len(colors))
	for _, c := range colors {
		switch strings.ToLower(c) {
		case "weis", "weiss":
			c = "Weiß"
		case "gruen", "grun", "grin", "grOn":
			c = "Grün"
		}
		if c != "" {
			out = append(out, c)
		}
	}
	return out
}

// cleanDrumNo ispravlja najčešću OCR zamenu kod broja koluta (£ → E).
func cleanDrumNo(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "| ")
	s = strings.ReplaceAll(s, "£", "E")
	return s
}
