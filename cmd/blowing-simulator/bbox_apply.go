package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"blowing-simulator/internal/simulator"
)

// applyBBoxMeasurements pokušava da izmeri tabelu iz koordinata
// (`pdftotext -bbox-layout`).
//
// Zašto: `-layout` tekst spaja kolone kada su blizu jedna drugoj, pa red može
// da se izgubi ili da se vrednosti pomere. Koordinatni parser grupiše reči po
// `y` (red) i `x` (kolona) i otporan je na to.
//
// Ponašanje zavisi od `MEASURE_SOURCE`:
//   - `auto` (podrazumevano): koristi bbox samo ako daje **više** redova;
//   - `bbox`: koristi bbox kad god vrati bar jedan red;
//   - `text`: nikad (samo tekstualni parser).
//
// Vraća napomenu za telemetriju ili prazan string.
func applyBBoxMeasurements[T any](pdfPath, format string, target *[]T, convert func([]simulator.RenderRow) []T) string {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("MEASURE_SOURCE")))
	if mode == "" {
		mode = "auto"
	}
	if mode == "text" {
		return ""
	}

	rows, err := simulator.MeasurementsFromPDFBBox(pdfPath, format)
	if err != nil {
		log.Printf("bbox %s: %v", format, err)
		return ""
	}
	if len(rows) == 0 {
		return ""
	}

	oldCount := len(*target)
	if mode == "auto" {
		log.Printf("bbox %s: tekst=%d bbox=%d (auto)", format, oldCount, len(rows))
	}
	if mode == "auto" && oldCount > 0 && len(rows) <= oldCount {
		return ""
	}

	*target = convert(rows)
	note := fmt.Sprintf("bbox: %d → %d merenja (koordinatni parser)", oldCount, len(rows))
	log.Printf("bbox %s: %s", format, note)
	return note
}
