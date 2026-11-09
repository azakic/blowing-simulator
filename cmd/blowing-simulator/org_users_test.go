package main

import (
	"path/filepath"
	"testing"
	"text/template"
)

func TestOrgRoleManageable(t *testing.T) {
	cases := map[string]bool{
		RolePartner: true,
		" PARTNER ": true,
		RoleAdmin:   false,
		RoleEditor:  false,
		RoleViewer:  false,
		"":          false,
	}
	for role, want := range cases {
		if got := orgRoleManageable(role); got != want {
			t.Errorf("orgRoleManageable(%q) = %v, očekivano %v", role, got, want)
		}
	}
}

func TestSeatAvailable(t *testing.T) {
	cases := []struct {
		used, max int
		want      bool
	}{
		{0, 10, true},
		{9, 10, true},
		{10, 10, false},
		{11, 10, false},
	}
	for _, c := range cases {
		if got := seatAvailable(c.used, c.max); got != c.want {
			t.Errorf("seatAvailable(%d,%d) = %v, očekivano %v", c.used, c.max, got, c.want)
		}
	}
}

func TestValidateNewPassword(t *testing.T) {
	cases := []struct {
		pw, pw2 string
		fail    bool
	}{
		{"12345678", "12345678", false},
		{"1234567", "1234567", true},
		{"12345678", "87654321", true},
		{"", "", true},
	}
	for _, c := range cases {
		got := validateNewPassword(c.pw, c.pw2)
		if c.fail && got == "" {
			t.Errorf("%q/%q: očekivana greška, dobijeno ok", c.pw, c.pw2)
		}
		if !c.fail && got != "" {
			t.Errorf("%q/%q: neočekivana greška %q", c.pw, c.pw2, got)
		}
	}
}

// TestTemplatesParse hvata grešku u šablonu pre deploy-a: template greške se
// inače vide tek kada strana padne u produkciji.
func TestTemplatesParse(t *testing.T) {
	files, err := filepath.Glob("../../web/templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	inner, _ := filepath.Glob("../../web/templates/report/*.html")
	files = append(files, inner...)
	if len(files) == 0 {
		t.Fatal("nema šablona")
	}
	// Neke strane registruju pomoćne funkcije u FuncMap-u; za proveru sintakse
	// je dovoljno da ime postoji.
	funcs := template.FuncMap{}
	for _, name := range []string{"add", "sub", "dateOnly", "timeOnly", "dateFormat", "extractNVT", "formatTime", "csrfToken", "dotTime", "hours", "minutes", "hourOf", "minOf", "dateShort", "photoCaptionLine", "formatHaDate", "swatchStyle", "dash"} {
		funcs[name] = func(args ...any) string { return "" }
	}
	for _, f := range files {
		tmpl := template.New(filepath.Base(f)).Funcs(funcs)
		if _, err := tmpl.ParseFiles(f); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}
