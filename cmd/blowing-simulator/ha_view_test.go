package main

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
)

func TestPhotoCaptionLine(t *testing.T) {
	p := haPhotoRow{GeoAddress: "Душана Јовановића 4, 11000 Stadtbezirk Voždovac"}
	p.TakenAt.Valid, p.TakenAt.String = true, "2026-11-09 14:30"
	got := photoCaptionLine(p)
	if want := "Душана Јовановића 4, 11000 Stadtbezirk Voždovac · 09.11.2026 14:30"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// Bez GPS-a i vremena natpis je prazan — adresa HaP-a se ne koristi.
	if got := photoCaptionLine(haPhotoRow{}); got != "" {
		t.Fatalf("prazno: %q", got)
	}
	// Samo vreme (npr. EXIF datum bez GPS-a).
	p2 := haPhotoRow{}
	p2.TakenAt.Valid, p2.TakenAt.String = true, "2026-11-09 14:30"
	if got := photoCaptionLine(p2); got != "09.11.2026 14:30" {
		t.Fatalf("samo vreme: %q", got)
	}
}

func renderHAView(t *testing.T, h haRow, p haPhotoRow) string {
	t.Helper()
	tmpl, err := template.New("ha-view.html").Funcs(template.FuncMap{
		"csrfToken":        func() string { return "" },
		"dateShort":        dateShort,
		"photoCaptionLine": photoCaptionLine,
	}).ParseFiles("../../web/templates/ha-view.html")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, map[string]any{
		"HA":          h,
		"Photos":      []haPhotoRow{p},
		"PhotoByKind": map[string]haPhotoRow{"bild1": p},
		"PhotoKinds":  haSlotPhotoKinds,
		"ONTs":        []ontRow{},
		"APLCodes":    []string{},
		"FiberColors": haFiberColors,
		"Selected":    []string{},
		"CanEdit":     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestHAViewPhotoCaptionShowsPhotoAddressAndTime(t *testing.T) {
	h := haRow{ID: 1, Address: "An der Borg 25", City: "Volmarstein", ProtocolDate: "2026-07-21"}
	p := haPhotoRow{ID: 5, Kind: "bild1", Bytes: 100}
	p.GeoAddress = "Душана Јовановића 4, 11000 Stadtbezirk Voždovac"
	p.TakenAt.Valid, p.TakenAt.String = true, "2026-11-09 14:30"
	p.Lat.Valid, p.Lat.Float64 = true, 44.753205
	p.Lon.Valid, p.Lon.Float64 = true, 20.498229

	out := renderHAView(t, h, p)
	want := "Душана Јовановића 4, 11000 Stadtbezirk Voždovac · 09.11.2026 14:30"
	if !strings.Contains(out, want) {
		t.Fatalf("natpis ispod slike ne sadrži %q", want)
	}
	// Adresa HaP-a ne sme da se pojavi ispod slike te lokacije.
	if strings.Contains(out, "An der Borg 25, Volmarstein · ") {
		t.Fatalf("ispod slike je adresa HaP-a")
	}
}

func TestHAViewPhotoCaptionWithoutGPSIsEmpty(t *testing.T) {
	h := haRow{ID: 2, Address: "Osthausstraße 48", City: "Volmarstein", ProtocolDate: "2026-07-21"}
	p := haPhotoRow{ID: 6, Kind: "bild1", Bytes: 100}

	out := renderHAView(t, h, p)
	if strings.Contains(out, "Osthausstraße 48, Volmarstein · ") {
		t.Fatalf("bez GPS-a ne treba prikazivati adresu HaP-a ispod slike")
	}
}
