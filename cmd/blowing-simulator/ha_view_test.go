package main

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
)

func TestPhotoAddress(t *testing.T) {
	h := haRow{Address: "An der Borg 25", City: "Volmarstein"}
	if got := photoAddress(h); got != "An der Borg 25, Volmarstein" {
		t.Fatalf("bez geo adrese: %q", got)
	}
	h.GeoAddress = "An der Borg 25, 58300 Wetter (Ruhr)"
	if got := photoAddress(h); got != h.GeoAddress {
		t.Fatalf("geo adresa ima prednost: %q", got)
	}
	if got := photoAddress(haRow{}); got != "" {
		t.Fatalf("prazno: %q", got)
	}
}

func renderHAView(t *testing.T, h haRow, p haPhotoRow) string {
	t.Helper()
	tmpl, err := template.New("ha-view.html").Funcs(template.FuncMap{
		"csrfToken":    func() string { return "" },
		"dateShort":    dateShort,
		"photoAddress": photoAddress,
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

func TestHAViewPhotoCaptionShowsAddressAndTime(t *testing.T) {
	h := haRow{ID: 1, Address: "An der Borg 25", City: "Volmarstein", ProtocolDate: "2026-07-21"}
	h.GeoAddress = "An der Borg 25, 58300 Wetter (Ruhr)"
	p := haPhotoRow{ID: 5, Kind: "bild1", Bytes: 100}
	p.TakenAt.Valid, p.TakenAt.String = true, "2026-07-21 17:49"
	p.Lat.Valid, p.Lat.Float64 = true, 51.3618
	p.Lon.Valid, p.Lon.Float64 = true, 7.3684

	out := renderHAView(t, h, p)
	want := "An der Borg 25, 58300 Wetter (Ruhr) · 21.07.2026 17:49"
	if !strings.Contains(out, want) {
		t.Fatalf("natpis ispod slike ne sadrži %q", want)
	}
}

func TestHAViewPhotoCaptionFallsBackToHaPAddress(t *testing.T) {
	h := haRow{ID: 2, Address: "Osthausstraße 48", City: "Volmarstein", ProtocolDate: "2026-07-21"}
	p := haPhotoRow{ID: 6, Kind: "bild1", Bytes: 100}
	p.TakenAt.Valid, p.TakenAt.String = true, "2026-07-21 17:49"

	out := renderHAView(t, h, p)
	want := "Osthausstraße 48, Volmarstein · 21.07.2026 17:49"
	if !strings.Contains(out, want) {
		t.Fatalf("fallback natpis ne sadrži %q", want)
	}
}
