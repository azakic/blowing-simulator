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
