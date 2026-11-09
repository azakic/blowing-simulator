package main

import "testing"

func TestParseOCRTextBild1(t *testing.T) {
	text := `Jul 21,2026 12:13:27
337° NW
25 An der Borg
Volmarstein
Wetter (Ruhr)
Arnsberg
Nordrhein-Westfalen
Altitude:276.6msnm
Speed:0.0km/h
Index number: 1359`
	res := parseOCRText(text)
	if res.Time != "21.07.2026 12:13" {
		t.Fatalf("time = %q", res.Time)
	}
	if res.Address != "An der Borg 25, Volmarstein" {
		t.Fatalf("address = %q", res.Address)
	}
	if res.HasCoords {
		t.Fatal("ne očekuju se koordinate")
	}
}

func TestParseOCRTextBild3(t *testing.T) {
	text := `RudoiisVireliow-Stralle 7
58300 Wetter (Ruhr)
Deutschland
+51.365908,+7.374781
141° SE
Freitag, 6. November 2026 um 16:18:20`
	res := parseOCRText(text)
	if !res.HasCoords || res.Lat != 51.365908 || res.Lon != 7.374781 {
		t.Fatalf("coords = %v %v %v", res.Lat, res.Lon, res.HasCoords)
	}
	if res.Time != "06.11.2026 16:18" {
		t.Fatalf("time = %q", res.Time)
	}
}

func TestParseOCRTextBild4(t *testing.T) {
	text := `Jul 21, 2026 11:56:08
307° NW
Altitude:276.6msnm
Speed:0.0km/h
Index number: 1356`
	res := parseOCRText(text)
	if res.Time != "21.07.2026 11:56" {
		t.Fatalf("time = %q", res.Time)
	}
	if res.Address != "" {
		t.Fatalf("address = %q", res.Address)
	}
}

func TestParseOCRTextGermanNumeric(t *testing.T) {
	res := parseOCRText("21.07.2026 14:05 Uhr\n48 Osthausstraße\nWetter (Ruhr)")
	if res.Time != "21.07.2026 14:05" {
		t.Fatalf("time = %q", res.Time)
	}
	if res.Address != "Osthausstraße 48, Wetter (Ruhr)" {
		t.Fatalf("address = %q", res.Address)
	}
}
