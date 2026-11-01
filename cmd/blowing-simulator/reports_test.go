package main

import "testing"

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
