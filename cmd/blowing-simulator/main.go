package main

import (
	"blowing-simulator/internal/simulator"
	"bytes"
	"database/sql"
	"database/sql/driver"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/jung-kurt/gofpdf"
	"github.com/jmoiron/sqlx"
	"github.com/ledongthuc/pdf"
	_ "github.com/lib/pq"
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
			timeRaw := strings.TrimSpace(parts[1])
			// Convert space-separated time to colon-separated (e.g., "11 09" -> "11:09")
			if strings.Contains(timeRaw, " ") {
				timeParts := strings.Fields(timeRaw)
				if len(timeParts) == 2 {
					time = timeParts[0] + ":" + timeParts[1]
				}
			} else {
				time = timeRaw
			}
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
	var fremcoProtocol *simulator.FremcoProtocol
	var jettingProtocol *simulator.JettingProtocol
	
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
		
		// Parse comprehensive protocol data
		fremcoProtocol = simulator.ParseFremcoProtocol(normalized)
		// Fill in filename-based metadata
		if fremcoProtocol != nil {
			fremcoProtocol.ProtocolInfo.ProjectNumber = project
			fremcoProtocol.ProtocolInfo.Date = date
			fremcoProtocol.ProtocolInfo.StartTime = time
			fremcoProtocol.ProtocolInfo.SectionNVT = address + " / " + nvt
			fremcoProtocol.ExportMetadata.SourceFilename = header.Filename
		}
		
		measurements = simulator.ParseFremcoSimple(normalized)
		log.Printf("Parsed measurements (Fremco): %+v\n", measurements)
		log.Printf("Comprehensive protocol (Fremco): %+v\n", fremcoProtocol)
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
		
		// Parse comprehensive protocol data
		jettingProtocol = simulator.ParseJettingProtocol(normalized)
		// Fill in filename-based metadata
		if jettingProtocol != nil {
			jettingProtocol.ProtocolInfo.Date = date
			jettingProtocol.ProtocolInfo.StartTime = time
			jettingProtocol.ProtocolInfo.SectionNVT = address + " / " + nvt
			jettingProtocol.ExportMetadata.SourceFilename = header.Filename
		}
		
		jettingMeasurements = simulator.ParseJettingTxt(normalized)
		log.Printf("Parsed measurements (Old Jetting): %+v\n", jettingMeasurements)
		log.Printf("Comprehensive protocol (Jetting): %+v\n", jettingProtocol)
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

	// Save protocol data to database
	var protocolID int
	var dbErr error
	if fremcoProtocol != nil {
		protocolID, dbErr = SaveFremcoProtocol(db, fremcoProtocol)
		if dbErr != nil {
			log.Printf("Failed to save Fremco protocol to database: %v", dbErr)
		} else {
			log.Printf("Successfully saved Fremco protocol with ID: %d", protocolID)
		}
	} else if jettingProtocol != nil {
		protocolID, dbErr = SaveJettingProtocol(db, jettingProtocol)
		if dbErr != nil {
			log.Printf("Failed to save Jetting protocol to database: %v", dbErr)
		} else {
			log.Printf("Successfully saved Jetting protocol with ID: %d", protocolID)
		}
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
	// Initialize database connection
	var err error
	dbHost := getEnvOrDefault("DB_HOST", "localhost")
	dbPort := getEnvOrDefault("DB_PORT", "5432")
	dbUser := getEnvOrDefault("DB_USER", "blowing")
	dbPassword := getEnvOrDefault("DB_PASSWORD", "7fG2vQp9sXw3Lk8r")
	dbName := getEnvOrDefault("DB_NAME", "blowing_simulator")
	
	connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		dbHost, dbPort, dbUser, dbPassword, dbName)
	
	db, err = sqlx.Open("postgres", connStr)
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}
	defer db.Close()
	
	// Test database connection
	if err = db.Ping(); err != nil {
		log.Fatal("Failed to ping database:", err)
	}
	
	log.Println("Successfully connected to database")
	
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
	http.HandleFunc("/protocols", ProtocolsHandler)
	http.HandleFunc("/protocols/view", ViewProtocolHandler)
	http.HandleFunc("/protocols/measurements", ProtocolMeasurementsHandler)
	http.HandleFunc("/protocols/length-report", LengthReportHandler)
	http.HandleFunc("/bulk-upload", BulkUploadHandler)
	http.HandleFunc("/debug-pdf", DebugPDFHandler)
	http.HandleFunc("/health", HealthCheckHandler)
	log.Println("Server started at http://0.0.0.0:8080/")
	log.Println("Available routes:")
	log.Println("  GET /")
	log.Println("  GET /protocols")
	log.Println("  GET /protocols/view?id=X")
	log.Println("  GET /protocols/measurements?id=X")
	log.Println("  GET /health")
	http.ListenAndServe(":8080", nil)
}

// Protocol represents a protocol record for display
type Protocol struct {
	ID              int            `db:"id"`
	ProtocolType    string         `db:"protocol_type"`
	SystemName      sql.NullString `db:"system_name"`
	ProtocolDate    sql.NullString `db:"protocol_date"`
	StartTime       sql.NullString `db:"start_time"`
	ProjectNumber   sql.NullString `db:"project_number"`
	Company         sql.NullString `db:"company"`
	ServiceProvider sql.NullString `db:"service_provider"`
	Operator        sql.NullString `db:"operator"`
	SourceFilename  sql.NullString `db:"source_filename"`
	CreatedAt       string         `db:"created_at"`
}

// NullTime represents a time.Time that may be null
type NullTime struct {
	Time  time.Time
	Valid bool
}

// Scan implements the Scanner interface
func (nt *NullTime) Scan(value interface{}) error {
	if value == nil {
		nt.Time, nt.Valid = time.Time{}, false
		return nil
	}
	nt.Valid = true
	return convertAssign(&nt.Time, value)
}

// Value implements the driver Valuer interface
func (nt NullTime) Value() (driver.Value, error) {
	if !nt.Valid {
		return nil, nil
	}
	return nt.Time, nil
}

// convertAssign is a helper function to convert values
func convertAssign(dest, src interface{}) error {
	switch s := src.(type) {
	case time.Time:
		switch d := dest.(type) {
		case *time.Time:
			*d = s
		}
	case string:
		switch d := dest.(type) {
		case *time.Time:
			var err error
			*d, err = time.Parse(time.RFC3339, s)
			return err
		}
	}
	return nil
}

// ProtocolMeasurement represents a measurement data point for display
type ProtocolMeasurement struct {
	ID               int             `db:"id"`
	SequenceNumber   sql.NullInt64   `db:"sequence_number"`
	LengthM          sql.NullFloat64 `db:"length_m"`
	TimestampValue   NullTime        `db:"timestamp_value"`
	SpeedMMin        sql.NullFloat64 `db:"speed_m_min"`
	PressureBar      sql.NullFloat64 `db:"pressure_bar"`
	TorquePercent    sql.NullFloat64 `db:"torque_percent"`
	TemperatureC     sql.NullFloat64 `db:"temperature_c"`
	ForceN           sql.NullFloat64 `db:"force_n"`
	TimeDuration     sql.NullString  `db:"time_duration"`
	CreatedAt        string          `db:"created_at"`
}

// LengthReportData represents aggregated length data for reporting
type LengthReportData struct {
	ProtocolID       int             `db:"protocol_id"`
	ProtocolType     string          `db:"protocol_type"`
	ProtocolDate     sql.NullString  `db:"protocol_date"`
	Company          sql.NullString  `db:"company"`
	ServiceProvider  sql.NullString  `db:"service_provider"`
	SourceFilename   sql.NullString  `db:"source_filename"`
	MaxLength        sql.NullFloat64 `db:"max_length"`
	MinLength        sql.NullFloat64 `db:"min_length"`
	AvgLength        sql.NullFloat64 `db:"avg_length"`
	MeasurementCount int             `db:"measurement_count"`
	TotalLength      sql.NullFloat64 `db:"total_length"`
	CreatedAt        string          `db:"created_at"`
}

// ProtocolsHandler displays list of imported protocols with search functionality
func ProtocolsHandler(w http.ResponseWriter, r *http.Request) {
	// Get search parameters
	search := r.URL.Query().Get("search")
	protocolType := r.URL.Query().Get("type")
	
	// Build query
	query := `
		SELECT id, protocol_type, system_name, protocol_date, start_time, 
		       project_number, company, service_provider, operator, 
		       source_filename, created_at::text 
		FROM protocols 
		WHERE 1=1`
	
	args := []interface{}{}
	argCount := 0
	
	if search != "" {
		argCount++
		query += fmt.Sprintf(" AND (COALESCE(company,'') ILIKE $%d OR COALESCE(service_provider,'') ILIKE $%d OR COALESCE(source_filename,'') ILIKE $%d OR COALESCE(project_number,'') ILIKE $%d)", argCount, argCount, argCount, argCount)
		args = append(args, "%"+search+"%")
	}
	
	if protocolType != "" && protocolType != "all" {
		argCount++
		query += fmt.Sprintf(" AND protocol_type = $%d", argCount)
		args = append(args, protocolType)
	}
	
	query += " ORDER BY created_at DESC LIMIT 100"
	
	// Execute query
	var protocols []Protocol
	err := db.Select(&protocols, query, args...)
	if err != nil {
		http.Error(w, "Error fetching protocols: "+err.Error(), http.StatusInternalServerError)
		return
	}
	
	// Get total count
	countQuery := "SELECT COUNT(*) FROM protocols WHERE 1=1"
	if search != "" {
		countQuery += " AND (COALESCE(company,'') ILIKE $1 OR COALESCE(service_provider,'') ILIKE $1 OR COALESCE(source_filename,'') ILIKE $1 OR COALESCE(project_number,'') ILIKE $1)"
		args = []interface{}{"%"+search+"%"}
		if protocolType != "" && protocolType != "all" {
			countQuery += " AND protocol_type = $2"
			args = append(args, protocolType)
		}
	} else if protocolType != "" && protocolType != "all" {
		countQuery += " AND protocol_type = $1"
		args = []interface{}{protocolType}
	} else {
		args = []interface{}{}
	}
	
	var totalCount int
	err = db.Get(&totalCount, countQuery, args...)
	if err != nil {
		totalCount = 0
	}
	
	// Render template
	tmpl := template.Must(template.ParseFiles("web/templates/protocols.html"))
	data := map[string]interface{}{
		"Protocols":    protocols,
		"Search":       search,
		"Type":         protocolType,
		"TotalCount":   totalCount,
		"ResultCount":  len(protocols),
	}
	
	err = tmpl.Execute(w, data)
	if err != nil {
		http.Error(w, "Error rendering template: "+err.Error(), http.StatusInternalServerError)
		return
	}
}

// ViewProtocolHandler displays detailed view of a specific protocol
func ViewProtocolHandler(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		http.Error(w, "Protocol ID required", http.StatusBadRequest)
		return
	}
	
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "Invalid protocol ID", http.StatusBadRequest)
		return
	}
	
	// Get protocol info
	var protocol Protocol
	err = db.Get(&protocol, `
		SELECT id, protocol_type, system_name, protocol_date, start_time, 
		       project_number, company, service_provider, operator, 
		       source_filename, created_at::text 
		FROM protocols WHERE id = $1`, id)
	
	if err != nil {
		http.Error(w, "Protocol not found: "+err.Error(), http.StatusNotFound)
		return
	}
	
	// Get equipment info
	var equipment map[string]interface{}
	equipmentRows, err := db.Query(`
		SELECT device_model, controller_sn, compressor_model, pipe_manufacturer, 
		       cable_manufacturer, cable_fiber_count, cable_diameter 
		FROM protocol_equipment WHERE protocol_id = $1`, id)
	if err == nil {
		defer equipmentRows.Close()
		if equipmentRows.Next() {
			var deviceModel, controllerSN, compressorModel, pipeManuf, cableManuf sql.NullString
			var fiberCount sql.NullInt64
			var cableDiam sql.NullFloat64
			
			equipmentRows.Scan(&deviceModel, &controllerSN, &compressorModel, 
				&pipeManuf, &cableManuf, &fiberCount, &cableDiam)
			
			equipment = map[string]interface{}{
				"DeviceModel":      deviceModel.String,
				"ControllerSN":     controllerSN.String,
				"CompressorModel":  compressorModel.String,
				"PipeManufacturer": pipeManuf.String,
				"CableManufacturer": cableManuf.String,
				"FiberCount":       fiberCount.Int64,
				"CableDiameter":    cableDiam.Float64,
			}
		}
	}
	
	// Get summary info
	var summary map[string]interface{}
	summaryRows, err := db.Query(`
		SELECT total_distance, blowing_time, weather_temperature, 
		       weather_humidity, gps_latitude, gps_longitude 
		FROM protocol_summary WHERE protocol_id = $1`, id)
	if err == nil {
		defer summaryRows.Close()
		if summaryRows.Next() {
			var totalDist sql.NullInt64
			var blowingTime sql.NullString
			var weatherTemp, weatherHum, gpsLat, gpsLon sql.NullFloat64
			
			summaryRows.Scan(&totalDist, &blowingTime, &weatherTemp, 
				&weatherHum, &gpsLat, &gpsLon)
			
			summary = map[string]interface{}{
				"TotalDistance":      totalDist.Int64,
				"BlowingTime":        blowingTime.String,
				"WeatherTemperature": weatherTemp.Float64,
				"WeatherHumidity":    weatherHum.Float64,
				"GPSLatitude":        gpsLat.Float64,
				"GPSLongitude":       gpsLon.Float64,
			}
		}
	}
	
	// Get measurements count
	var measurementCount int
	db.Get(&measurementCount, "SELECT COUNT(*) FROM protocol_measurements WHERE protocol_id = $1", id)
	
	// Render template
	tmpl := template.Must(template.ParseFiles("web/templates/protocol-detail.html"))
	data := map[string]interface{}{
		"Protocol":         protocol,
		"Equipment":        equipment,
		"Summary":          summary,
		"MeasurementCount": measurementCount,
	}
	
	err = tmpl.Execute(w, data)
	if err != nil {
		http.Error(w, "Error rendering template: "+err.Error(), http.StatusInternalServerError)
		return
	}
}

// ProtocolMeasurementsHandler displays detailed measurement data for a protocol
func ProtocolMeasurementsHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("ProtocolMeasurementsHandler called with URL: %s, Query: %s", r.URL.Path, r.URL.RawQuery)
	
	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		log.Printf("Protocol ID missing from request: %v", r.URL.Query())
		http.Error(w, "Protocol ID required", http.StatusBadRequest)
		return
	}
	
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "Invalid protocol ID", http.StatusBadRequest)
		return
	}
	
	// Get protocol info
	var protocol Protocol
	err = db.Get(&protocol, `
		SELECT id, protocol_type, system_name, protocol_date, start_time, 
		       project_number, company, service_provider, operator, 
		       source_filename, created_at::text 
		FROM protocols WHERE id = $1`, id)
	
	if err != nil {
		http.Error(w, "Protocol not found: "+err.Error(), http.StatusNotFound)
		return
	}
	
	// Get pagination parameters
	page := 1
	if pageStr := r.URL.Query().Get("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}
	
	limit := 50 // measurements per page
	offset := (page - 1) * limit
	
	// Get measurements with pagination
	var measurements []ProtocolMeasurement
	err = db.Select(&measurements, `
		SELECT id, sequence_number, length_m, timestamp_value, speed_m_min, 
		       pressure_bar, torque_percent, temperature_c, force_n, 
		       time_duration, created_at::text 
		FROM protocol_measurements 
		WHERE protocol_id = $1 
		ORDER BY COALESCE(sequence_number, id) ASC 
		LIMIT $2 OFFSET $3`, id, limit, offset)
	
	if err != nil {
		http.Error(w, "Error fetching measurements: "+err.Error(), http.StatusInternalServerError)
		return
	}
	
	// Get total measurement count
	var totalCount int
	err = db.Get(&totalCount, "SELECT COUNT(*) FROM protocol_measurements WHERE protocol_id = $1", id)
	if err != nil {
		totalCount = 0
	}
	
	// Calculate pagination info
	totalPages := (totalCount + limit - 1) / limit
	hasNext := page < totalPages
	hasPrev := page > 1
	
	// Create template with custom functions
	funcMap := template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
	}
	tmpl := template.Must(template.New("protocol-measurements.html").Funcs(funcMap).ParseFiles("web/templates/protocol-measurements.html"))
	data := map[string]interface{}{
		"Protocol":     protocol,
		"Measurements": measurements,
		"CurrentPage":  page,
		"TotalPages":   totalPages,
		"TotalCount":   totalCount,
		"HasNext":      hasNext,
		"HasPrev":      hasPrev,
		"NextPage":     page + 1,
		"PrevPage":     page - 1,
		"Limit":        limit,
	}
	
	err = tmpl.Execute(w, data)
	if err != nil {
		http.Error(w, "Error rendering template: "+err.Error(), http.StatusInternalServerError)
		return
	}
}

// HealthCheckHandler provides a simple health check endpoint
func HealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Health check requested from: %s", r.RemoteAddr)
	
	// Test database connection
	if err := db.Ping(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, "Database connection failed: %v", err)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status": "healthy", "timestamp": "%s", "routes": ["/", "/protocols", "/protocols/view", "/protocols/measurements", "/protocols/length-report"]}`, time.Now().Format(time.RFC3339))
}

// LengthReportHandler displays length reports with date filtering and format selection
func LengthReportHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("LengthReportHandler called with URL: %s, Query: %s", r.URL.Path, r.URL.RawQuery)
	
	// Get filter parameters
	startDate := r.URL.Query().Get("start_date")
	endDate := r.URL.Query().Get("end_date")
	includeFremco := r.URL.Query().Get("fremco") == "on" || r.URL.Query().Get("fremco") == "true"
	includeJetting := r.URL.Query().Get("jetting") == "on" || r.URL.Query().Get("jetting") == "true"
	
	// Default to both if none selected
	if !includeFremco && !includeJetting {
		includeFremco = true
		includeJetting = true
	}
	
	// Build query
	query := `
		SELECT 
			p.id as protocol_id,
			p.protocol_type,
			p.protocol_date,
			p.company,
			p.service_provider,
			p.source_filename,
			COALESCE(MAX(m.length_m), 0) as max_length,
			COALESCE(MIN(m.length_m), 0) as min_length,
			COALESCE(AVG(m.length_m), 0) as avg_length,
			COUNT(m.id) as measurement_count,
			COALESCE(SUM(m.length_m), 0) as total_length,
			p.created_at::text
		FROM protocols p
		LEFT JOIN protocol_measurements m ON p.id = m.protocol_id
		WHERE 1=1`
	
	args := []interface{}{}
	argCount := 0
	
	// Add date filters
	if startDate != "" {
		argCount++
		query += fmt.Sprintf(" AND p.protocol_date >= $%d", argCount)
		args = append(args, startDate)
	}
	
	if endDate != "" {
		argCount++
		query += fmt.Sprintf(" AND p.protocol_date <= $%d", argCount)
		args = append(args, endDate)
	}
	
	// Add format filters
	var formatConditions []string
	if includeFremco {
		formatConditions = append(formatConditions, "p.protocol_type = 'fremco'")
	}
	if includeJetting {
		formatConditions = append(formatConditions, "p.protocol_type = 'jetting'")
	}
	
	if len(formatConditions) > 0 {
		query += " AND (" + strings.Join(formatConditions, " OR ") + ")"
	}
	
	query += `
		GROUP BY p.id, p.protocol_type, p.protocol_date, p.company, 
		         p.service_provider, p.source_filename, p.created_at
		ORDER BY p.protocol_date DESC, p.created_at DESC`
	
	// Execute query
	var reports []LengthReportData
	err := db.Select(&reports, query, args...)
	if err != nil {
		http.Error(w, "Error fetching length report: "+err.Error(), http.StatusInternalServerError)
		return
	}
	
	// Calculate totals
	var totalMeasurementCount int     // Count of all measurement records
	var totalMaxLengthSum float64     // Sum of maximum lengths from each protocol
	var totalProtocols = len(reports)
	var maxLengthOverall float64
	
	for _, report := range reports {
		totalMeasurementCount += report.MeasurementCount
		if report.MaxLength.Valid && report.MaxLength.Float64 > maxLengthOverall {
			maxLengthOverall = report.MaxLength.Float64
		}
		// Add up the maximum length from each protocol
		if report.MaxLength.Valid {
			totalMaxLengthSum += report.MaxLength.Float64
		}
	}
	
	// Overall average is average of maximum lengths from each protocol
	var overallAverage float64
	if totalProtocols > 0 {
		// Calculate average of max lengths (same as totalMaxLengthSum / number of protocols)
		overallAverage = totalMaxLengthSum / float64(totalProtocols)
	}
	
	// Render template
	funcMap := template.FuncMap{
		"dateFormat": func(dateStr string) string {
			// Accepts date in YYYY-MM-DD or RFC3339, returns DD.MM.YYYY
			if len(dateStr) >= 10 {
				parts := strings.Split(dateStr, "-")
				if len(parts) == 3 {
					return parts[2] + "." + parts[1] + "." + parts[0]
				}
				// Try RFC3339
				if strings.Contains(dateStr, "T") {
					dateOnly := strings.Split(dateStr, "T")[0]
					parts = strings.Split(dateOnly, "-")
					if len(parts) == 3 {
						return parts[2] + "." + parts[1] + "." + parts[0]
					}
				}
			}
			return dateStr
		},
		"extractNVT": func(filename string) string {
			// Extract NVT number from filename, e.g. "NVT 1V2800" or similar
			re := regexp.MustCompile(`(?i)(NVT\s*\w+)`)
			match := re.FindStringSubmatch(filename)
			if len(match) > 1 {
				return match[1]
			}
			return ""
		},
	}
	tmpl := template.Must(template.New("length-report.html").Funcs(funcMap).ParseFiles("web/templates/length-report.html"))
	data := map[string]interface{}{
		"Reports":           reports,
		"StartDate":         startDate,
		"EndDate":           endDate,
		"IncludeFremco":     includeFremco,
		"IncludeJetting":    includeJetting,
		"TotalProtocols":    totalProtocols,
		"TotalMeasurements": totalMaxLengthSum,     // Now shows sum of max lengths
		"MaxLengthOverall":  maxLengthOverall,
		"OverallAverage":    overallAverage,
	}

	err = tmpl.Execute(w, data)
	if err != nil {
		http.Error(w, "Error rendering template: "+err.Error(), http.StatusInternalServerError)
		return
	}
}

// Helper function to get environment variable with default
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// isJettingFormatWithFilename detects format using both content and filename
func isJettingFormatWithFilename(text string, filename string) bool {
	// Check filename patterns first - strong indicator
	filenameHints := 0
	
	// German Jetting filename pattern: "DD.MM.YYYY, HH MM, Location NVT XXXXX.pdf"
	if strings.Contains(filename, "NVT") && strings.Contains(filename, ",") {
		filenameHints += 3 // Strong Jetting indicator
	}
	
	// Date pattern: DD.MM.YYYY
	if matched, _ := regexp.MatchString(`\d{2}\.\d{2}\.\d{4}`, filename); matched {
		filenameHints++
	}
	
	// Time pattern: HH MM
	if matched, _ := regexp.MatchString(`\d{2} \d{2}`, filename); matched {
		filenameHints++
	}
	
	return isJettingFormat(text) || filenameHints >= 3
}

// isJettingFormat detects if the text is from a Jetting protocol
func isJettingFormat(text string) bool {
	// Check for Jetting-specific indicators (more comprehensive)
	jettingIndicators := 0
	
	// German language indicators (primary)
	if strings.Contains(text, "Streckenlänge") { jettingIndicators += 2 }
	if strings.Contains(text, "Geschwindigkeit") { jettingIndicators += 2 }
	if strings.Contains(text, "Drehmoment") { jettingIndicators += 2 }
	if strings.Contains(text, "Schubkraft") { jettingIndicators += 2 }
	if strings.Contains(text, "Uhrzeit") { jettingIndicators += 1 }
	
	// Common patterns
	if strings.Contains(text, "Länge") && strings.Contains(text, "m/min") { jettingIndicators += 2 }
	if strings.Contains(text, "[m]") && strings.Contains(text, "[bar]") { jettingIndicators += 1 }
	
	// Additional German indicators
	if strings.Contains(text, "Rohr-Druck") { jettingIndicators += 2 }
	if strings.Contains(text, "Lufttemperatur") { jettingIndicators += 1 }
	if strings.Contains(text, "Einblasdruck") { jettingIndicators += 1 }
	
	// Time format patterns (German)
	if strings.Contains(text, "[hh:mm:ss]") { jettingIndicators++ }
	if strings.Contains(text, "Zeit - Dauer") { jettingIndicators++ }
	
	// Unit patterns
	if strings.Contains(text, "[°C]") { jettingIndicators++ }
	if strings.Contains(text, "[N]") { jettingIndicators++ }
	if strings.Contains(text, "[%]") { jettingIndicators++ }
	
	// Additional fallback patterns for difficult-to-detect files
	if strings.Contains(text, "Protokoll") && (strings.Contains(text, "Meter") || strings.Contains(text, "Zeit")) {
		jettingIndicators++
	}
	
	// Very generic patterns as last resort
	if strings.Contains(text, "bar") && strings.Contains(text, "min") {
		jettingIndicators++
	}
	
	// If text contains measurement-like patterns
	if matched, _ := regexp.MatchString(`\d+[.,]\d+\s*(m|bar|°C|N|%)`, text); matched {
		jettingIndicators++
	}
	
	// Exclude Fremco files (strong exclusion)
	if strings.Contains(text, "Fremco") { return false }
	if strings.Contains(text, "SpeedNet") { return false }
	if strings.Contains(text, "MicroFlow") { return false }
	
	// Very low threshold - be more permissive
	return jettingIndicators >= 1
}

// isFremcoFormat detects if the text is from a Fremco protocol
func isFremcoFormat(text string) bool {
	// Check for Fremco-specific indicators (prioritized detection)
	fremcoIndicators := 0
	
	// Primary indicators
	if strings.Contains(text, "Fremco") { fremcoIndicators += 3 }
	if strings.Contains(text, "SpeedNet") { fremcoIndicators += 2 }
	if strings.Contains(text, "MicroFlow") { fremcoIndicators += 2 }
	
	// Secondary indicators
	if strings.Contains(text, "Blowing distance") { fremcoIndicators++ }
	if strings.Contains(text, "Blowing time") { fremcoIndicators++ }
	if strings.Contains(text, "Streckenabschnitt") { fremcoIndicators++ }
	if strings.Contains(text, "Equipment") && strings.Contains(text, "Serial") { fremcoIndicators++ }
	
	return fremcoIndicators >= 2
}

// truncateString helper function for logging
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// DebugPDFHandler helps debug PDF format detection issues
func DebugPDFHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`
<!DOCTYPE html>
<html>
<head><title>PDF Debug Tool</title></head>
<body>
<h1>PDF Format Detection Debug</h1>
<form method="POST" enctype="multipart/form-data">
    <input type="file" name="pdfFile" accept=".pdf" required>
    <button type="submit">Debug PDF</button>
</form>
</body>
</html>`))
		return
	}

	// Parse the uploaded file
	err := r.ParseMultipartForm(10 << 20) // 10MB limit
	if err != nil {
		http.Error(w, "Error parsing form: "+err.Error(), http.StatusBadRequest)
		return
	}

	files := r.MultipartForm.File["pdfFile"]
	if len(files) == 0 {
		http.Error(w, "No file provided", http.StatusBadRequest)
		return
	}

	file := files[0]
	f, err := file.Open()
	if err != nil {
		http.Error(w, "Error opening file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()

	// Read file content
	content, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, "Error reading file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Debug the processing
	result := debugPDFProcessing(file.Filename, content)

	// Return JSON response
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// debugPDFProcessing provides detailed debugging information
func debugPDFProcessing(filename string, content []byte) map[string]interface{} {
	result := map[string]interface{}{
		"filename": filename,
		"fileSize": len(content),
	}

	// Basic validation
	if len(content) < 100 {
		result["error"] = "File too small"
		return result
	}

	if !strings.HasPrefix(string(content[:4]), "%PDF") {
		result["error"] = "Invalid PDF header"
		return result
	}

	// Create temp file and extract text
	tempFile, err := os.CreateTemp("", "debug_*.pdf")
	if err != nil {
		result["error"] = "Failed to create temp file: " + err.Error()
		return result
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	_, err = tempFile.Write(content)
	if err != nil {
		result["error"] = "Failed to write temp file: " + err.Error()
		return result
	}
	tempFile.Close()

	// Try pdftotext first
	cmd := exec.Command("pdftotext", "-layout", tempFile.Name(), "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	extractMethod := "pdftotext"
	err = cmd.Run()
	if err != nil {
		result["pdftotext_error"] = stderr.String()
		// Try Go library
		text, err := extractTextWithGoLib(tempFile.Name())
		if err != nil {
			result["go_lib_error"] = err.Error()
			result["extraction_failed"] = true
			return result
		}
		stdout.WriteString(text)
		extractMethod = "go-library"
	}

	rawText := stdout.String()
	result["extraction_method"] = extractMethod
	result["text_length"] = len(rawText)
	result["text_sample"] = truncateString(rawText, 1000)

	// Format detection analysis
	isJetting := isJettingFormatWithFilename(rawText, filename)
	isFremco := isFremcoFormat(rawText)
	isJettingContentOnly := isJettingFormat(rawText)
	
	result["format_detection"] = map[string]interface{}{
		"is_jetting": isJetting,
		"is_jetting_content_only": isJettingContentOnly,
		"is_fremco":  isFremco,
		"jetting_indicators": analyzeJettingIndicators(rawText),
		"fremco_indicators":  analyzeFremcoIndicators(rawText),
		"filename_hints": analyzeFilenameHints(filename),
	}

	// Try parsing if format detected
	if isJetting {
		normalized := simulator.NormalizeJettingTxt(rawText)
		result["normalized_sample"] = truncateString(normalized, 500)
		protocol := simulator.ParseJettingProtocol(normalized)
		if protocol != nil {
			result["parsing_success"] = true
			result["measurements"] = len(protocol.Measurements.DataPoints)
		} else {
			result["parsing_success"] = false
		}
	} else if isFremco {
		normalized := simulator.NormalizeFremcoTxt(rawText)
		result["normalized_sample"] = truncateString(normalized, 500)
		protocol := simulator.ParseFremcoProtocol(normalized)
		if protocol != nil {
			result["parsing_success"] = true
			result["measurements"] = len(protocol.Measurements.DataPoints)
		} else {
			result["parsing_success"] = false
		}
	}

	return result
}

// analyzeJettingIndicators provides detailed analysis of Jetting format indicators
func analyzeJettingIndicators(text string) map[string]bool {
	return map[string]bool{
		"has_streckenlänge": strings.Contains(text, "Streckenlänge"),
		"has_geschwindigkeit": strings.Contains(text, "Geschwindigkeit"),
		"has_drehmoment": strings.Contains(text, "Drehmoment"),
		"has_schubkraft": strings.Contains(text, "Schubkraft"),
		"has_uhrzeit": strings.Contains(text, "Uhrzeit"),
		"has_länge": strings.Contains(text, "Länge"),
		"has_m_min": strings.Contains(text, "m/min"),
		"has_m_bracket": strings.Contains(text, "[m]"),
		"has_bar_bracket": strings.Contains(text, "[bar]"),
		"has_fremco": strings.Contains(text, "Fremco"),
	}
}

// analyzeFremcoIndicators provides detailed analysis of Fremco format indicators  
func analyzeFremcoIndicators(text string) map[string]bool {
	return map[string]bool{
		"has_fremco": strings.Contains(text, "Fremco"),
		"has_speednet": strings.Contains(text, "SpeedNet"),
		"has_microflow": strings.Contains(text, "MicroFlow"),
		"has_blowing_distance": strings.Contains(text, "Blowing distance"),
		"has_blowing_time": strings.Contains(text, "Blowing time"),
		"has_streckenabschnitt": strings.Contains(text, "Streckenabschnitt"),
		"has_equipment_serial": strings.Contains(text, "Equipment") && strings.Contains(text, "Serial"),
	}
}

// analyzeFilenameHints provides detailed analysis of filename-based format indicators
func analyzeFilenameHints(filename string) map[string]bool {
	datePattern, _ := regexp.MatchString(`\d{2}\.\d{2}\.\d{4}`, filename)
	timePattern, _ := regexp.MatchString(`\d{2} \d{2}`, filename)
	
	return map[string]bool{
		"has_nvt": strings.Contains(filename, "NVT"),
		"has_comma_separation": strings.Contains(filename, ","),
		"has_date_pattern": datePattern,
		"has_time_pattern": timePattern,
		"probable_jetting_filename": strings.Contains(filename, "NVT") && strings.Contains(filename, ",") && datePattern,
	}
}

// BulkUploadHandler serves the bulk upload page and handles bulk processing
func BulkUploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		// Serve the bulk upload page
		tmpl, err := template.ParseFiles("web/templates/bulk-upload.html")
		if err != nil {
			http.Error(w, "Error loading template: "+err.Error(), http.StatusInternalServerError)
			return
		}
		
		err = tmpl.Execute(w, nil)
		if err != nil {
			http.Error(w, "Error rendering template: "+err.Error(), http.StatusInternalServerError)
			return
		}
		return
	}
	
	if r.Method == "POST" {
		// Handle bulk file upload processing
		err := r.ParseMultipartForm(100 << 20) // 100 MB max memory
		if err != nil {
			http.Error(w, "Error parsing multipart form: "+err.Error(), http.StatusBadRequest)
			return
		}
		
		files := r.MultipartForm.File["pdfFiles"]
		if len(files) == 0 {
			http.Error(w, "No files provided", http.StatusBadRequest)
			return
		}
		
		// Get options
		autoSave := r.FormValue("autoSave") == "true"
		skipExisting := r.FormValue("skipExisting") == "true"
		
		results := make([]map[string]interface{}, 0)
		var mutex sync.Mutex
		var wg sync.WaitGroup
		
		// Process files in parallel (limit concurrency to avoid overwhelming system)
		semaphore := make(chan struct{}, 5) // Max 5 concurrent processing
		
		for _, fileHeader := range files {
			wg.Add(1)
			go func(fh *multipart.FileHeader) {
				defer wg.Done()
				semaphore <- struct{}{} // Acquire semaphore
				defer func() { <-semaphore }() // Release semaphore
				
				file, err := fh.Open()
				if err != nil {
					mutex.Lock()
					results = append(results, map[string]interface{}{
						"filename": fh.Filename,
						"success":  false,
						"error":    "Failed to open file: " + err.Error(),
					})
					mutex.Unlock()
					return
				}
				defer file.Close()
				
				// Check if file exists in database if skipExisting is enabled
				if skipExisting && db != nil {
					var count int
					err = db.QueryRow("SELECT COUNT(*) FROM protocols WHERE source_filename = $1", fh.Filename).Scan(&count)
					if err == nil && count > 0 {
						mutex.Lock()
						results = append(results, map[string]interface{}{
							"filename": fh.Filename,
							"success":  true,
							"skipped":  true,
							"message":  "File already exists in database",
						})
						mutex.Unlock()
						return
					}
				}
				
				// Read file content
				content, err := io.ReadAll(file)
				if err != nil {
					mutex.Lock()
					results = append(results, map[string]interface{}{
						"filename": fh.Filename,
						"success":  false,
						"error":    "Failed to read file: " + err.Error(),
					})
					mutex.Unlock()
					return
				}
				
				// Process the PDF
				result := processBulkPDF(fh.Filename, content, autoSave)
				
				mutex.Lock()
				results = append(results, result)
				mutex.Unlock()
				
			}(fileHeader)
		}
		
		wg.Wait()
		
		// Return results as JSON
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"results": results,
		})
		return
	}
	
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// processBulkPDF processes a single PDF file for bulk upload
func processBulkPDF(filename string, content []byte, autoSave bool) map[string]interface{} {
	result := map[string]interface{}{
		"filename": filename,
		"success":  false,
	}
	
	// Validate input
	if len(content) == 0 {
		result["error"] = "Empty file content"
		log.Printf("Bulk upload error - %s: Empty file content", filename)
		return result
	}
	
	if len(content) < 100 {
		result["error"] = "File too small (likely not a valid PDF)"
		log.Printf("Bulk upload error - %s: File size %d bytes - too small", filename, len(content))
		return result
	}
	
	// Check PDF header
	if !strings.HasPrefix(string(content[:4]), "%PDF") {
		result["error"] = "Not a valid PDF file (missing PDF header)"
		log.Printf("Bulk upload error - %s: Invalid PDF header", filename)
		return result
	}
	
	// Save to temporary file
	tempFile, err := os.CreateTemp("", "bulk_*.pdf")
	if err != nil {
		result["error"] = "Failed to create temporary file: " + err.Error()
		log.Printf("Bulk upload error - %s: Temp file creation failed - %v", filename, err)
		return result
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()
	
	_, err = tempFile.Write(content)
	if err != nil {
		result["error"] = "Failed to write temporary file: " + err.Error()
		log.Printf("Bulk upload error - %s: Temp file write failed - %v", filename, err)
		return result
	}
	tempFile.Close()
	
	// Extract text using pdftotext first
	cmd := exec.Command("pdftotext", "-layout", tempFile.Name(), "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	extractMethod := "pdftotext"
	if err != nil {
		log.Printf("Bulk upload - %s: pdftotext failed, trying Go library - %v", filename, err)
		// Try Go PDF library as fallback
		text, err := extractTextWithGoLib(tempFile.Name())
		if err != nil {
			result["error"] = "Both PDF extraction methods failed: pdftotext: " + stderr.String() + ", Go library: " + err.Error()
			log.Printf("Bulk upload error - %s: All extraction methods failed - pdftotext: %v, Go library: %v", filename, stderr.String(), err)
			return result
		}
		stdout.WriteString(text)
		extractMethod = "go-library"
	}
	
	rawText := stdout.String()
	if rawText == "" {
		result["error"] = "No text extracted from PDF"
		log.Printf("Bulk upload error - %s: No text extracted using %s", filename, extractMethod)
		return result
	}

	if len(strings.TrimSpace(rawText)) < 50 {
		result["error"] = "Extracted text too short (likely extraction failed)"
		log.Printf("Bulk upload error - %s: Extracted text too short (%d chars)", filename, len(rawText))
		return result
	}

	log.Printf("Bulk upload - %s: Successfully extracted %d characters using %s", filename, len(rawText), extractMethod)
	
	// Detect format and parse (with filename hints)
	isJetting := isJettingFormatWithFilename(rawText, filename)
	isFremco := isFremcoFormat(rawText)
	
	log.Printf("Bulk upload - %s: Format detection - Jetting: %v, Fremco: %v", filename, isJetting, isFremco)
	
	if isJetting {
		normalized := simulator.NormalizeJettingTxt(rawText)
		protocol := simulator.ParseJettingProtocol(normalized)
		if protocol == nil {
			result["error"] = "Jetting parsing failed - unable to parse normalized text"
			log.Printf("Bulk upload error - %s: Jetting parsing failed. First 200 chars of normalized text: %q", filename, truncateString(normalized, 200))
			return result
		}
		
		// Parse filename for metadata (Jetting format: "29.10.2025, 11 09, Eiemarkt 14 NVT 1V2200.pdf")
		baseName := strings.TrimSuffix(filename, filepath.Ext(filename))
		var date, time, address, nvt string
		if strings.Contains(baseName, ",") {
			parts := strings.Split(baseName, ",")
			if len(parts) >= 3 {
				date = strings.TrimSpace(parts[0])
				timeRaw := strings.TrimSpace(parts[1])
				// Convert space-separated time to colon-separated (e.g., "11 09" -> "11:09")
				if strings.Contains(timeRaw, " ") {
					timeParts := strings.Fields(timeRaw)
					if len(timeParts) == 2 {
						time = timeParts[0] + ":" + timeParts[1]
					}
				} else {
					time = timeRaw
				}
				rest := strings.TrimSpace(parts[2])
				nvtIdx := strings.Index(rest, "NVT ")
				if nvtIdx != -1 {
					address = strings.TrimSpace(rest[:nvtIdx])
					nvt = strings.TrimSpace(rest[nvtIdx+4:])
				} else {
					address = rest
				}
			}
			log.Printf("Bulk upload - %s [Jetting] Parsed filename: Date: %s | Time: %s | Address: %s | NVT: %s", filename, date, time, address, nvt)
		}
		
		// Fill in filename-based metadata
		protocol.ExportMetadata.SourceFilename = filename
		if date != "" {
			protocol.ProtocolInfo.Date = date
		}
		if time != "" {
			protocol.ProtocolInfo.StartTime = time
		}
		if address != "" && nvt != "" {
			protocol.ProtocolInfo.SectionNVT = address + " / " + nvt
		} else if address != "" {
			protocol.ProtocolInfo.SectionNVT = address
		}
		
		result["format"] = "jetting"
		measurementCount := len(protocol.Measurements.DataPoints)
		result["measurements"] = measurementCount
		
		log.Printf("Bulk upload - %s: Jetting protocol parsed successfully - %d measurements", filename, measurementCount)
		
		if autoSave && db != nil {
			_, err := SaveJettingProtocol(db, protocol)
			if err != nil {
				result["error"] = "Database save failed: " + err.Error()
				log.Printf("Bulk upload error - %s: Jetting database save failed - %v", filename, err)
				return result
			}
			result["saved"] = true
			log.Printf("Bulk upload - %s: Jetting protocol saved to database successfully", filename)
		}
	} else if isFremco {
		normalized := simulator.NormalizeFremcoTxt(rawText)
		protocol := simulator.ParseFremcoProtocol(normalized)
		if protocol == nil {
			result["error"] = "Fremco parsing failed - unable to parse normalized text"
			log.Printf("Bulk upload error - %s: Fremco parsing failed. First 200 chars of normalized text: %q", filename, truncateString(normalized, 200))
			return result
		}
		
		// Parse filename for metadata (Fremco format: "SM209214964_2025-10-22 10_51_Oldenburger Koppel_10_NVT1V3400.pdf")
		baseName := strings.TrimSuffix(filename, filepath.Ext(filename))
		var date, time, address, nvt, project string
		if strings.Contains(baseName, "_") {
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
			log.Printf("Bulk upload - %s [Fremco] Parsed filename: Project: %s | Date: %s | Time: %s | Address: %s | NVT: %s", filename, project, date, time, address, nvt)
		}
		
		// Fill in filename-based metadata
		protocol.ExportMetadata.SourceFilename = filename
		if date != "" {
			protocol.ProtocolInfo.Date = date
		}
		if time != "" {
			protocol.ProtocolInfo.StartTime = time
		}
		if project != "" {
			protocol.ProtocolInfo.ProjectNumber = project
		}
		if address != "" && nvt != "" {
			protocol.ProtocolInfo.SectionNVT = address + " / " + nvt
		} else if address != "" {
			protocol.ProtocolInfo.SectionNVT = address
		}
		
		result["format"] = "fremco"
		measurementCount := len(protocol.Measurements.DataPoints)
		result["measurements"] = measurementCount
		
		log.Printf("Bulk upload - %s: Fremco protocol parsed successfully - %d measurements", filename, measurementCount)
		
		if autoSave && db != nil {
			_, err := SaveFremcoProtocol(db, protocol)
			if err != nil {
				result["error"] = "Database save failed: " + err.Error()
				log.Printf("Bulk upload error - %s: Fremco database save failed - %v", filename, err)
				return result
			}
			result["saved"] = true
			log.Printf("Bulk upload - %s: Fremco protocol saved to database successfully", filename)
		}
	} else {
		// Last resort: if filename looks like Jetting but content detection failed, try Jetting anyway
		filenameHints := analyzeFilenameHints(filename)
		if filenameHints["probable_jetting_filename"] {
			log.Printf("Bulk upload - %s: Content detection failed but filename suggests Jetting, attempting Jetting parse", filename)
			
			normalized := simulator.NormalizeJettingTxt(rawText)
			protocol := simulator.ParseJettingProtocol(normalized)
			if protocol != nil {
				// Filename-based detection succeeded
				protocol.ExportMetadata.SourceFilename = filename
				
				result["format"] = "jetting"
				measurementCount := len(protocol.Measurements.DataPoints)
				result["measurements"] = measurementCount
				result["filename_fallback"] = true
				
				log.Printf("Bulk upload - %s: Filename-based Jetting fallback successful - %d measurements", filename, measurementCount)
				
				if autoSave && db != nil {
					_, err := SaveJettingProtocol(db, protocol)
					if err != nil {
						result["error"] = "Database save failed: " + err.Error()
						log.Printf("Bulk upload error - %s: Jetting database save failed - %v", filename, err)
						return result
					}
					result["saved"] = true
					log.Printf("Bulk upload - %s: Filename-fallback Jetting protocol saved to database successfully", filename)
				}
			} else {
				result["error"] = "Unknown PDF format - content does not match Fremco or Jetting patterns, filename fallback also failed"
				log.Printf("Bulk upload error - %s: Format detection failed completely. First 500 chars: %q", filename, truncateString(rawText, 500))
				return result
			}
		} else {
			result["error"] = "Unknown PDF format - content does not match Fremco or Jetting patterns"
			log.Printf("Bulk upload error - %s: Format detection failed. First 500 chars: %q", filename, truncateString(rawText, 500))
			return result
		}
	}
	
	result["success"] = true
	return result
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
