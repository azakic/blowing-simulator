package main

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// OCR Timestamp Camera overlays. Slike bez EXIF GPS-a često imaju lokaciju,
// datum i vreme ispisane u pikselima. Tesseract (deu+eng) čita tekst nakon
// podešavanja kontrasta i threshold-a; koordinate iz overlay-a su autoritet
// nad geolokacijom pregledača (koja je gde je telefon u trenutku uploada).

var photoOCRMu sync.Mutex

type photoOCRResult struct {
	Lat       float64
	Lon       float64
	HasCoords bool
	Address   string
	Time      string
	Raw       string
}

func (r photoOCRResult) score() int {
	s := 0
	if r.Time != "" {
		s += 3
	}
	if r.HasCoords {
		s += 3
	}
	if r.Address != "" {
		s += 2
	}
	return s
}

// runPhotoOCR obrađuje fotografije HaP-a koje još nisu OCR-ovane.
func runPhotoOCR(ctx context.Context, haID int64) {
	if _, err := exec.LookPath("tesseract"); err != nil {
		return
	}
	photoOCRMu.Lock()
	defer photoOCRMu.Unlock()

	var photos []struct {
		ID        int64  `db:"id"`
		ObjectKey string `db:"object_key"`
	}
	if err := db.SelectContext(ctx, &photos, `SELECT id, object_key FROM ha_photos
		WHERE ha_id = $1 AND ocr_at IS NULL ORDER BY id LIMIT 10`, haID); err != nil {
		return
	}
	for _, p := range photos {
		// Atomski claim: u klasteru rade dve replike, pa istu fotografiju
		// obrađuje samo pod koji prvi postavi ocr_at.
		var claimed int64
		if err := db.GetContext(ctx, &claimed, `UPDATE ha_photos SET ocr_at = NOW()
			WHERE id = $1 AND ocr_at IS NULL RETURNING id`, p.ID); err != nil {
			continue
		}
		data, _, err := sourceStore.GetRaw(ctx, p.ObjectKey)
		if err != nil {
			log.Printf("ocr photo %d: %v", p.ID, err)
			// Otključaj, da slika ne ostane trajno bez OCR-a.
			_, _ = db.ExecContext(ctx, `UPDATE ha_photos SET ocr_at = NULL
				WHERE id = $1 AND ocr_text = ''`, p.ID)
			continue
		}
		res := ocrPhoto(data)
		var lat, lon any
		if res.HasCoords {
			lat, lon = res.Lat, res.Lon
		}
		addr := res.Address
		if res.HasCoords {
			addr = geocodeCoords(ctx, res.Lat, res.Lon)
		}
		if _, err := db.ExecContext(ctx, `UPDATE ha_photos SET
				ocr_text = $1, ocr_time = $2, ocr_at = NOW(),
				lat = COALESCE($3, lat), lon = COALESCE($4, lon),
				geo_address = CASE WHEN $5 <> '' THEN $5 ELSE geo_address END
			WHERE id = $6`, res.Raw, res.Time, lat, lon, addr, p.ID); err != nil {
			log.Printf("ocr photo %d save: %v", p.ID, err)
		}
	}
	// Adrese za koordinate iz overlay-a. Geokoder ide ~1 zahtev/s, ali
	// keširane koordinate ne troše nijedan zahtev.
	applyPhotoGeoAddresses(ctx, haID)
}

// geocodeCoords koristi keš, pa Nominatim; na grešku vraća prazno.
func geocodeCoords(ctx context.Context, lat, lon float64) string {
	if !geocoderEnabled() {
		return ""
	}
	if addr, ok := cachedGeoAddress(ctx, lat, lon); ok {
		return addr
	}
	addr, err := reverseGeocode(ctx, lat, lon)
	if err != nil {
		log.Printf("geocode %.6f,%.6f: %v", lat, lon, err)
		return ""
	}
	storeGeoAddress(ctx, lat, lon, addr)
	return addr
}

// ocrPhoto proba četiri rotacije i vraća najbolji rezultat.
func ocrPhoto(data []byte) photoOCRResult {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return photoOCRResult{}
	}
	img = scaleImageTo(img, 2400)
	best := photoOCRResult{}
	cur := img
	for rot := 0; rot < 4; rot++ {
		if rot > 0 {
			cur = rotateImage90(cur)
		}
		prepared := ocrPrepare(cur)
		if prepared == nil {
			continue
		}
		text := runTesseract(prepared)
		if strings.TrimSpace(text) == "" {
			continue
		}
		res := parseOCRText(text)
		if res.score() > best.score() {
			best = res
		}
		if best.HasCoords && best.Time != "" {
			break
		}
	}
	return best
}

// scaleImageTo dovodi najdužu stranu na side (gore ili dole).
func scaleImageTo(img image.Image, side int) image.Image {
	longest := imageLongestSide(img)
	if longest == 0 {
		return img
	}
	if longest > side {
		return scaleImageDown(img, side)
	}
	return scaleImageUp(img, side)
}

// ocrPrepare pravi crno-belu sliku (crn tekst na beloj pozadini) sa
// istegnutim kontrastom. Overlay je beo, pa se sve svetlo prevodi u crno.
func ocrPrepare(img image.Image) *image.Gray {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return nil
	}
	g := image.NewGray(image.Rect(0, 0, w, h))
	min, max := uint32(255), uint32(0)
	for y := 0; y < h; y++ {
		row := g.Pix[y*g.Stride : y*g.Stride+w]
		for x := 0; x < w; x++ {
			r, gg, bb, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			lum := (r*299 + gg*587 + bb*114) / 1000 >> 8
			row[x] = uint8(lum)
			if lum < min {
				min = lum
			}
			if lum > max {
				max = lum
			}
		}
	}
	span := max - min
	if span < 20 {
		span = 20
	}
	for i, v := range g.Pix {
		out := uint32(v)
		if out > min {
			out = (out - min) * 255 / span
		} else {
			out = 0
		}
		if out > 240 {
			g.Pix[i] = 0 // tekst
		} else {
			g.Pix[i] = 255 // pozadina
		}
	}
	return g
}

func runTesseract(img image.Image) string {
	f, err := os.CreateTemp("", "ocr-*.png")
	if err != nil {
		return ""
	}
	path := f.Name()
	if err := png.Encode(f, img); err != nil {
		f.Close()
		os.Remove(path)
		return ""
	}
	f.Close()
	defer os.Remove(path)

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tesseract", path, "stdout", "-l", "deu+eng", "--psm", "6").Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// --- parsiranje OCR teksta ---

var (
	ocrTimeEN      = regexp.MustCompile(`(?i)\b(jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*\.?\s*(\d{1,2})\s*,?\s*(\d{4})[^\d]{0,20}(\d{1,2}):(\d{2})(?::(\d{2}))?`)
	ocrTimeDE      = regexp.MustCompile(`(?i)\b(\d{1,2})\.?\s+(januar|februar|märz|maerz|april|mai|juni|juli|august|september|oktober|november|dezember)\s+(\d{4})[^\d]{0,15}(\d{1,2}):(\d{2})(?::(\d{2}))?`)
	ocrTimeNum     = regexp.MustCompile(`\b(\d{1,2})[./](\d{1,2})[./](\d{2,4})\s+(\d{1,2}):(\d{2})(?::(\d{2}))?`)
	ocrCoords      = regexp.MustCompile(`([+-]?\d{1,3}[.,]\d{4,})\s*[,;]\s*([+-]?\d{1,3}[.,]\d{4,})`)
	ocrStreetFirst = regexp.MustCompile(`^\s*(\d{1,4}\s?[a-zA-Z]?)\s+(.+)$`)
	ocrStreetLast  = regexp.MustCompile(`^\s*(.+?)\s+(\d{1,4}\s?[a-zA-Z]?)\s*$`)
	ocrPostcode    = regexp.MustCompile(`^\s*\d{5}\s+\S`)
)

var ocrMonthsEN = map[string]time.Month{
	"jan": time.January, "feb": time.February, "mar": time.March, "apr": time.April,
	"may": time.May, "jun": time.June, "jul": time.July, "aug": time.August,
	"sep": time.September, "oct": time.October, "nov": time.November, "dec": time.December,
}

var ocrMonthsDE = map[string]time.Month{
	"januar": time.January, "februar": time.February, "märz": time.March, "maerz": time.March,
	"april": time.April, "mai": time.May, "juni": time.June, "juli": time.July,
	"august": time.August, "september": time.September, "oktober": time.October,
	"november": time.November, "dezember": time.December,
}

func parseOCRText(text string) photoOCRResult {
	res := photoOCRResult{Raw: text}
	res.Time = parseOCRTime(text)
	if m := ocrCoords.FindStringSubmatch(text); m != nil {
		lat, err1 := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", "."), 64)
		lon, err2 := strconv.ParseFloat(strings.ReplaceAll(m[2], ",", "."), 64)
		if err1 == nil && err2 == nil && lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180 {
			res.Lat, res.Lon, res.HasCoords = lat, lon, true
		}
	}
	if !res.HasCoords {
		res.Address = parseOCRAddress(text)
	}
	return res
}

func ocrDate(y int, mo time.Month, d, h, mi, sec int) string {
	if d < 1 || d > 31 || mo < 1 || mo > 12 || h < 0 || h > 23 || mi < 0 || mi > 59 || sec < 0 || sec > 59 {
		return ""
	}
	return time.Date(y, mo, d, h, mi, sec, 0, time.UTC).Format("02.01.2006 15:04")
}

func parseOCRTime(text string) string {
	if m := ocrTimeEN.FindStringSubmatch(text); m != nil {
		d, _ := strconv.Atoi(m[2])
		y, _ := strconv.Atoi(m[3])
		h, _ := strconv.Atoi(m[4])
		mi, _ := strconv.Atoi(m[5])
		sec := 0
		if m[6] != "" {
			sec, _ = strconv.Atoi(m[6])
		}
		if v := ocrDate(y, ocrMonthsEN[strings.ToLower(m[1][:3])], d, h, mi, sec); v != "" {
			return v
		}
	}
	if m := ocrTimeDE.FindStringSubmatch(text); m != nil {
		d, _ := strconv.Atoi(m[1])
		y, _ := strconv.Atoi(m[3])
		h, _ := strconv.Atoi(m[4])
		mi, _ := strconv.Atoi(m[5])
		sec := 0
		if m[6] != "" {
			sec, _ = strconv.Atoi(m[6])
		}
		if v := ocrDate(y, ocrMonthsDE[strings.ToLower(m[2])], d, h, mi, sec); v != "" {
			return v
		}
	}
	if m := ocrTimeNum.FindStringSubmatch(text); m != nil {
		d, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		y, _ := strconv.Atoi(m[3])
		if y < 100 {
			y += 2000
		}
		h, _ := strconv.Atoi(m[4])
		mi, _ := strconv.Atoi(m[5])
		sec := 0
		if m[6] != "" {
			sec, _ = strconv.Atoi(m[6])
		}
		if v := ocrDate(y, time.Month(mo), d, h, mi, sec); v != "" {
			return v
		}
	}
	return ""
}

func ocrNoiseLine(l string) bool {
	low := strings.ToLower(l)
	for _, k := range []string{
		"altitude", "geschwindigkeit", "speed", "index", "msnm", "km/h", "°",
		"m ü", "m u. m", "deutschland", "nordrhein", "westfalen", "arnsberg",
		"karten", "freitag", "montag", "dienstag", "mittwoch", "donnerstag",
		"samstag", "sonntag", "januar", "februar", "märz", "april", "mai",
		"juni", "juli", "august", "september", "oktober", "november", "dezember",
	} {
		if strings.Contains(low, k) {
			return true
		}
	}
	return false
}

func parseOCRAddress(text string) string {
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(strings.Trim(lines[i], " .,;:|-\\/"))
		if len(line) < 4 || ocrNoiseLine(line) {
			continue
		}
		if strings.Contains(line, ":") || strings.Count(line, ".") >= 2 {
			continue
		}
		var house, street string
		if m := ocrStreetFirst.FindStringSubmatch(line); m != nil {
			house, street = m[1], m[2]
		} else if m := ocrStreetLast.FindStringSubmatch(line); m != nil {
			street, house = m[1], m[2]
		} else {
			continue
		}
		street = strings.TrimSpace(street)
		if len(street) < 3 || !strings.ContainsAny(street, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			continue
		}
		base := street + " " + strings.TrimSpace(house)
		for j := i + 1; j < len(lines) && j <= i+3; j++ {
			city := strings.TrimSpace(strings.Trim(lines[j], " .,;:|-\\/"))
			if city == "" || ocrNoiseLine(city) {
				continue
			}
			if ocrPostcode.MatchString(city) {
				return base + ", " + city
			}
			if len(city) >= 3 && !strings.ContainsAny(city, "0123456789") {
				return base + ", " + city
			}
		}
		return base
	}
	return ""
}

// startPhotoSweeper periodično obrađuje fotografije koje su ostale bez OCR-a
// ili bez adrese: posle restarta poda usred obrade, ili kada je slika zamenjena
// (tada se ocr_at i geo_address resetuju pri čuvanju HaP-a).
func startPhotoSweeper() {
	go func() {
		for {
			sweepPhotos(context.Background())
			time.Sleep(20 * time.Minute)
		}
	}()
}

// sweepPhotos obrađuje najviše nekoliko HaP-ova po ciklusu, da tesseract i
// Nominatim (limit 1 zahtev/s) ostanu rasterećeni.
func sweepPhotos(ctx context.Context) {
	var ocrIDs []int64
	if err := db.SelectContext(ctx, &ocrIDs, `SELECT DISTINCT ha_id FROM ha_photos
		WHERE ocr_at IS NULL ORDER BY ha_id LIMIT 5`); err != nil {
		log.Printf("photo sweeper ocr: %v", err)
	} else {
		for _, id := range ocrIDs {
			octx, cancel := context.WithTimeout(ctx, 3*time.Minute)
			runPhotoOCR(octx, id)
			cancel()
		}
	}

	var geoIDs []int64
	if err := db.SelectContext(ctx, &geoIDs, `SELECT DISTINCT ha_id FROM ha_photos
		WHERE lat IS NOT NULL AND lon IS NOT NULL AND COALESCE(geo_address, '') = ''
		ORDER BY ha_id LIMIT 5`); err != nil {
		log.Printf("photo sweeper geo: %v", err)
		return
	}
	for _, id := range geoIDs {
		gctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		applyPhotoGeoAddresses(gctx, id)
		cancel()
	}
}
