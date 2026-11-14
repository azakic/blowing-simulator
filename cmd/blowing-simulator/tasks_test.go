package main

import (
	"net/http/httptest"
	"testing"
)

func TestValidTaskStatus(t *testing.T) {
	ok := []string{TaskStatusOpen, TaskStatusInProgress, TaskStatusDone, TaskStatusCancelled}
	for _, s := range ok {
		if !validTaskStatus(s) {
			t.Errorf("validTaskStatus(%q) = false, očekivano true", s)
		}
	}
	for _, s := range []string{"", "OPEN", "finished", "done ", "deleted"} {
		if validTaskStatus(s) {
			t.Errorf("validTaskStatus(%q) = true, očekivano false", s)
		}
	}
}

func TestValidTaskPriority(t *testing.T) {
	for _, s := range []string{TaskPriorityLow, TaskPriorityNormal, TaskPriorityHigh} {
		if !validTaskPriority(s) {
			t.Errorf("validTaskPriority(%q) = false", s)
		}
	}
	for _, s := range []string{"", "urgent", "HIGH"} {
		if validTaskPriority(s) {
			t.Errorf("validTaskPriority(%q) = true", s)
		}
	}
}

func TestParseTaskFilterDefaultsToMine(t *testing.T) {
	r := httptest.NewRequest("GET", "/tasks", nil)
	f := parseTaskFilter(r)
	if !f.Mine {
		t.Error("bez parametra mine, zadaci treba da budu samo moji (Mine=true)")
	}
	if qs := f.queryString(); !contains(qs, "mine=1") {
		t.Errorf("queryString %q ne sadrži mine=1", qs)
	}
}

func TestParseTaskFilterMineOff(t *testing.T) {
	r := httptest.NewRequest("GET", "/tasks?mine=0", nil)
	f := parseTaskFilter(r)
	if f.Mine {
		t.Error("mine=0 mora isključiti filter")
	}
	if qs := f.queryString(); !contains(qs, "mine=0") {
		t.Errorf("queryString %q ne sadrži mine=0", qs)
	}

	// Forma šalje hidden mine=0 i checkbox mine=1 kada je filter uključen.
	r = httptest.NewRequest("GET", "/tasks?mine=0&mine=1", nil)
	if f := parseTaskFilter(r); !f.Mine {
		t.Error("kada postoji mine=1 u formi, filter mora biti uključen")
	}
}

func TestMinuteValues(t *testing.T) {
	got := minuteValues()
	want := []string{"00", "15", "30", "45"}
	if len(got) != len(want) {
		t.Fatalf("minuteValues = %v, očekivano %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("minuteValues = %v, očekivano %v", got, want)
		}
	}
}

func TestMinuteOptionsKeepsLegacy(t *testing.T) {
	if got := minuteOptions(""); len(got) != 4 {
		t.Fatalf("minuteOptions(prazno) = %v", got)
	}
	if got := minuteOptions("09:30"); len(got) != 4 {
		t.Fatalf("minuteOptions(09:30) = %v (ne sme duplikat)", got)
	}
	got := minuteOptions("09:37")
	if len(got) != 5 {
		t.Fatalf("minuteOptions(09:37) = %v", got)
	}
	found := false
	for _, m := range got {
		if m == "37" {
			found = true
		}
	}
	if !found {
		t.Fatalf("minuteOptions(09:37) ne sadrži 37: %v", got)
	}
}

func TestValidateTaskRequired(t *testing.T) {
	if msg := validateTaskRequired(1, "Am Hang 10", "1.25", "2026-11-08", "09:30"); msg != "" {
		t.Fatalf("validan unos vraća grešku: %q", msg)
	}
	cases := []struct {
		name                  string
		org                   int64
		addr, nvt, due, clock string
	}{
		{"organizacija", 0, "A", "1.1", "2026-11-08", "09:30"},
		{"adresa", 1, " ", "1.1", "2026-11-08", "09:30"},
		{"nvt", 1, "A", "", "2026-11-08", "09:30"},
		{"datum", 1, "A", "1.1", "", "09:30"},
		{"vreme", 1, "A", "1.1", "2026-11-08", ""},
	}
	for _, c := range cases {
		if msg := validateTaskRequired(c.org, c.addr, c.nvt, c.due, c.clock); msg == "" {
			t.Errorf("%s: očekivana greška", c.name)
		}
	}
}

func TestParseTaskFilter(t *testing.T) {
	r := httptest.NewRequest("GET", "/tasks?status=open&project=SM209214964&mine=1&org=2", nil)
	f := parseTaskFilter(r)
	if f.Status != TaskStatusOpen {
		t.Errorf("status = %q", f.Status)
	}
	if f.Project != "SM209214964" {
		t.Errorf("project = %q", f.Project)
	}
	if !f.Mine {
		t.Error("mine mora biti true")
	}
	if f.OrgID != 2 {
		t.Errorf("org = %d", f.OrgID)
	}

	qs := f.queryString()
	for _, want := range []string{"status=open", "project=SM209214964", "mine=1", "org=2"} {
		if !contains(qs, want) {
			t.Errorf("queryString %q ne sadrži %q", qs, want)
		}
	}
}

func TestTaskFilterDefaultOmitsStatus(t *testing.T) {
	f := taskFilter{Status: "active"}
	if qs := f.queryString(); contains(qs, "status=") {
		t.Errorf("podrazumevani 'active' ne treba u query string, dobijeno %q", qs)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func TestTaskClientName(t *testing.T) {
	cases := []struct{ first, last, want string }{
		{"Milan", "Petrović", "Milan Petrović"},
		{"", "Petrović", "Petrović"},
		{"Milan", "", "Milan"},
		{"  ", " ", ""},
	}
	for _, c := range cases {
		if got := taskClientName(c.first, c.last); got != c.want {
			t.Errorf("taskClientName(%q,%q) = %q, očekivano %q", c.first, c.last, got, c.want)
		}
	}
}

func TestMapsURL(t *testing.T) {
	got := mapsURL("An der Borg 25, Volmarstein")
	want := "https://www.google.com/maps/search/?api=1&query=An+der+Borg+25%2C+Volmarstein"
	if got != want {
		t.Errorf("mapsURL = %q, očekivano %q", got, want)
	}
	if mapsURL("   ") != "" {
		t.Error("prazna adresa mora dati prazan link")
	}
}
