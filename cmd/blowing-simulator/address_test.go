package main

import "testing"

func TestParseAddress(t *testing.T) {
	cases := []struct {
		in                             string
		street, number, postcode, city string
	}{
		{"Goethestraße 28", "Goethestraße", "28", "", ""},
		{"An der Borg 25", "An der Borg", "25", "", ""},
		{"Hauptstraße 3-5", "Hauptstraße", "3-5", "", ""},
		{"Am Weg 13a", "Am Weg", "13a", "", ""},
		{"Goethestraße 28, 58300 Wetter (Ruhr)", "Goethestraße", "28", "58300", "Wetter (Ruhr)"},
		{"Goethestraße 28, Volmarstein", "Goethestraße", "28", "", "Volmarstein"},
		{"Goethestraße 28 58300 Wetter", "Goethestraße", "28", "58300", "Wetter"},
		{"Volmarstein", "Volmarstein", "", "", ""},
		{"  Goethestraße   28  ", "Goethestraße", "28", "", ""},
		{"", "", "", "", ""},
	}
	for _, c := range cases {
		st, no, plz, city := parseAddress(c.in)
		if st != c.street || no != c.number || plz != c.postcode || city != c.city {
			t.Errorf("parseAddress(%q) = %q,%q,%q,%q; očekivano %q,%q,%q,%q",
				c.in, st, no, plz, city, c.street, c.number, c.postcode, c.city)
		}
	}
}

func TestFormatAddress(t *testing.T) {
	cases := []struct{ street, number, plz, city, want string }{
		{"Goethestraße", "28", "", "", "Goethestraße 28"},
		{"Goethestraße", "28", "58300", "Wetter", "Goethestraße 28, 58300 Wetter"},
		{"", "", "58300", "Wetter", "58300 Wetter"},
		{"An der Borg", "25", "", "Volmarstein", "An der Borg 25, Volmarstein"},
	}
	for _, c := range cases {
		if got := formatAddress(c.street, c.number, c.plz, c.city); got != c.want {
			t.Errorf("formatAddress = %q, očekivano %q", got, c.want)
		}
	}
}

func TestStreetKey(t *testing.T) {
	if got := streetKey("Goethestraße", "28"); got != "goethestraße 28" {
		t.Fatalf("streetKey = %q", got)
	}
	if got := streetKey("", "28"); got != "" {
		t.Fatalf("streetKey prazno = %q", got)
	}
}

func TestCustomerNameParts(t *testing.T) {
	cases := []struct{ in, first, last, company string }{
		{"Hellmuth Stahl", "Hellmuth", "Stahl", ""},
		{"Schneider, Jörg", "Jörg", "Schneider", ""},
		{"VWV GmbH", "", "", "VWV GmbH"},
		{"Haus- und Mietverwaltung Henniges", "", "", "Haus- und Mietverwaltung Henniges"},
		{"Herr Dr. Max Mustermann", "Max", "Mustermann", ""},
		{"", "", "", ""},
	}
	for _, c := range cases {
		f, l, co := customerNameParts(c.in)
		if f != c.first || l != c.last || co != c.company {
			t.Errorf("customerNameParts(%q) = %q,%q,%q; očekivano %q,%q,%q",
				c.in, f, l, co, c.first, c.last, c.company)
		}
	}
}

func TestNVTFromSection(t *testing.T) {
	cases := map[string]string{
		"Mühlenstr 13f / NVt 1V2800": "1V2800",
		"Dammstr.15 / 1V2200":        "1V2200",
		"Dammstr.11 NVT1V2200":       "1V2200",
		"Dammstr.3 / 1V2200_095658":  "1V2200",
		"Goethestraße 28 / NVT 1.09": "1.09",
		"":                           "",
		"nema nvt ovde":              "",
	}
	for in, want := range cases {
		if got := nvtFromSection(in); got != want {
			t.Errorf("nvtFromSection(%q) = %q, očekivano %q", in, got, want)
		}
	}
}

func TestAddressSlug(t *testing.T) {
	cases := map[string]string{
		"Dammstr. 9":     "dammstr9",
		"Mühlenstr 13f":  "mühlenstr13f",
		"An der Borg 25": "anderborg25",
		"  Dammstr.9  ":  "dammstr9",
	}
	for in, want := range cases {
		if got := addressSlug(in); got != want {
			t.Errorf("addressSlug(%q) = %q, očekivano %q", in, got, want)
		}
	}
}

func TestAddressMatchKey(t *testing.T) {
	cases := map[string]string{
		"Dammstr. 9":                  "dammstr9",
		"Dammstr. 9, 58300 Wetter":    "dammstr9",
		"An der Borg 25, Volmarstein": "anderborg25",
		"Mühlenstr 13f":               "mühlenstr13f",
	}
	for in, want := range cases {
		if got := addressMatchKey(in); got != want {
			t.Errorf("addressMatchKey(%q) = %q, očekivano %q", in, got, want)
		}
	}
}
