package main

import "testing"

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
