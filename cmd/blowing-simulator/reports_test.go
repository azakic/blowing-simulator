package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFormatClock(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, ""},
		{-5, ""},
		{45, "00:00:45"},
		{221 * 60, "03:41:00"},
		{3725, "01:02:05"},
	}
	for _, c := range cases {
		if got := formatClock(c.in); got != c.want {
			t.Errorf("formatClock(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatSeconds(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "—"},
		{59, "0m"},
		{221 * 60, "3h 41m"},
	}
	for _, c := range cases {
		if got := formatSeconds(c.in); got != c.want {
			t.Errorf("formatSeconds(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCSVSafe(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Oldenburger Koppel 10", "Oldenburger Koppel 10"},
		{"", ""},
		{"  Marko  ", "Marko"},
		{"=1+1", "'=1+1"},
		{"@SUM(A1)", "'@SUM(A1)"},
		{"-", "-"},
	}
	for _, c := range cases {
		if got := csvSafe(c.in); got != c.want {
			t.Errorf("csvSafe(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCSVFilenameSlug(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"SM209214964", "SM209214964"},
		{"HA 551/735", "HA_551_735"},
		{"../etc/passwd", "etc_passwd"},
		{"...", "project"},
	}
	for _, c := range cases {
		if got := csvFilenameSlug(c.in); got != c.want {
			t.Errorf("csvFilenameSlug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseReportFilter(t *testing.T) {
	r := httptest.NewRequest("GET", "/reports/projects?org=M.A.X.+Bauservice+GmbH&worker=Marko&from=2025-10-01&to=2025-10-31&junk=1", nil)
	f := parseReportFilter(r)

	if f.Org != "M.A.X. Bauservice GmbH" {
		t.Errorf("Org = %q", f.Org)
	}
	if f.Worker != "Marko" {
		t.Errorf("Worker = %q", f.Worker)
	}
	if f.From != "2025-10-01" || f.To != "2025-10-31" {
		t.Errorf("From/To = %q/%q", f.From, f.To)
	}
	if !f.HasAny() {
		t.Error("HasAny() = false, want true")
	}
}

func TestParseReportFilterRejectsInvalid(t *testing.T) {
	long := strings.Repeat("x", maxFilterValueLen+50)
	r := httptest.NewRequest("GET", "/reports/projects?org="+long+"&from=not-a-date&to=2025-13-45", nil)
	f := parseReportFilter(r)

	if len(f.Org) != maxFilterValueLen {
		t.Errorf("Org length = %d, want %d", len(f.Org), maxFilterValueLen)
	}
	if f.From != "" || f.To != "" {
		t.Errorf("invalid dates must be dropped, got From=%q To=%q", f.From, f.To)
	}
	if (reportFilter{}).HasAny() {
		t.Error("empty filter HasAny() = true")
	}
}

func TestReportFilterQueryString(t *testing.T) {
	f := reportFilter{Org: "M.A.X. Bauservice", Worker: "Marko", From: "2025-10-01"}
	want := "from=2025-10-01&org=M.A.X.+Bauservice&worker=Marko"
	if got := f.QueryString(); got != want {
		t.Errorf("QueryString() = %q, want %q", got, want)
	}
}
