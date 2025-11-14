package simulator

import (
	"strconv"
	"strings"
)

// SimpleMeasurement represents a single measurement in the uniform format (for Fremco).
type SimpleMeasurement struct {
	Length   float64 `json:"length"`
	Speed    float64 `json:"speed"`
	Pressure float64 `json:"pressure"`
	Torque   float64 `json:"torque"`
	Time     string  `json:"time"`
}

// JettingMeasurement represents a Jetting measurement with German field names.
type JettingMeasurement struct {
	Length      float64 `json:"länge_m"`
	Temperature float64 `json:"lufttemperatur_c"`
	Force       float64 `json:"schubkraft_n"`
	Pressure    float64 `json:"einblasdruck_bar"`
	Speed       float64 `json:"geschwindigkeit_m_min"`
	Time        string  `json:"zeit_dauer_hh_mm_ss"`
}

// ExtractFremcoMetadata parses normalized Fremco text and returns a map of metadata fields.

// ExtractFremcoMetadata parses normalized Fremco text and returns a map of metadata fields.
func ExtractFremcoMetadata(normalized string) map[string]string {
	meta := make(map[string]string)
	lines := strings.Split(normalized, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Bauvorhaben Nr.") {
			// Project number
			if i+1 < len(lines) {
				meta["Project"] = strings.TrimSpace(lines[i+1])
			}
		}
		if strings.HasPrefix(line, "Datum, Startzeit:") {
			// Date and time
			if i+1 < len(lines) {
				dt := strings.TrimSpace(lines[i+1])
				parts := strings.Split(dt, " ")
				if len(parts) >= 2 {
					meta["Date"] = parts[0]
					meta["Time"] = parts[1]
				} else {
					meta["Date"] = dt
				}
			}
		}
		if strings.HasPrefix(line, "Streckenabschnitt / NVt") {
			// Address and NVT
			if i+1 < len(lines) {
				addrNvt := strings.TrimSpace(lines[i+1])
				addrParts := strings.Split(addrNvt, "/")
				if len(addrParts) >= 2 {
					meta["Address"] = strings.TrimSpace(addrParts[0])
					meta["NVT"] = strings.TrimSpace(addrParts[1])
				} else {
					meta["Address"] = addrNvt
				}
			}
		}
		if strings.HasPrefix(line, "Firma") {
			if i+1 < len(lines) {
				meta["Company"] = strings.TrimSpace(lines[i+1])
			}
		}
		if strings.HasPrefix(line, "Einblasgerät:") {
			meta["Device"] = strings.TrimPrefix(line, "Einblasgerät:")
			meta["Device"] = strings.TrimSpace(meta["Device"])
		}
		if strings.HasPrefix(line, "Bezeichnung:") {
			meta["Cable"] = strings.TrimPrefix(line, "Bezeichnung:")
			meta["Cable"] = strings.TrimSpace(meta["Cable"])
		}
		if strings.HasPrefix(line, "Meterzahlen:") {
			// Meter range
			if i+1 < len(lines) {
				meta["MeterRange"] = strings.TrimSpace(lines[i+1])
			}
		}
		if strings.HasPrefix(line, "Zusammenfassung") {
			// Strecke, Einblaszeit, Wetter, Ort (GPS)
			if i+1 < len(lines) {
				sum := lines[i+1]
				// Example: Strecke: 370 Einblaszeit: 00:38:25 Wetter: 19.3°C, 69.5%RH Ort (GPS): 52.48653, 9.85465
				if strings.Contains(sum, "Strecke:") {
					parts := strings.Split(sum, "Strecke:")
					if len(parts) > 1 {
						rest := parts[1]
						fields := strings.Fields(rest)
						if len(fields) > 0 {
							meta["Strecke"] = fields[0]
						}
					}
				}
				if strings.Contains(sum, "Einblaszeit:") {
					parts := strings.Split(sum, "Einblaszeit:")
					if len(parts) > 1 {
						meta["Einblaszeit"] = strings.Fields(parts[1])[0]
					}
				}
				if strings.Contains(sum, "Wetter:") {
					parts := strings.Split(sum, "Wetter:")
					if len(parts) > 1 {
						meta["Wetter"] = strings.Split(parts[1], "Ort")[0]
						meta["Wetter"] = strings.TrimSpace(meta["Wetter"])
					}
				}
				if strings.Contains(sum, "Ort (GPS):") {
					parts := strings.Split(sum, "Ort (GPS):")
					if len(parts) > 1 {
						meta["GPS"] = strings.TrimSpace(parts[1])
					}
				}
			}
		}
	}
	return meta
}

// ParseFremcoSimple parses normalized Fremco TXT and returns a slice of SimpleMeasurement.
// It expects the table header to start with "Streckenlänge" and each row to have:
// Length [m], Speed [m/min], Pressure [bar], Torque [%], DateTime [hh:mm:ss]
func ParseFremcoSimple(normalized string) []SimpleMeasurement {
	lines := strings.Split(normalized, "\n")
	var measurements []SimpleMeasurement
	inTable := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Detect start of table (accept both German and English headers)
		if strings.HasPrefix(line, "Länge") || strings.HasPrefix(line, "Streckenlänge") {
			inTable = true
			continue
		}
		// Skip headers inside the table
		if strings.HasPrefix(line, "Lufttemperatur") ||
			strings.HasPrefix(line, "Schubkraft") ||
			strings.HasPrefix(line, "Einblasdruck") ||
			strings.HasPrefix(line, "Geschwindigkeit") ||
			strings.HasPrefix(line, "Zeit - Dauer") ||
			strings.HasPrefix(line, "[m]") || strings.HasPrefix(line, "[°C]") {
			continue
		}
		if inTable {
			fields := strings.Fields(line)
			// Accept rows with at least 5 columns: length, speed, pressure, torque, time
			if len(fields) >= 5 {
				length, _ := strconv.ParseFloat(fields[0], 64)
				speed, _ := strconv.ParseFloat(fields[1], 64)
				pressure, _ := strconv.ParseFloat(fields[2], 64)
				torque, _ := strconv.ParseFloat(fields[3], 64)
				time := fields[4]
				// If time field is split (date + time), join them
				if len(fields) > 5 && isDateTime(fields[4]+" "+fields[5]) {
					time = fields[4] + " " + fields[5]
				}
				measurements = append(measurements, SimpleMeasurement{
					Length:   length,
					Speed:    speed,
					Pressure: pressure,
					Torque:   torque,
					Time:     time,
				})
			}
		}
	}
	return measurements
}

// Helper functions
func isNumeric(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

func isDateTime(s string) bool {
	// Accepts formats like "2024-07-26 14:41:07"
	return len(s) >= 16 && s[4] == '-' && s[7] == '-' && (s[10] == ' ' || s[10] == 'T')
}
