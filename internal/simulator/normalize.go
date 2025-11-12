package simulator

import (
	"strings"
)

// NormalizeJettingTxt normalizes Jetting TXT data extracted from PDF.
// It removes form feeds, trims whitespace, and groups blocks by headers.
func NormalizeJettingTxt(raw string) string {
	// Replace form feeds with newlines
	raw = strings.ReplaceAll(raw, "\f", "\n")
	lines := strings.Split(raw, "\n")
	var cleaned []string
	for _, line := range lines {
		line = strings.TrimRight(line, " \t\r")
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return strings.Join(cleaned, "\n")
}

// NormalizeFremcoTxt normalizes Fremco TXT data extracted from PDF.
// It removes form feeds and trims whitespace, preserving table structure.
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

// NormalizeJettingFlatTxt converts a flat value list into block format for Jetting parser.
// Assumes a fixed order and count for each block. Adapt as needed for your data.
func NormalizeJettingFlatTxt(raw string) string {
	lines := strings.Split(raw, "\n")
	// Example: define block order and counts
	// Adjust these counts to match your actual data!
	blocks := []struct {
		name  string
		unit  string
		count int
	}{
		{"Länge", "[m]", 13},      // first 13 lines
		{"Schubkraft", "[N]", 13}, // next 13 lines
		{"Geschwindigkeit Zeit - Dauer", "[m/min]\n[hh:mm:ss]", 20}, // next 40 lines (20 pairs)
	}

	var out []string
	idx := 0
	for _, b := range blocks {
		out = append(out, b.name)
		out = append(out, b.unit)
		for i := 0; i < b.count && idx < len(lines); i++ {
			val := strings.TrimSpace(lines[idx])
			if val != "" {
				out = append(out, val)
			}
			idx++
			// For paired block, add next line as well
			if b.name == "Geschwindigkeit Zeit - Dauer" && idx < len(lines) {
				val2 := strings.TrimSpace(lines[idx])
				if val2 != "" {
					out = append(out, val2)
				}
				idx++
			}
		}
		out = append(out, "") // blank line between blocks
	}
	return strings.Join(out, "\n")
}
