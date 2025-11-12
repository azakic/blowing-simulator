package simulator

import (
	"strconv"
	"strings"
)

// ExtractJettingMetadata scans normalized Jetting TXT for metadata fields.
func ExtractJettingMetadata(normalized string) map[string]string {
	meta := make(map[string]string)
	lines := strings.Split(normalized, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Datum:") {
			meta["Date"] = strings.TrimSpace(strings.TrimPrefix(line, "Datum:"))
		}
		if strings.HasPrefix(line, "Uhrzeit:") {
			meta["Time"] = strings.TrimSpace(strings.TrimPrefix(line, "Uhrzeit:"))
		}
		if strings.HasPrefix(line, "Adresse:") {
			meta["Address"] = strings.TrimSpace(strings.TrimPrefix(line, "Adresse:"))
		}
		if strings.HasPrefix(line, "NVT:") {
			meta["NVT"] = strings.TrimSpace(strings.TrimPrefix(line, "NVT:"))
		}
	}
	return meta
}

// SimpleMeasurement is defined in parse_fremco.go

// Block header detection for Jetting format
func isBlockHeader(line string) bool {
	headers := []string{
		"Länge[m]", "Lufttemperatur[°C]", "Schubkraft[N]", "Einblasdruck[bar]", "Geschwindigkeit[m/min]", "Zeit - Dauer[hh:mm:ss]",
	}
	for _, h := range headers {
		if strings.HasPrefix(line, h) {
			return true
		}
	}
	return false
}

// Unit line detection for Jetting format
func isUnitLine(line string) bool {
	units := []string{
		"[m]", "[°C]", "[N]", "[bar]", "[m/min]", "[hh:mm:ss]",
	}
	for _, u := range units {
		if strings.HasPrefix(strings.TrimSpace(line), u) {
			return true
		}
	}
	return false
}

// Minimum of multiple ints
func min(vals ...int) int {
	if len(vals) == 0 {
		return 0
	}
	m := vals[0]
	for _, v := range vals {
		if v < m {
			m = v
		}
	}
	return m
}

// ParseJettingTxt parses normalized Jetting TXT data in vertical block format and returns a slice of SimpleMeasurement.
func ParseJettingTxt(normalized string) []SimpleMeasurement {
	lines := strings.Split(normalized, "\n")
	var measurements []SimpleMeasurement
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		// Accept rows with 6 columns (Jetting format)
		if len(fields) >= 6 {
			length, _ := strconv.ParseFloat(fields[0], 64)
			// temp := fields[1] // not used
			force, _ := strconv.ParseFloat(fields[2], 64)
			pressure, _ := strconv.ParseFloat(fields[3], 64)
			speed, _ := strconv.ParseFloat(fields[4], 64)
			time := fields[5]
			measurements = append(measurements, SimpleMeasurement{
				Length:   length,
				Speed:    speed,
				Pressure: pressure,
				Torque:   force, // use force as torque for now
				Time:     time,
			})
		} else if len(fields) == 5 {
			// Accept alternate format if present
			length, _ := strconv.ParseFloat(fields[0], 64)
			speed, _ := strconv.ParseFloat(fields[1], 64)
			pressure, _ := strconv.ParseFloat(fields[2], 64)
			torque, _ := strconv.ParseFloat(fields[3], 64)
			time := fields[4]
			measurements = append(measurements, SimpleMeasurement{
				Length:   length,
				Speed:    speed,
				Pressure: pressure,
				Torque:   torque,
				Time:     time,
			})
		}
		// Ignore rows with fewer than 5 columns
	}
	return measurements
}
