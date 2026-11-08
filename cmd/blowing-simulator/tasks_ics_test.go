package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// newFormRequest pravi POST zahtev sa parsiranom formom (za parseDue testove).
func newFormRequest(vals map[string]string) *http.Request {
	form := url.Values{}
	for k, v := range vals {
		form.Set(k, v)
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = req.ParseForm()
	return req
}

func sampleTask() taskRow {
	return taskRow{
		ID:       42,
		Project:  "SM209214964",
		Title:    "HA 12 — Oldenburger Koppel 10",
		Address:  "Oldenburger Koppel 10, 26123 Oldenburg",
		NVT:      "NVT 1V3400",
		DueDate:  sql.NullString{String: "2026-11-15", Valid: true},
		DueTime:  sql.NullString{String: "09:30", Valid: true},
		Status:   TaskStatusOpen,
		Priority: TaskPriorityHigh,
	}
}

func TestBuildTaskICTimedEvent(t *testing.T) {
	ics := string(buildTaskICS(sampleTask(), "https://ha.wolken-asm.de/tasks/view?id=42"))
	for _, want := range []string{
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"BEGIN:VEVENT",
		"UID:task-42@ha.wolken-asm.de",
		"DTSTART:20261115T093000",
		"DTEND:20261115T103000",
		"SUMMARY:HA: HA 12 — Oldenburger Koppel 10 (SM209214964)",
		"LOCATION:Oldenburger Koppel 10\\, 26123 Oldenburg",
		"TRIGGER:-PT1H",
		"END:VEVENT",
		"END:VCALENDAR",
	} {
		if !strings.Contains(ics, want) {
			t.Errorf("ICS ne sadrži %q\n---\n%s", want, ics)
		}
	}
	if !strings.Contains(ics, "NVT: NVT 1V3400") {
		t.Error("opis mora sadržati NVT")
	}
}

func TestBuildTaskICSAllDay(t *testing.T) {
	task := sampleTask()
	task.DueTime = sql.NullString{}
	ics := string(buildTaskICS(task, "https://example.test/tasks/view?id=42"))

	if !strings.Contains(ics, "DTSTART;VALUE=DATE:20261115") {
		t.Errorf("bez vremena događaj mora biti celodnevni\n%s", ics)
	}
	if !strings.Contains(ics, "DTEND;VALUE=DATE:20261116") {
		t.Errorf("celodnevni događaj se završava sledećeg dana\n%s", ics)
	}
	if strings.Contains(ics, "TRIGGER:") {
		t.Error("bez vremena nema podsetnika")
	}
}

func TestICSLineEndingsAndFolding(t *testing.T) {
	ics := string(buildTaskICS(sampleTask(), "https://ha.wolken-asm.de/tasks/view?id=42"))
	if !strings.HasPrefix(ics, "BEGIN:VCALENDAR\r\n") {
		t.Error("linije moraju da se završavaju sa CRLF")
	}
	for _, line := range strings.Split(ics, "\r\n") {
		if len([]byte(line)) > 75 {
			t.Errorf("linija preko 75 okteta (bez preloma): %q", line)
		}
	}
}

func TestICSEscapeAndHelpers(t *testing.T) {
	got := icsEscape("a;b,c\\d\ne")
	want := `a\;b\,c\\d\ne`
	if got != want {
		t.Errorf("icsEscape = %q, očekivano %q", got, want)
	}
	if got := addHour("20261115", "23:30"); got != "20261116T003000" {
		t.Errorf("addHour preko ponoći = %q", got)
	}
	if got := nextDay("20261231"); got != "20270101" {
		t.Errorf("nextDay preko godine = %q", got)
	}
	if got := dotTime("09:30"); got != "09.30" {
		t.Errorf("dotTime = %q", got)
	}
}

func TestParseDueValidation(t *testing.T) {
	cases := []struct {
		date, clock string
		wantErr     bool
	}{
		{"2026-11-15", "09:30", false},
		{"2026-11-15", "", false},
		{"2026-11-15", "09.30", false}, // nemačka notacija
		{"", "", false},
		{"2026-13-01", "", true},
		{"2026-11-15", "9:30", false}, // normalizuje se u 09:30
		{"", "09:30", true},
	}
	for _, c := range cases {
		r := newFormRequest(map[string]string{"due_date": c.date, "due_time": c.clock})
		_, _, msg := parseDue(r)
		if c.wantErr && msg == "" {
			t.Errorf("%q/%q: očekivana greška", c.date, c.clock)
		}
		if !c.wantErr && msg != "" {
			t.Errorf("%q/%q: neočekivana greška %q", c.date, c.clock, msg)
		}
	}
}
