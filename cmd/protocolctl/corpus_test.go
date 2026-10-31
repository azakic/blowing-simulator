package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"blowing-simulator/internal/simulator"
)

// Regresioni test nad stvarnim korpusom. Fajlovi nisu u gitu (sadrže adrese
// kupaca), pa se test preskače ako PROTOCOL_SAMPLES_DIR nije postavljen:
//
//	PROTOCOL_SAMPLES_DIR=~/protocol-samples go test ./cmd/protocolctl -run Corpus
//
// Očekivani broj merenja je proveren i protiv baze (protocol_measurements).
func TestCorpus(t *testing.T) {
	dir := os.Getenv("PROTOCOL_SAMPLES_DIR")
	if dir == "" {
		t.Skip("PROTOCOL_SAMPLES_DIR nije postavljen")
	}

	cases := []struct {
		match        string
		format       simulator.PDFFormat
		measurements int
	}{
		{"Haflinger Weg 10", simulator.FormatFremco, 204},
		{"Oldenburger Koppel 23", simulator.FormatFremco, 293},
		{"Oldenburger Koppel 27", simulator.FormatFremco, 234},
		{"Oldenburger Koppel 19a", simulator.FormatFremco, 371},
		{"Haflinger Weg _9 _", simulator.FormatFremco, 182},
		{"Haflinger Weg 5", simulator.FormatFremco, 151},
		{"Oldenburger Koppel 15", simulator.FormatFremco, 395},
		{"Oldenburger Koppel 13", simulator.FormatFremco, 381},
		{"Mühlenstr 13f", simulator.FormatFremco, 278},
		{"Eiermarkt 14 B", simulator.FormatJetting, 180},
		{"Eiermarkt 15 B", simulator.FormatJetting, 48},
		{"Eiermarkt 13", simulator.FormatJetting, 165},
		{"Eiermarkt 3E", simulator.FormatJetting, 222},
		{"Eiermarkt 3D", simulator.FormatJetting, 189},
		{"Eiermarkt 9", simulator.FormatJetting, 145},
	}

	files, err := filepath.Glob(filepath.Join(dir, "*", "*.pdf"))
	if err != nil || len(files) == 0 {
		t.Skipf("nema PDF-ova u %s", dir)
	}

	for _, c := range cases {
		var match string
		for _, f := range files {
			if strings.Contains(filepath.Base(f), c.match) {
				match = f
				break
			}
		}
		if match == "" {
			t.Logf("preskočeno (nema uzorka): %s", c.match)
			continue
		}

		text, _, err := extractText(match, "auto", 40)
		if err != nil {
			t.Errorf("%s: extractText: %v", c.match, err)
			continue
		}
		if got := simulator.DetectFormat(text); got != c.format {
			t.Errorf("%s: format = %q, očekivano %q", c.match, got, c.format)
			continue
		}
		fields, measurements, _, err := parseProtocol(text, c.format)
		if err != nil {
			t.Errorf("%s: parse: %v", c.match, err)
			continue
		}
		if measurements != c.measurements {
			t.Errorf("%s: merenja = %d, očekivano %d", c.match, measurements, c.measurements)
		}
		for _, f := range fields {
			if f[0] == "date" || f[0] == "start_time" || f[0] == "project_number" || f[0] == "section_nvt" {
				if strings.TrimSpace(f[1]) == "" {
					t.Errorf("%s: prazno polje %s", c.match, f[0])
				}
			}
		}
	}
}
