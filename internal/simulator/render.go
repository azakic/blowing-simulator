package simulator

import (
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jung-kurt/gofpdf"
)

// RenderRow je jedno merenje u dokumentu koji generišemo.
type RenderRow struct {
	Length      float64
	Temperature float64
	Force       float64
	Pressure    float64
	Speed       float64
	Torque      float64
	Time        string
}

// RenderModel su podaci potrebni da se napravi dokument koji se ponovo parsira
// u iste vrednosti (round-trip).
type RenderModel struct {
	Format          string // "fremco" | "jetting"
	Date            string
	StartTime       string
	ProjectNumber   string
	SectionNVT      string
	Company         string
	ServiceProvider string
	Operator        string
	Remarks         string
	Rows            []RenderRow
}

// RenderModelFromFremco prevodi parsirani Fremco protokol u model za render.
func RenderModelFromFremco(p *FremcoProtocol) RenderModel {
	m := RenderModel{
		Format:          "fremco",
		Date:            p.ProtocolInfo.Date,
		StartTime:       p.ProtocolInfo.StartTime,
		ProjectNumber:   p.ProtocolInfo.ProjectNumber,
		SectionNVT:      p.ProtocolInfo.SectionNVT,
		Company:         p.ProtocolInfo.Company,
		ServiceProvider: p.ProtocolInfo.ServiceProvider,
		Operator:        p.ProtocolInfo.Operator,
		Remarks:         p.ProtocolInfo.Remarks,
	}
	for _, d := range p.Measurements.DataPoints {
		m.Rows = append(m.Rows, RenderRow{
			Length:   d.LengthM,
			Speed:    d.SpeedMMin,
			Pressure: d.PressureBar,
			Torque:   d.TorquePercent,
			Time:     d.Timestamp,
		})
	}
	return m
}

// RenderModelFromJetting prevodi parsirani Jetting protokol u model za render.
func RenderModelFromJetting(p *JettingProtocol) RenderModel {
	m := RenderModel{
		Format:          "jetting",
		Date:            p.ProtocolInfo.Date,
		StartTime:       p.ProtocolInfo.StartTime,
		SectionNVT:      p.ProtocolInfo.SectionNVT,
		Company:         p.ProtocolInfo.Company,
		ServiceProvider: p.ProtocolInfo.ServiceProvider,
		Remarks:         p.ProtocolInfo.Remarks,
	}
	if p.ProtocolInfo.ProjectNumber != nil {
		m.ProjectNumber = *p.ProtocolInfo.ProjectNumber
	}
	if p.ProtocolInfo.Operator != nil {
		m.Operator = *p.ProtocolInfo.Operator
	}
	for _, d := range p.Measurements.DataPoints {
		m.Rows = append(m.Rows, RenderRow{
			Length:      d.LengthM,
			Temperature: d.TemperatureC,
			Force:       d.ForceN,
			Pressure:    d.PressureBar,
			Speed:       d.SpeedMMin,
			Time:        d.TimeDuration,
		})
	}
	return m
}

// RenderProtocolPDF upisuje dokument u formatu koji odgovara `m.Format`.
//
// Cilj nije vizuelna kopija originala, nego **re-parse**: dokument sadrži ista
// polja na istim mestima (Firma blok, tabela sa istim kolonama), pa
// `protocolctl -verify` može da ga pročita istim parserom.
func RenderProtocolPDF(w io.Writer, m RenderModel, fontPath string) error {
	pdf := gofpdf.New("P", "mm", "A4", "")
	font := "Helvetica"
	if fontPath != "" {
		// gofpdf spaja prosleđenu putanju sa svojim font direktorijumom i pri
		// tome odseče vodeći "/", pa font čitamo sami i dajemo bajtove.
		if data, err := os.ReadFile(fontPath); err != nil {
			log.Printf("render: font %s nije pročitan: %v (koristim Helvetica)", fontPath, err)
		} else {
			pdf.AddUTF8FontFromBytes("DejaVu", "", data)
			if err := pdf.Error(); err == nil {
				font = "DejaVu"
			} else {
				log.Printf("render: font %s nije učitan: %v", fontPath, err)
				pdf.ClearError()
			}
		}
	}
	// Datum iz baze je ISO, a Jetting parser očekuje DD.MM.YYYY (i obrnuto).
	if t, err := time.Parse("2006-01-02", strings.TrimSpace(m.Date)); err == nil && m.Format == "jetting" {
		m.Date = t.Format("02.01.2006")
	}
	if t, err := time.Parse("02.01.2006", strings.TrimSpace(m.Date)); err == nil && m.Format == "fremco" {
		m.Date = t.Format("2006-01-02")
	}

	pdf.SetMargins(12, 12, 12)
	pdf.SetAutoPageBreak(true, 15)
	pdf.AddPage()
	pdf.SetFont(font, "", 10)

	const labelX, valueX = 12.0, 70.0

	field := func(label, value string) {
		pdf.SetX(labelX)
		pdf.CellFormat(56, 7, label, "", 0, "L", false, 0, "")
		pdf.SetX(valueX)
		pdf.CellFormat(126, 7, value, "", 1, "L", false, 0, "")
	}

	switch m.Format {
	case "fremco":
		field("SpeedNet-System", "Einblas - Protokoll")
		field("Bauvorhaben Nr.", m.ProjectNumber)
		// Fremco parser čita datum iz reda ispod „Bauvorhaben Nr.".
		field("", strings.TrimSpace(m.Date+" "+m.StartTime))
		field("Streckenabschnitt / NVt", m.SectionNVT)
		field("Firma", m.Company)
		pdf.SetX(valueX)
		pdf.CellFormat(80, 7, m.ServiceProvider, "", 0, "L", false, 0, "")
		pdf.CellFormat(50, 7, "Einbläser: "+m.Operator, "", 1, "L", false, 0, "")
		field("Bemerkungen", m.Remarks)

	case "jetting":
		field("JetLogger", "Einblas-Protokoll")
		field("Bauvorhaben", m.ProjectNumber)
		field("Streckenabschnitt/ NVt", m.SectionNVT)
		field("Firma", m.Company)
		field("Einbläser", m.Operator)
		field("Bemerkungen", m.Remarks)
		field("Datum", strings.TrimSpace(m.Date+", "+m.StartTime))

	default:
		return fmt.Errorf("nepoznat format za render: %q", m.Format)
	}

	pdf.Ln(4)
	renderTable(pdf, font, m)
	return pdf.Output(w)
}

func renderTable(pdf *gofpdf.Fpdf, font string, m RenderModel) {
	headers := []string{}
	units := []string{}
	widths := []float64{}

	if m.Format == "fremco" {
		headers = []string{"Streckenlänge [m]", "Geschwindigkeit [m/min]", "Rohr-Druck [bar]", "Drehmoment [%]", "Uhrzeit [hh:mm:ss]"}
		units = []string{"", "", "", "", ""}
		widths = []float64{32, 42, 32, 30, 50}
	} else {
		headers = []string{"Länge", "Lufttemperatur", "Schubkraft", "Einblasdruck", "Geschwindigkeit", "Zeit - Dauer"}
		units = []string{"[m]", "[°C]", "[N]", "[bar]", "[m/min]", "[hh:mm:ss]"}
		widths = []float64{24, 32, 28, 30, 34, 38}
	}

	pdf.SetFont(font, "", 9)
	for i, h := range headers {
		pdf.CellFormat(widths[i], 6, h, "", 0, "L", false, 0, "")
	}
	pdf.Ln(-1)
	for i, u := range units {
		pdf.CellFormat(widths[i], 5, u, "", 0, "L", false, 0, "")
	}
	pdf.Ln(-1)

	pdf.SetFont(font, "", 8)
	for _, row := range m.Rows {
		if pdf.GetY() > 275 {
			pdf.AddPage()
			pdf.SetFont(font, "", 8)
		}
		var cells []string
		if m.Format == "fremco" {
			cells = []string{num(row.Length), num(row.Speed), num(row.Pressure), num(row.Torque), row.Time}
		} else {
			cells = []string{num(row.Length), num(row.Temperature), num(row.Force), num(row.Pressure), num(row.Speed), row.Time}
		}
		for i, c := range cells {
			pdf.CellFormat(widths[i], 5, c, "", 0, "L", false, 0, "")
		}
		pdf.Ln(-1)
	}
}

func num(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// CompareRenderModels poredi originalno parsirane podatke sa podacima
// dobijenim ponovnim parsiranjem generisanog PDF-a. Vraća listu razlika;
// prazna lista znači uspešan round-trip.
func CompareRenderModels(want, got RenderModel) []string {
	var diffs []string
	if want.Format != got.Format {
		return []string{fmt.Sprintf("format: %q != %q", want.Format, got.Format)}
	}
	// Višak razmaka je artefakt PDF layouta, ne razlika u podacima.
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	cmp := func(name, a, b string) {
		if norm(a) != norm(b) {
			diffs = append(diffs, fmt.Sprintf("%s: %q != %q", name, a, b))
		}
	}
	cmp("date", want.Date, got.Date)
	cmp("start_time", want.StartTime, got.StartTime)
	cmp("project_number", want.ProjectNumber, got.ProjectNumber)
	cmp("section_nvt", want.SectionNVT, got.SectionNVT)
	cmp("company", want.Company, got.Company)
	cmp("service_provider", want.ServiceProvider, got.ServiceProvider)
	cmp("operator", want.Operator, got.Operator)
	cmp("remarks", want.Remarks, got.Remarks)

	if len(want.Rows) != len(got.Rows) {
		diffs = append(diffs, fmt.Sprintf("merenja: %d != %d", len(want.Rows), len(got.Rows)))
		return diffs
	}
	near := func(name string, i int, a, b float64) {
		d := a - b
		if d < 0 {
			d = -d
		}
		if d > 0.01 {
			diffs = append(diffs, fmt.Sprintf("red %d %s: %g != %g", i+1, name, a, b))
		}
	}
	for i := range want.Rows {
		w, g := want.Rows[i], got.Rows[i]
		near("length", i, w.Length, g.Length)
		near("temperature", i, w.Temperature, g.Temperature)
		near("force", i, w.Force, g.Force)
		near("pressure", i, w.Pressure, g.Pressure)
		near("speed", i, w.Speed, g.Speed)
		near("torque", i, w.Torque, g.Torque)
		if strings.TrimSpace(w.Time) != strings.TrimSpace(g.Time) {
			diffs = append(diffs, fmt.Sprintf("red %d time: %q != %q", i+1, w.Time, g.Time))
		}
	}
	return diffs
}

// FremcoDataPointsFromRows prevodi redove iz bbox parsera u data points.
func FremcoDataPointsFromRows(rows []RenderRow) []FremcoDataPoint {
	out := make([]FremcoDataPoint, 0, len(rows))
	for _, r := range rows {
		out = append(out, FremcoDataPoint{
			LengthM:       r.Length,
			SpeedMMin:     r.Speed,
			PressureBar:   r.Pressure,
			TorquePercent: r.Torque,
			Timestamp:     r.Time,
		})
	}
	return out
}

// JettingDataPointsFromRows prevodi redove iz bbox parsera u data points.
func JettingDataPointsFromRows(rows []RenderRow) []JettingDataPoint {
	out := make([]JettingDataPoint, 0, len(rows))
	for _, r := range rows {
		out = append(out, JettingDataPoint{
			LengthM:      r.Length,
			TemperatureC: r.Temperature,
			ForceN:       r.Force,
			PressureBar:  r.Pressure,
			SpeedMMin:    r.Speed,
			TimeDuration: r.Time,
		})
	}
	return out
}
