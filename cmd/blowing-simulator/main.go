package main

import (
	"blowing-simulator/internal/simulator"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"text/template"

	"github.com/jung-kurt/gofpdf"
	"github.com/ledongthuc/pdf"
)

var lastCSVExport string

// --- Normalization functions (Jetting & Fremco) ---

func NormalizeJettingTxt(raw string) string {
	raw = strings.ReplaceAll(raw, "\f", "\n")
	lines := strings.Split(raw, "\n")
	var normalizedLines []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			normalizedLines = append(normalizedLines, line)
		}
	}
	return strings.Join(normalizedLines, "\n")
}

func NormalizeFremcoTxt(raw string) string {
	raw = strings.ReplaceAll(raw, "\f", "\n")
	lines := strings.Split(raw, "\n")
	var cleaned []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return strings.Join(cleaned, "\n")
}

// --- Background Job Store ---

type JobStatus string

const (
	JobPending  JobStatus = "pending"
	JobRunning  JobStatus = "running"
	JobFinished JobStatus = "finished"
	JobFailed   JobStatus = "failed"
)

type JobResult struct {
	Status JobStatus
	Output string // Python script output or CSV file path
	Error  string // Error message, if any
}

var jobStore = struct {
	sync.RWMutex
	jobs map[string]*JobResult
}{jobs: make(map[string]*JobResult)}

// --- Start Report Job Handler ---

// --- Get Report Job Status Handler ---

func GetReportJobStatusHandler(w http.ResponseWriter, r *http.Request) {
	jobID := r.URL.Query().Get("jobID")
	jobStore.RLock()
	job, ok := jobStore.jobs[jobID]
	jobStore.RUnlock()
	if !ok {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"%s","output":%q,"error":%q}`, job.Status, job.Output, job.Error)
}

// --- PDF to Text Handler ---

// Index page handler (unchanged, but you may want to update frontend to use AJAX and poll job status)

// Create Report page handler
func CreateReportHandler(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("web/templates/create-report.html")
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, nil)
}

// Edit Report page handler
func EditReportHandler(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("web/templates/edit-report.html")
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, nil)
}

// Create Fremco Report page handler
func CreateFremcoHandler(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("web/templates/create-fremco.html")
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, nil)
}

// Create Jetting Report page handler
func CreateJettingHandler(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("web/templates/create-jetting.html")
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, nil)
}

func extractTextWithGoLib(pdfPath string) (string, error) {
	f, r, err := pdf.Open(pdfPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var buf bytes.Buffer
	b, err := r.GetPlainText()
	if err != nil {
		return "", err
	}
	_, err = buf.ReadFrom(b)
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Extract text using pdftotext CLI tool
func extractTextWithPdftotext(pdfPath string) (string, error) {
	txtPath := pdfPath + ".pdftotext.txt"
	cmd := "pdftotext"
	args := []string{"-layout", pdfPath, txtPath}
	if err := runCommand(cmd, args); err != nil {
		return "", err
	}
	data, err := os.ReadFile(txtPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Helper to run external command
func runCommand(cmd string, args []string) error {
	c := exec.Command(cmd, args...)
	return c.Run()
}

func Pdf2TextHandler(w http.ResponseWriter, r *http.Request) {
	tmpl := template.New("pdf2text.html").Funcs(template.FuncMap{
		"formatTime": func(t string) string {
			t = strings.TrimSpace(t)
			t = strings.ReplaceAll(t, " ", ".")
			return t
		},
	})
	tmpl, err := tmpl.ParseFiles("web/templates/pdf2text.html")
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if r.Method == http.MethodGet {
		tmpl.Execute(w, nil)
		return
	}
	// Handle POST (single file)
	file, header, err := r.FormFile("pdfFile")
	if err != nil {
		http.Error(w, "File upload error", http.StatusBadRequest)
		return
	}
	defer file.Close()
	
	// Ensure temp directory exists
	tempDir := os.TempDir()
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		http.Error(w, "Cannot create temp directory", http.StatusInternalServerError)
		return
	}
	
	tempPdf := filepath.Join(tempDir, header.Filename)
	out, err := os.Create(tempPdf)
	if err != nil {
		http.Error(w, "Cannot save PDF", http.StatusInternalServerError)
		return
	}
	defer out.Close()
	_, err = io.Copy(out, file)
	if err != nil {
		http.Error(w, "Cannot save PDF", http.StatusInternalServerError)
		return
	}
	baseName := strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
	// Support both Jetting and Fremco filename formats
	var date, time, address, nvt, project string
	if strings.Contains(baseName, ",") {
		// Jetting format: "29.10.2025, 11 20, Eiermarkt 15 B NVT 1V2200"
		parts := strings.Split(baseName, ",")
		if len(parts) >= 3 {
			date = strings.TrimSpace(parts[0])
			time = strings.TrimSpace(parts[1])
			rest := strings.TrimSpace(parts[2])
			nvtIdx := strings.Index(rest, "NVT ")
			if nvtIdx != -1 {
				address = strings.TrimSpace(rest[:nvtIdx])
				nvt = strings.TrimSpace(rest[nvtIdx+4:])
			} else {
				address = rest
			}
		}
		log.Printf("[Jetting] Parsed filename: %s | Date: %s | Time: %s | Address: %s | NVT: %s", baseName, date, time, address, nvt)
	} else {
		// Fremco format: "SM209214964_2025-10-22 10_51_Oldenburger Koppel_10_NVT1V3400"
		parts := strings.Split(baseName, "_")
		if len(parts) >= 4 {
			project = parts[0]
			
			// Parse date and time from second part "2025-10-22 10"
			dateTimePart := parts[1]
			if strings.Contains(dateTimePart, " ") {
				dateTimeFields := strings.Fields(dateTimePart)
				if len(dateTimeFields) >= 2 {
					// Date part: "2025-10-22"
					dateParts := strings.Split(dateTimeFields[0], "-")
					if len(dateParts) == 3 {
						date = dateParts[2] + "." + dateParts[1] + "." + dateParts[0] // Convert to DD.MM.YYYY
					}
					// Time part: combine hour from dateTimePart and minutes from parts[2]
					hour := dateTimeFields[1]
					minute := parts[2]
					time = hour + ":" + minute
				}
			}
			
			// Find NVT part (starts with "NVT")
			nvtIndex := -1
			for i, part := range parts {
				if strings.HasPrefix(part, "NVT") {
					nvtIndex = i
					nvt = part
					break
				}
			}
			
			// Address is everything between time part and NVT
			if nvtIndex > 3 {
				addressParts := parts[3:nvtIndex]
				address = strings.TrimSpace(strings.Join(addressParts, " "))
			}
		}
		log.Printf("[Fremco] Parsed filename: %s | Project: %s | Date: %s | Time: %s | Address: %s | NVT: %s", baseName, project, date, time, address, nvt)
	}

	tempTxt := filepath.Join(os.TempDir(), baseName+".txt")
	format := r.FormValue("format")
	var normalized string
	var jsonOutput []byte

	// Try Go-native extraction first
	text, err := extractTextWithGoLib(tempPdf)
	if err != nil {
		http.Error(w, "PDF extraction failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	err = os.WriteFile(tempTxt, []byte(text), 0644)
	if err != nil {
		http.Error(w, "Cannot save extracted text", http.StatusInternalServerError)
		return
	}
	raw, err := os.ReadFile(tempTxt)
	if err != nil {
		http.Error(w, "Cannot read extracted text", http.StatusInternalServerError)
		return
	}
	rawStr := string(raw)

	var measurements []simulator.SimpleMeasurement
	var jettingMeasurements []simulator.JettingMeasurement
	var pdfMeta map[string]string
	
	// Check for Fremco first (most specific indicators)
	if (strings.Contains(rawStr, "Streckenabschnitt") && strings.Contains(rawStr, "Einblasgerät")) || 
	   strings.Contains(rawStr, "Fremco") || 
	   (strings.Contains(rawStr, "Streckenlänge [m]") && strings.Contains(rawStr, "Geschwindigkeit [m/min]") && strings.Contains(rawStr, "Rohr-Druck [bar]") && strings.Contains(rawStr, "Drehmoment [%]")) {
		// Fremco format detected, use pdftotext extraction
		text, err := extractTextWithPdftotext(tempPdf)
		if err != nil {
			http.Error(w, "pdftotext extraction failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		normalized = simulator.NormalizeFremcoTxt(text)
		log.Println("Normalized text (Fremco/pdftotext):")
		log.Println(normalized)
		measurements = simulator.ParseFremcoSimple(normalized)
		log.Printf("Parsed measurements (Fremco): %+v\n", measurements)
		jsonOutput, _ = json.MarshalIndent(measurements, "", "  ")
		if string(jsonOutput) == "null" {
			jsonOutput = []byte("[]")
		}
		format = "fremco"
	} else if strings.Contains(rawStr, "Länge") && strings.Contains(rawStr, "Schubkraft") {
		// Old Jetting format detected (vertical block format), use Go-native extraction
		pdfMeta = simulator.ExtractJettingMetadata(rawStr)
		normalized = simulator.NormalizeJettingTxt(rawStr)
		log.Println("Normalized text (Old Jetting):")
		log.Println(normalized)
		jettingMeasurements = simulator.ParseJettingTxt(normalized)
		log.Printf("Parsed measurements (Old Jetting): %+v\n", jettingMeasurements)
		jsonOutput, _ = json.MarshalIndent(jettingMeasurements, "", "  ")
		if string(jsonOutput) == "null" {
			jsonOutput = []byte("[]")
		}
		format = "jetting"
	} else if strings.Contains(rawStr, "Streckenlänge") && strings.Contains(rawStr, "Drehmoment") && !strings.Contains(rawStr, "Fremco") {
		// New Jetting format detected (Messwerte Tabelle), use Go-native extraction
		pdfMeta = simulator.ExtractJettingMetadata(rawStr)
		normalized = simulator.NormalizeJettingTxt(rawStr)
		log.Println("Normalized text (New Jetting - Messwerte Tabelle):")
		log.Println(normalized)
		jettingMeasurements = simulator.ParseJettingTxt(normalized)
		log.Printf("Parsed measurements (New Jetting): %+v\n", jettingMeasurements)
		jsonOutput, _ = json.MarshalIndent(jettingMeasurements, "", "  ")
		if string(jsonOutput) == "null" {
			jsonOutput = []byte("[]")
		}
		format = "jetting"
	} else {
		// Fallback to user selection
		if format == "jetting" {
			normalized = simulator.NormalizeJettingTxt(rawStr)
			log.Println("Normalized text (Jetting fallback):")
			log.Println(normalized)
			jettingMeasurements = simulator.ParseJettingTxt(normalized)
			log.Printf("Parsed measurements (Jetting fallback): %+v\n", jettingMeasurements)
			jsonOutput, _ = json.MarshalIndent(jettingMeasurements, "", "  ")
		} else {
			// Fallback to Fremco, use pdftotext
			text, err := extractTextWithPdftotext(tempPdf)
			if err != nil {
				http.Error(w, "pdftotext extraction failed: "+err.Error(), http.StatusInternalServerError)
				return
			}
			normalized = simulator.NormalizeFremcoTxt(text)
			log.Println("Normalized text (Fremco fallback/pdftotext):")
			log.Println(normalized)
			measurements = simulator.ParseFremcoSimple(normalized)
			log.Printf("Parsed measurements (Fremco fallback): %+v\n", measurements)
			jsonOutput, _ = json.MarshalIndent(measurements, "", "  ")
		}
	}

	// Calculate last and sum
	var lastMeasurement map[string]interface{}
	var sumMeasurement map[string]interface{}
	var totalLength float64
	if len(measurements) > 0 {
		last := measurements[len(measurements)-1]
		lastMeasurement = map[string]interface{}{
			"SourceFile": "last",
			"Length":     last.Length,
			"Speed":      last.Speed,
			"Pressure":   last.Pressure,
			"Torque":     last.Torque,
			"Time":       "", // Add time if available
		}
		var totalSpeed, totalPressure, totalTorque float64
		for _, m := range measurements {
			totalLength += m.Length
			totalSpeed += m.Speed
			totalPressure += m.Pressure
			totalTorque += m.Torque
		}
		sumMeasurement = map[string]interface{}{
			"Length":   totalLength,
			"Speed":    totalSpeed,
			"Pressure": totalPressure,
			"Torque":   totalTorque,
		}
	}
	var fremcoMeta map[string]string
	if format == "fremco" {
		fremcoMeta = simulator.ExtractFremcoMetadata(normalized)
	}
	tmpl.Execute(w, map[string]interface{}{
		"Text":            rawStr,
		"Normalized":      normalized,
		"JSON":            string(jsonOutput),
		"Format":          format,
		"LastMeasurement": lastMeasurement,
		"SumMeasurement":  sumMeasurement,
		"TotalLength":     totalLength,
		"Date":            date,
		"Time":            time,
		"Address":         address,
		"NVT":             nvt,
		"Project":         project,
		"Filename":        header.Filename,
		"PDFDate":         pdfMeta["Date"],
		"PDFTime":         pdfMeta["Time"],
		"PDFAddress":      pdfMeta["Address"],
		"PDFNVT":          pdfMeta["NVT"],
		"FremcoMeta":      fremcoMeta,
	})
}

func IndexHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "web/templates/index.html")
}

func DownloadCSVHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	csvData := r.FormValue("csvdata")
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=report.csv")
	w.Write([]byte(csvData))
}

func DownloadPDFHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	csvData := r.FormValue("csvdata")
	reader := csv.NewReader(strings.NewReader(csvData))
	reader.Comma = ';'
	records, err := reader.ReadAll()
	if err != nil {
		http.Error(w, "Error parsing CSV for PDF", http.StatusInternalServerError)
		return
	}

	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.AddUTF8Font("DejaVu", "", "web/static/fonts/DejaVuSans.ttf")
	pdf.SetFont("DejaVu", "", 12)
	pdf.AddPage()

	// Table header
	for _, header := range records[0] {
		pdf.CellFormat(60, 10, header, "1", 0, "C", false, 0, "")
	}
	pdf.Ln(-1)

	// Table rows
	for _, row := range records[1:] {
		for _, cell := range row {
			pdf.CellFormat(60, 10, cell, "1", 0, "C", false, 0, "")
		}
		pdf.Ln(-1)
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "attachment; filename=report.pdf")
	err = pdf.Output(w)
	if err != nil {
		http.Error(w, "Error generating PDF", http.StatusInternalServerError)
		return
	}
}

func main() {
	http.HandleFunc("/", IndexHandler)
	http.HandleFunc("/download-json", DownloadJSONHandler)
	http.HandleFunc("/pdf2text", Pdf2TextHandler)
	http.HandleFunc("/create-report", CreateReportHandler)
	http.HandleFunc("/edit-report", EditReportHandler)
	http.HandleFunc("/create-fremco", CreateFremcoHandler)
	http.HandleFunc("/create-jetting", CreateJettingHandler)
	http.HandleFunc("/submit-jetting-report", SubmitJettingReportHandler)
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))
	http.HandleFunc("/jetting-report", JettingReportHandler)
	http.HandleFunc("/export-pdf", ExportPDFHandler)
	http.HandleFunc("/download-csv", DownloadCSVHandler)
	http.HandleFunc("/download-pdf", DownloadPDFHandler)
	http.HandleFunc("/export-csv", ExportCSVHandler)
	log.Println("Server started at http://0.0.0.0:8080/")
	http.ListenAndServe(":8080", nil)
}

// SubmitJettingReportHandler processes Jetting form and renders the report

// ExportCSVHandler serves the CSV summary for batch PDF uploads
func ExportCSVHandler(w http.ResponseWriter, r *http.Request) {
	if lastCSVExport == "" {
		http.Error(w, "No CSV available. Please process a batch first.", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment; filename=report.csv")
	w.Write([]byte(lastCSVExport))
}
func SubmitJettingReportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Forward the POST data to JettingReportHandler for rendering
	JettingReportHandler(w, r)
}

// ExportPDFHandler is a stub for Jetting report PDF export integration
func ExportPDFHandler(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement PDF export using wkhtmltopdf or similar
	http.Error(w, "PDF export not implemented yet.", http.StatusNotImplemented)
}

// Handler to render Jetting HTML report for preview/PDF export
func JettingReportHandler(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("web/templates/report/jetting-report.html")
	if err != nil {
		http.Error(w, "Template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// If GET, use dummy data for preview/testing
	if r.Method == http.MethodGet {
		data := map[string]interface{}{
			"Bauvorhaben":                "SM209214964",
			"Streckenabschnitt":          "Mühlenstr 19 B NVT 1V2800",
			"Firma":                      "M.A.X. Bauservice GmbH",
			"Einblaeser":                 "Vladimir S",
			"Bemerkungen":                "",
			"ZeitDauer":                  "00:12:01",
			"DatumUhrzeit":               "30.10.2025, 16:24",
			"GPS":                        "52.4914°;9.85174°;95.8000 m",
			"RohrHersteller":             "Gabocom",
			"KabelHersteller":            "Yangtze Optical Fibre and Cab",
			"Einblasgeraet":              "MJET V1/V2",
			"Rohrtyp":                    "7x1,5",
			"Kabeltyp":                   "A-D2Y 1x4 E(GYAXY-4B6a1)",
			"Gleitmittel":                "micro",
			"FarbeKennung":               "Gelb",
			"KabelDurchmesser":           "2,4mm",
			"Faseranzahl":                "4",
			"Kompressor":                 "jetair141000",
			"Verlegungsart":              "",
			"KabeltrommelNr":             "E9/125",
			"Oelabscheider":              false,
			"Nachkuehler":                false,
			"Kabeltemperatur":            "11°C",
			"KabeleinblaskappJa":         true,
			"KabeleinblaskappNein":       false,
			"CrashTestJa":                false,
			"CrashTestNein":              true,
			"Rohrinnendurchmesser":       "4",
			"Rohraussendurchmesser":      "7",
			"Rohrtemperatur":             "11°C",
			"RohrinnenwandGlatt":         true,
			"RohrinnenwandGerieft":       false,
			"Schubkraft":                 "",
			"SicherheitsabschaltungJa":   false,
			"SicherheitsabschaltungNein": true,
			"Start":                      "3012",
			"Ende":                       "2523",
			"Streckenlaenge":             "489",
			"UmgebungsluftTemp":          "0.0°",
			"Luftfeuchtigkeit":           "0.0 %",
			"ReportID":                   "demo123",
		}
		tmpl.Execute(w, data)
		return
	}
	// If POST, use submitted form data
	if r.Method == http.MethodPost {
		r.ParseForm()
		data := map[string]interface{}{
			"Bauvorhaben":                r.FormValue("bauvorhaben"),
			"Streckenabschnitt":          r.FormValue("streckenabschnitt"),
			"Firma":                      r.FormValue("firma"),
			"Einblaeser":                 r.FormValue("einblaeser"),
			"Bemerkungen":                r.FormValue("bemerkungen"),
			"ZeitDauer":                  r.FormValue("zeit_dauer"),
			"DatumUhrzeit":               r.FormValue("datum") + ", " + r.FormValue("uhrzeit"),
			"GPS":                        r.FormValue("gps"),
			"RohrHersteller":             r.FormValue("rohr_hersteller"),
			"KabelHersteller":            r.FormValue("kabel_hersteller"),
			"Einblasgeraet":              r.FormValue("einblaeser"),
			"Rohrtyp":                    r.FormValue("rohrtyp"),
			"Kabeltyp":                   r.FormValue("kabeltyp"),
			"Gleitmittel":                r.FormValue("gleitmittel"),
			"FarbeKennung":               r.FormValue("farbe_kennung"),
			"KabelDurchmesser":           r.FormValue("kabel_durchmesser"),
			"Faseranzahl":                r.FormValue("faseranzahl"),
			"Kompressor":                 r.FormValue("kompressor"),
			"Verlegungsart":              r.FormValue("verlegungsart"),
			"KabeltrommelNr":             r.FormValue("kabeltrommel_nr"),
			"Oelabscheider":              r.FormValue("oelabscheider") == "on",
			"Nachkuehler":                r.FormValue("nachkuehler") == "on",
			"Kabeltemperatur":            r.FormValue("kabeltemperatur"),
			"KabeleinblaskappJa":         r.FormValue("kabeleinblaskapp") == "ja",
			"KabeleinblaskappNein":       r.FormValue("kabeleinblaskapp") == "nein",
			"CrashTestJa":                r.FormValue("crashtest") == "ja",
			"CrashTestNein":              r.FormValue("crashtest") == "nein",
			"Rohrinnendurchmesser":       r.FormValue("rohrinnendurchmesser"),
			"Rohraussendurchmesser":      r.FormValue("rohraussendurchmesser"),
			"Rohrtemperatur":             r.FormValue("rohrtemperatur"),
			"RohrinnenwandGlatt":         r.FormValue("rohrinnenwand") == "glatt",
			"RohrinnenwandGerieft":       r.FormValue("rohrinnenwand") == "gerieft",
			"Schubkraft":                 r.FormValue("schubkraft"),
			"SicherheitsabschaltungJa":   r.FormValue("sicherheitsabschaltung") == "ja",
			"SicherheitsabschaltungNein": r.FormValue("sicherheitsabschaltung") == "nein",
			"Start":                      r.FormValue("start"),
			"Ende":                       r.FormValue("ende"),
			"Streckenlaenge":             r.FormValue("streckenlaenge"),
			"UmgebungsluftTemp":          r.FormValue("umgebungsluft_temp"),
			"Luftfeuchtigkeit":           r.FormValue("luftfeuchtigkeit"),
			"ReportID":                   "submitted",
		}
		tmpl.Execute(w, data)
		return
	}
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}
