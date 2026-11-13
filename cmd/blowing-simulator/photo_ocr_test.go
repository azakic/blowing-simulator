package main

import (
	"database/sql"
	"testing"
)

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

func TestDistanceKm(t *testing.T) {
	// Wetter -> Beograd je ~1.400 km.
	if d := distanceKm(51.365908, 7.374781, 44.752999, 20.498377); d < 1200 || d > 1600 {
		t.Fatalf("distance = %.0f km", d)
	}
	if d := distanceKm(51.365908, 7.374781, 51.366228, 7.374192); d > 0.2 {
		t.Fatalf("blizina = %.3f km", d)
	}
}

func TestOverlayWins(t *testing.T) {
	wetterLat := sql.NullFloat64{Float64: 51.366228, Valid: true}
	wetterLon := sql.NullFloat64{Float64: 7.374192, Valid: true}
	beogradLat := sql.NullFloat64{Float64: 44.752999, Valid: true}
	beogradLon := sql.NullFloat64{Float64: 20.498377, Valid: true}
	empty := sql.NullFloat64{}

	cases := []struct {
		name      string
		prevLat   sql.NullFloat64
		prevLon   sql.NullFloat64
		geoSource string
		lat, lon  float64
		want      bool
	}{
		{"nema prethodnih koordinata", empty, empty, "", 51.365908, 7.374781, true},
		{"lokacija uredjaja uvek gubi", beogradLat, beogradLon, "browser", 51.365908, 7.374781, true},
		{"EXIF na istom mestu", wetterLat, wetterLon, "exif", 51.365908, 7.374781, true},
		{"EXIF 1400 km daleko (OCR previd)", wetterLat, wetterLon, "exif", 44.752999, 20.498377, false},
	}
	for _, c := range cases {
		if got := overlayWins(c.prevLat, c.prevLon, c.geoSource, c.lat, c.lon); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
