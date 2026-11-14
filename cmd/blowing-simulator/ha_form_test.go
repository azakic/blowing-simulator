package main

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
)

func TestHAColorOptions(t *testing.T) {
	if len(haRohrColors) != 5 {
		t.Fatalf("Rohr boja = %d, očekivano 5", len(haRohrColors))
	}
	if len(haRoehrchenColors) != 24 {
		t.Fatalf("Röhrchenfarbe = %d, očekivano 24", len(haRoehrchenColors))
	}
	seen := map[string]bool{}
	for _, c := range haRoehrchenColors {
		if seen[c.Key] {
			t.Fatalf("duplikat ključa %q", c.Key)
		}
		seen[c.Key] = true
	}
	solid, striped := 0, 0
	for _, c := range haRoehrchenColors {
		if c.Striped {
			striped++
		} else {
			solid++
		}
	}
	if solid != 12 || striped != 12 {
		t.Fatalf("solid=%d striped=%d", solid, striped)
	}
}

func TestHAFormRendersRohrAndRoehrchenColors(t *testing.T) {
	tmpl, err := template.New("ha-form.html").Funcs(template.FuncMap{
		"csrfToken": func() string { return "" },
	}).ParseFiles("../../web/templates/ha-form.html")
	if err != nil {
		t.Fatal(err)
	}
	h := haRow{ID: 1, RohrColor: "blau", RoehrchenColor: "rot-weiss"}
	h.Rohr.Valid, h.Rohr.Int64 = true, 2
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, map[string]any{
		"HA":              h,
		"IsEdit":          true,
		"PhotoKinds":      haSlotPhotoKinds,
		"PhotosByKind":    map[string]any{},
		"ONTs":            []ontRow{},
		"FiberColors":     haFiberColors,
		"RohrColors":      haRohrColors,
		"RoehrchenColors": haRoehrchenColors,
		"Selected":        []string{},
		"Projects":        []projectRow{},
		"Orgs":            []Org{},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Rohr (1–4)", "Rohrfarbe", "Röhrchenfarbe", `name="rohr" min="1" max="4"`} {
		if !strings.Contains(out, want) {
			t.Errorf("forma ne sadrži %q", want)
		}
	}
	if got := strings.Count(out, `name="roehrchen_color"`); got != 25 {
		t.Errorf("roehrchen_color radio = %d, očekivano 25 (24 + keine)", got)
	}
	if got := strings.Count(out, `name="rohr_color"`); got != 6 {
		t.Errorf("rohr_color radio = %d, očekivano 6 (5 + keine)", got)
	}
	if !strings.Contains(out, `value="rot-weiss" checked`) {
		t.Error("Röhrchenfarbe rot-weiss nije označena")
	}
	if !strings.Contains(out, `value="blau" checked`) {
		t.Error("Rohrfarbe blau nije označena")
	}
}

// Geolokacija po fotografiji: svaki slot ima skriveno polje, a skripta mora
// da traži svež fiks pri promeni slike (i pri zameni postojeće), jer koordinate
// zabeležene pri prvom izboru ne važe za kasnije zamenjenu sliku.
func TestHAFormGeolocationPerPhoto(t *testing.T) {
	tmpl, err := template.New("ha-form.html").Funcs(template.FuncMap{
		"csrfToken": func() string { return "" },
	}).ParseFiles("../../web/templates/ha-form.html")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, map[string]any{
		"HA":              haRow{ID: 1},
		"IsEdit":          true,
		"PhotoKinds":      haSlotPhotoKinds,
		"PhotosByKind":    map[string]any{},
		"ONTs":            []ontRow{},
		"FiberColors":     haFiberColors,
		"RohrColors":      haRohrColors,
		"RoehrchenColors": haRoehrchenColors,
		"Selected":        []string{},
		"Projects":        []projectRow{},
		"Orgs":            []Org{},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, k := range haSlotPhotoKinds {
		if !strings.Contains(out, `name="geo_`+k.Key+`"`) {
			t.Errorf("nema skrivenog polja geo_%s", k.Key)
		}
		if !strings.Contains(out, `data-kind="`+k.Key+`"`) {
			t.Errorf("nema data-kind za %s", k.Key)
		}
	}
	for _, want := range []string{
		`id="geo-btn"`,
		"standort", // id geo-status se dobija preko getElementById
		"FIX_TTL",
		"fillEmpty",
		"Foto-Overlay (OCR)",
	} {
		if !strings.Contains(strings.ToLower(out), strings.ToLower(want)) {
			t.Errorf("forma ne sadrži %q", want)
		}
	}
}

// Pri izmeni forma prikazuje već sačuvanu fotografiju po slotu, sa adresom i
// vremenom — da se vidi šta se zamenjuje.
func TestHAFormShowsExistingPhoto(t *testing.T) {
	tmpl, err := template.New("ha-form.html").Funcs(template.FuncMap{
		"csrfToken": func() string { return "" },
	}).ParseFiles("../../web/templates/ha-form.html")
	if err != nil {
		t.Fatal(err)
	}
	type formPhoto struct {
		ID      int64
		Caption string
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, map[string]any{
		"HA":           haRow{ID: 2},
		"IsEdit":       true,
		"PhotoKinds":   haSlotPhotoKinds,
		"PhotosByKind": map[string]*formPhoto{"bild2": {ID: 7, Caption: "Sauerbruchstraße 5 · 06.11.2026 13:37"}},
		"ONTs":         []ontRow{},
		"FiberColors":  haFiberColors,
		"RohrColors":   haRohrColors,
		"Selected":     []string{},
		"Projects":     []projectRow{},
		"Orgs":         []Org{},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{`src="/ha/photo?id=7"`, "Sauerbruchstraße 5", "noch kein Foto", `data-kind="bild1"`} {
		if !strings.Contains(out, want) {
			t.Errorf("forma ne sadrži %q", want)
		}
	}
}

func TestHAFormClientFieldsAndRemarks(t *testing.T) {
	tmpl, err := template.New("ha-form.html").Funcs(template.FuncMap{
		"csrfToken": func() string { return "" },
	}).ParseFiles("../../web/templates/ha-form.html")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, map[string]any{
		"HA":              haRow{ID: 1, ClientCompany: "Hausverwaltung Meier GmbH", Remarks: "Zugang hinten", StartTime: "13:52"},
		"IsEdit":          true,
		"PhotoKinds":      haSlotPhotoKinds,
		"PhotosByKind":    map[string]any{},
		"ONTs":            []ontRow{},
		"FiberColors":     haFiberColors,
		"RohrColors":      haRohrColors,
		"RoehrchenColors": haRoehrchenColors,
		"Selected":        []string{},
		"Projects":        []projectRow{},
		"Orgs":            []Org{},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		`name="client_company"`, `value="Hausverwaltung Meier GmbH"`,
		`name="client_email"`, `name="remarks"`, `Zugang hinten`,
		`name="start_time"`, `value="13:52"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("forma ne sadrži %q", want)
		}
	}
}

// Potpisi su dozvoljene vrste (za buduće provajdere), ali Westnetz HaP se
// potpisuje na papiru — zato ih forma ne prikazuje kao slotove.
func TestHAPhotoKindsUniqueAndSignatures(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range haPhotoKinds {
		if seen[k.Key] {
			t.Errorf("duplikat foto slota %q", k.Key)
		}
		seen[k.Key] = true
	}
	for _, k := range []string{"sig_monteur", "sig_pruefer", "sig_kunde"} {
		if !validHAKind(k) {
			t.Errorf("potpis %q nije dozvoljena vrsta", k)
		}
		for _, slot := range haSlotPhotoKinds {
			if slot.Key == k {
				t.Errorf("potpis %q ne treba da bude slot Westnetz forme", k)
			}
		}
	}
	if len(haSlotPhotoKinds) != 5 {
		t.Errorf("slotova = %d, očekivano 5 (Bild 1–4 + Licht test)", len(haSlotPhotoKinds))
	}
}

func TestParseHATime(t *testing.T) {
	cases := map[string]any{
		"13:52":    "13:52",
		" 09:05 ":  "09:05",
		"13:52:30": "13:52:30",
		"":         nil,
		"abc":      nil,
		"25:00":    nil,
	}
	for in, want := range cases {
		if got := parseHATime(in); got != want {
			t.Errorf("parseHATime(%q) = %#v, očekivano %#v", in, got, want)
		}
	}
}
