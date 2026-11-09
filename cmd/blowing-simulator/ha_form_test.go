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
