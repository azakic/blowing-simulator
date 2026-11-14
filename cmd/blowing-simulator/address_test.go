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
