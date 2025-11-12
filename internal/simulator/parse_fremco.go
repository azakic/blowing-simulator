package simulator

import (
	"strconv"
	"strings"
)

// SimpleMeasurement represents a single measurement in the uniform format.
type SimpleMeasurement struct {
	Length   float64 `json:"length"`
	Speed    float64 `json:"speed"`
	Pressure float64 `json:"pressure"`
	Torque   int     `json:"torque"`
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
		// Detect start of table
		if strings.HasPrefix(line, "Streckenlänge") {
			inTable = true
			continue
		}
		// Skip headers inside the table
		if strings.HasPrefix(line, "Geschwindigkeit") ||
			strings.HasPrefix(line, "Rohr-Druck") ||
			strings.HasPrefix(line, "Drehmoment") ||
			strings.HasPrefix(line, "Uhrzeit") {
			continue
		}
		if inTable {
			fields := strings.Fields(line)
			if len(fields) >= 5 {
				length, _ := strconv.ParseFloat(fields[0], 64)
				speed, _ := strconv.ParseFloat(fields[1], 64)
				pressure, _ := strconv.ParseFloat(fields[2], 64)
				torque, _ := strconv.ParseFloat(fields[3], 64)
				measurements = append(measurements, SimpleMeasurement{
					Length:   length,
					Speed:    speed,
					Pressure: pressure,
					Torque:   int(torque + 0.5),
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
