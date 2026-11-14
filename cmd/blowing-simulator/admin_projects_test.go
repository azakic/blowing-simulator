package main

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
)

// Admin stranica projekata mora da se parsira sa istim funkcijama koje
// handler registruje — dummy lista u TestTemplatesParse je jednom propustila
// funkciju koja je u šablonu korišćena, a nije bila registrovana.
func TestAdminProjectsTemplateRenders(t *testing.T) {
	tmpl, err := template.New("admin-projects.html").Funcs(template.FuncMap{
		"csrfToken":     func() string { return "" },
		"ProviderLabel": projectProviderLabel,
	}).ParseFiles("../../web/templates/admin-projects.html")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, map[string]any{
		"Projects": []projectRow2{
			{ID: 1, Number: "VMS235612", Name: "Volmarstein", Status: "active", Provider: "westnetz"},
			{ID: 2, Number: "TST000001", Status: "active", Provider: "telekom"},
		},
		"Orgs":           []Org{},
		"Users":          []any{},
		"OrgsByProject":  map[int64][]projectOrgRow{},
		"UsersByProject": map[int64][]projectUserRow{},
		"Providers":      projectProviders,
		"IsAdmin":        true,
		"IsOrgAdmin":     false,
		"UserRole":       "admin",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"Westnetz (HaP 1.5.1)", "Telekom", "name=\"provider\""} {
		if !strings.Contains(out, want) {
			t.Errorf("stranica ne sadrži %q", want)
		}
	}
}
