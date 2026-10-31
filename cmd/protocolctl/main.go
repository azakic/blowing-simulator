// protocolctl je offline alat za rad sa protokolima: izvlači tekst iz PDF-a
// (uz OCR fallback), prepoznaje format, parsira i ispisuje rezultat.
//
// Ne dira bazu i ne treba mu klaster — koristi se za razvoj parsera i za
// proveru stvarnih dokumenata pre upload-a.
//
//	go run ./cmd/protocolctl -in protokol.pdf
//	go run ./cmd/protocolctl -in protokol.pdf -json
//	go run ./cmd/protocolctl -in protokol.pdf -dump
//	go run ./cmd/protocolctl -in sken.pdf -ocr force
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"blowing-simulator/internal/simulator"
)

func main() {
	in := flag.String("in", "", "putanja do PDF-a")
	formatFlag := flag.String("format", "auto", "auto | fremco | jetting")
	dump := flag.Bool("dump", false, "ispiši normalizovan tekst i završi")
	asJSON := flag.Bool("json", false, "ispiši parsirane podatke kao JSON")
	ocrMode := flag.String("ocr", "auto", "auto | off | force")
	minChars := flag.Int("min-chars", 40, "prag (znakova) po strani ispod kog -ocr=auto pokreće OCR")
	dir := flag.String("dir", "", "obradi sve PDF-ove u folderu (rekurzivno) i ispiši jednu liniju po fajlu")
	renderOut := flag.String("render", "", "generiši PDF iz parsiranih podataka")
	verify := flag.Bool("verify", false, "round-trip: parse → render → parse → diff")
	bboxCount := flag.Bool("bbox", false, "prikaži broj merenja iz koordinatnog (bbox) parsera")
	fontPath := flag.String("font", "web/static/fonts/DejaVuSans.ttf", "TTF font za generisani PDF")
	flag.Parse()

	if *dir != "" {
		runDir(*dir, *formatFlag, *ocrMode, *minChars)
		return
	}
	if *in == "" {
		fmt.Fprintln(os.Stderr, "upotreba: protocolctl -in protokol.pdf [-format auto|fremco|jetting] [-dump|-json] [-ocr auto|off|force]")
		fmt.Fprintln(os.Stderr, "       protocolctl -dir /putanja/do/pdf-ova")
		fmt.Fprintln(os.Stderr, "       protocolctl -in protokol.pdf -render out.pdf")
		fmt.Fprintln(os.Stderr, "       protocolctl -in protokol.pdf -verify")
		os.Exit(2)
	}

	text, source, err := extractText(*in, *ocrMode, *minChars)
	if err != nil {
		fail(err)
	}
	if *dump {
		fmt.Println(text)
		return
	}

	format := simulator.FormatUnknown
	switch *formatFlag {
	case "fremco":
		format = simulator.FormatFremco
	case "jetting":
		format = simulator.FormatJetting
	case "", "auto":
		format = simulator.DetectFormat(text)
	default:
		fail(fmt.Errorf("nepoznat -format %q (auto|fremco|jetting)", *formatFlag))
	}
	if format == simulator.FormatUnknown {
		fail(fmt.Errorf("format nije prepoznat (%d znakova); probaj -format fremco|jetting ili -dump", len(text)))
	}

	if *renderOut != "" {
		model, err := buildRenderModel(text, format)
		if err != nil {
			fail(err)
		}
		out, err := os.Create(*renderOut)
		if err != nil {
			fail(err)
		}
		defer out.Close()
		if err := simulator.RenderProtocolPDF(out, model, *fontPath); err != nil {
			fail(err)
		}
		fmt.Printf("render: %s (%d merenja)\n", *renderOut, len(model.Rows))
		return
	}
	if *verify {
		verifyRoundTrip(*in, text, format, *fontPath)
		return
	}

	fields, measurements, payload, err := parseProtocol(text, format)
	if err != nil {
		fail(err)
	}

	if *asJSON {
		out, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			fail(err)
		}
		fmt.Println(string(out))
		return
	}

	fmt.Printf("file:              %s\n", filepath.Base(*in))
	fmt.Printf("text_source:       %s (%d znakova)\n", source, len(text))
	fmt.Printf("format:            %s\n", format)
	for _, f := range fields {
		value := strings.TrimSpace(f[1])
		if value == "" {
			fmt.Printf("%-18s—\n", f[0]+":")
			continue
		}
		fmt.Printf("%-18s%s\n", f[0]+":", value)
	}
	fmt.Printf("measurements:      %d\n", measurements)
	if *bboxCount {
		rows, err := simulator.MeasurementsFromPDFBBox(*in, string(format))
		if err != nil {
			fmt.Printf("measurements_bbox: greška: %v\n", err)
		} else {
			fmt.Printf("measurements_bbox: %d\n", len(rows))
		}
	}

	for _, w := range collectWarnings(fields, measurements, source) {
		fmt.Printf("⚠ %s\n", w)
	}
}

// extractText vraća tekst PDF-a i izvor: "text", "ocr" ili "mixed".
//
// Radi po strani: text layer se koristi gde postoji, a OCR samo za strane bez
// teksta. To je bitno jer „Jettlogger / Einblas-Protokoll" ima prvu stranu kao
// raster sliku, a tabele merenja na ostalim stranama kao pravi tekst.
func extractText(path, ocrMode string, minChars int) (string, string, error) {
	if _, err := os.Stat(path); err != nil {
		return "", "", fmt.Errorf("ne mogu da otvorim %s: %w", path, err)
	}
	pages, err := pdfPageCount(path)
	if err != nil {
		return "", "", err
	}

	var out strings.Builder
	usedText, usedOCR := 0, 0

	for page := 1; page <= pages; page++ {
		pageText, _ := runCapture("pdftotext", "-layout", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), path, "-")
		trimmed := strings.TrimSpace(pageText)

		needOCR := false
		switch ocrMode {
		case "off":
		case "force":
			needOCR = true
		default:
			needOCR = len(trimmed) < minChars
		}

		if !needOCR {
			out.WriteString(pageText)
			out.WriteString("\n")
			usedText++
			continue
		}

		ocrText, ocrErr := ocrPage(path, page)
		if ocrErr != nil {
			if trimmed == "" {
				return "", "", fmt.Errorf("strana %d: nema teksta i OCR nije uspeo: %w", page, ocrErr)
			}
			out.WriteString(pageText)
			out.WriteString("\n")
			usedText++
			continue
		}
		// OCR ume da izgubi kratak text layer (npr. naziv firme) — zadrži oba.
		if len(ocrText) < len(trimmed) {
			out.WriteString(pageText)
			out.WriteString("\n")
		}
		out.WriteString(ocrText)
		out.WriteString("\n")
		usedOCR++
	}

	text := out.String()
	if strings.TrimSpace(text) == "" {
		return "", "", fmt.Errorf("PDF nema teksta (probaj -ocr force)")
	}
	source := "text"
	switch {
	case usedOCR > 0 && usedText == 0:
		source = "ocr"
	case usedOCR > 0 && usedText > 0:
		source = "mixed"
	}
	return text, source, nil
}

func pdfPageCount(path string) (int, error) {
	info, err := runCapture("pdfinfo", path)
	if err != nil {
		return 0, fmt.Errorf("pdfinfo: %w", err)
	}
	for _, line := range strings.Split(info, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Pages:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if n, convErr := strconv.Atoi(fields[1]); convErr == nil && n > 0 {
					return n, nil
				}
			}
		}
	}
	return 0, fmt.Errorf("pdfinfo: broj strana nije pronađen")
}

// ocrPage rasterizuje jednu stranu na 400 dpi i pušta tesseract.
// `--psm 4` se pokazao najboljim za zaglavlja sa tabelom (psm 6 gubi redove).
func ocrPage(path string, page int) (string, error) {
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		return "", fmt.Errorf("pdftoppm nije instaliran")
	}
	if _, err := exec.LookPath("tesseract"); err != nil {
		return "", fmt.Errorf("tesseract nije instaliran")
	}

	dir, err := os.MkdirTemp("", "protocolctl-ocr-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	prefix := filepath.Join(dir, "page")
	if _, err := runCapture("pdftoppm", "-r", "400", "-png", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), path, prefix); err != nil {
		return "", fmt.Errorf("pdftoppm: %w", err)
	}
	images, err := filepath.Glob(prefix + "*.png")
	if err != nil || len(images) == 0 {
		return "", fmt.Errorf("pdftoppm nije napravio sliku za stranu %d", page)
	}
	text, err := runCapture("tesseract", images[0], "stdout", "-l", "deu+eng", "--psm", "4")
	if err != nil {
		return "", fmt.Errorf("tesseract: %w", err)
	}
	return text, nil
}

func runCapture(name string, args ...string) (string, error) {
	var stdout, stderr strings.Builder
	cmd := exec.Command(name, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("%s: %s", name, msg)
	}
	return stdout.String(), nil
}

func collectWarnings(fields [][2]string, measurements int, source string) []string {
	var warnings []string
	if measurements == 0 {
		warnings = append(warnings, "nema izmerenih tačaka — proveri format i ekstrakciju")
	}
	if source == "ocr" || source == "mixed" {
		warnings = append(warnings, "tekst je delimično iz OCR-a ("+source+"): očekuj greške u brojevima i umlautima")
	}
	for _, f := range fields {
		if strings.TrimSpace(f[1]) == "" {
			warnings = append(warnings, "prazno polje: "+f[0])
		}
	}
	if pn := fieldValue(fields, "project_number"); pn != "" && !looksLikeProjectNumber(pn) {
		warnings = append(warnings, "project_number ne liči na očekivani format: "+pn)
	}
	if nvt := fieldValue(fields, "section_nvt"); nvt != "" && !strings.Contains(strings.ToUpper(nvt), "NVT") {
		warnings = append(warnings, "section_nvt ne sadrži NVT: "+nvt)
	}
	return warnings
}

func fieldValue(fields [][2]string, name string) string {
	for _, f := range fields {
		if f[0] == name {
			return strings.TrimSpace(f[1])
		}
	}
	return ""
}

func looksLikeProjectNumber(s string) bool {
	letters, digits := 0, 0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'):
			letters++
		}
	}
	return letters >= 1 && digits >= 4
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "greška:", err)
	os.Exit(1)
}

// runDir prolazi kroz folder i za svaki PDF ispiše jednu liniju:
// fajl, izvor teksta, format, broj merenja i broj upozorenja.
func runDir(dir, formatFlag, ocrMode string, minChars int) {
	files := 0
	failed := 0
	warned := 0

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			fmt.Printf("%s\tERROR\t%s\n", path, err)
			failed++
			return nil
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".pdf") {
			return nil
		}
		files++

		text, source, err := extractText(path, ocrMode, minChars)
		if err != nil {
			fmt.Printf("%s\tERROR\t%s\n", path, err)
			failed++
			return nil
		}
		format := detectFormat(text, formatFlag)
		if format == simulator.FormatUnknown {
			fmt.Printf("%s\t%s\tunknown\t-\t-\tformat nije prepoznat\n", path, source)
			failed++
			return nil
		}

		fields, measurements := parseFields(text, format)
		warnings := collectWarnings(fields, measurements, source)
		if len(warnings) > 0 {
			warned++
		}
		fmt.Printf("%s\t%s\t%s\t%d\t%d\t%s\n",
			path, source, format, measurements, len(warnings), strings.Join(warnings, "; "))
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "greška:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "\n%d PDF-ova, %d sa greškom, %d sa upozorenjima\n", files, failed, warned)
}

func detectFormat(text, formatFlag string) simulator.PDFFormat {
	switch formatFlag {
	case "fremco":
		return simulator.FormatFremco
	case "jetting":
		return simulator.FormatJetting
	case "", "auto":
		return simulator.DetectFormat(text)
	}
	return simulator.FormatUnknown
}

// parseProtocol vraća polja za prikaz, broj merenja i kompletan objekat.
func parseProtocol(text string, format simulator.PDFFormat) ([][2]string, int, any, error) {
	var (
		fields       [][2]string
		measurements int
		payload      any
	)

	switch format {
	case simulator.FormatFremco:
		p := simulator.ParseFremcoProtocol(simulator.NormalizeFremcoTxt(text))
		if p == nil {
			fail(fmt.Errorf("Fremco parser vratio nil"))
		}
		info := p.ProtocolInfo
		fields = [][2]string{
			{"date", info.Date},
			{"start_time", info.StartTime},
			{"project_number", info.ProjectNumber},
			{"section_nvt", info.SectionNVT},
			{"company", info.Company},
			{"service_provider", info.ServiceProvider},
			{"operator", info.Operator},
			{"remarks", info.Remarks},
		}
		measurements = len(p.Measurements.DataPoints)
		payload = p

	case simulator.FormatJetting:
		p := simulator.ParseJettingProtocol(simulator.NormalizeJettingTxt(text))
		if p == nil {
			fail(fmt.Errorf("Jetting parser vratio nil"))
		}
		info := p.ProtocolInfo
		op, pn := "", ""
		if info.Operator != nil {
			op = *info.Operator
		}
		if info.ProjectNumber != nil {
			pn = *info.ProjectNumber
		}
		fields = [][2]string{
			{"date", info.Date},
			{"start_time", info.StartTime},
			{"project_number", pn},
			{"section_nvt", info.SectionNVT},
			{"company", info.Company},
			{"service_provider", info.ServiceProvider},
			{"operator", op},
			{"remarks", info.Remarks},
		}
		measurements = len(p.Measurements.DataPoints)
		payload = p
	}

	if payload == nil {
		return nil, 0, nil, fmt.Errorf("nepoznat format %q", format)
	}
	return fields, measurements, payload, nil
}

func parseFields(text string, format simulator.PDFFormat) ([][2]string, int) {
	fields, measurements, _, err := parseProtocol(text, format)
	if err != nil {
		return nil, 0
	}
	return fields, measurements
}

// buildRenderModel parsira tekst u model za render.
func buildRenderModel(text string, format simulator.PDFFormat) (simulator.RenderModel, error) {
	switch format {
	case simulator.FormatFremco:
		p := simulator.ParseFremcoProtocol(simulator.NormalizeFremcoTxt(text))
		if p == nil {
			return simulator.RenderModel{}, fmt.Errorf("Fremco parser vratio nil")
		}
		return simulator.RenderModelFromFremco(p), nil
	case simulator.FormatJetting:
		p := simulator.ParseJettingProtocol(simulator.NormalizeJettingTxt(text))
		if p == nil {
			return simulator.RenderModel{}, fmt.Errorf("Jetting parser vratio nil")
		}
		return simulator.RenderModelFromJetting(p), nil
	}
	return simulator.RenderModel{}, fmt.Errorf("format nije prepoznat")
}

// verifyRoundTrip: parsira original, generiše PDF, ponovo ga parsira i poredi.
func verifyRoundTrip(in, text string, format simulator.PDFFormat, fontPath string) {
	want, err := buildRenderModel(text, format)
	if err != nil {
		fail(err)
	}

	tmp, err := os.CreateTemp("", "roundtrip-*.pdf")
	if err != nil {
		fail(err)
	}
	defer os.Remove(tmp.Name())

	if err := simulator.RenderProtocolPDF(tmp, want, fontPath); err != nil {
		fail(err)
	}
	tmp.Close()

	renderedText, err := runCapture("pdftotext", "-layout", tmp.Name(), "-")
	if err != nil {
		fail(err)
	}

	gotFormat := simulator.DetectFormat(renderedText)
	if gotFormat != format {
		fmt.Printf("format se promenio: %s -> %s\n", format, gotFormat)
	}
	got, err := buildRenderModel(renderedText, gotFormat)
	if err != nil {
		fail(err)
	}

	diffs := simulator.CompareRenderModels(want, got)
	fmt.Printf("file:        %s\n", filepath.Base(in))
	fmt.Printf("format:      %s\n", format)
	fmt.Printf("merenja:     %d -> %d\n", len(want.Rows), len(got.Rows))
	if len(diffs) == 0 {
		fmt.Println("round-trip:  OK (0 razlika)")
		return
	}
	fmt.Printf("round-trip:  %d razlika\n", len(diffs))
	for _, d := range diffs {
		fmt.Println("  -", d)
	}
	os.Exit(1)
}
