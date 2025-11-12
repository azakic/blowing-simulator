package simulator

import (
	"strconv"
	"strings"
)

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

	// Remove empty lines and trim
	var values []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			values = append(values, line)
		}
	}
	println("Jetting Parser Debug: values count =", len(values))

	// Robust row parser: scan for plausible length values, treat next 5 values as row
	var rows [][]string
	for i := 0; i+5 < len(values); i++ {
		length, err := strconv.ParseFloat(values[i], 64)
		// Accept plausible length values (e.g., 0 < length < 1000)
		if err == nil && length >= 0 && length < 1000 {
			row := values[i : i+6]
			rows = append(rows, row)
			i += 5 // advance to next possible row
		}
	}
	println("Jetting Parser Debug: Robust row parser, rows =", len(rows))

	// Find max force for torque calculation
	maxForce := 1.0
	for _, row := range rows {
		force, err := strconv.ParseFloat(row[2], 64)
		if err == nil && force > maxForce {
			maxForce = force
		}
	}

	for _, row := range rows {
		length, _ := strconv.ParseFloat(row[0], 64)
		force, _ := strconv.ParseFloat(row[2], 64)
		pressure, _ := strconv.ParseFloat(row[3], 64)
		speed, _ := strconv.ParseFloat(row[4], 64)
		// time := row[5] // ignored

		// Calculate torque as percentage of max force, clamp to 1-100
		torque := 1.0
		if maxForce > 0 {
			torque = (force / maxForce) * 100
			if torque < 1 {
				torque = 1
			}
			if torque > 100 {
				torque = 100
			}
		}

		measurements = append(measurements, SimpleMeasurement{
			Length:   length,
			Speed:    speed,
			Pressure: pressure,
			Torque:   int(torque + 0.5),
		})
	}
	return measurements
}
