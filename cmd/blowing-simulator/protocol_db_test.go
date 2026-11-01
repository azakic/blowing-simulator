package main

import (
	"testing"
	"time"
)

func TestExtractAddressFromSectionNVT(t *testing.T) {
	cases := map[string]string{
		"Dammstr.15 / 1V2200":            "Dammstr.15",
		"Eiermarkt 14 B NVT 1V2200":      "Eiermarkt 14 B",
		"Haflinger Weg 5/NVT1V3400":      "Haflinger Weg 5",
		"Oldenburger Koppel/NVT1V3400":   "Oldenburger Koppel",
		"Eiermarkt 13":                   "Eiermarkt 13",
		"":                               "",
		"  Mühlenstr 13f / NVt 1V2800  ": "Mühlenstr 13f",
	}
	for in, want := range cases {
		if got := extractAddressFromSectionNVT(in); got != want {
			t.Errorf("extractAddressFromSectionNVT(%q) = %q, očekivano %q", in, got, want)
		}
	}
}

func TestParseDuration(t *testing.T) {
	cases := map[string]time.Duration{
		"00:18:25": 18*time.Minute + 25*time.Second,
		"01:02:03": time.Hour + 2*time.Minute + 3*time.Second,
		"05:30":    5*time.Minute + 30*time.Second,
		"1h30m":    90 * time.Minute,
	}
	for in, want := range cases {
		got := parseDuration(in)
		if got == nil {
			t.Errorf("parseDuration(%q) = nil", in)
			continue
		}
		if *got != want {
			t.Errorf("parseDuration(%q) = %v, očekivano %v", in, *got, want)
		}
	}
	for _, bad := range []string{"", "-", "abc", "1:2:3:4"} {
		if got := parseDuration(bad); got != nil {
			t.Errorf("parseDuration(%q) = %v, očekivano nil", bad, *got)
		}
	}
}
