package main

import (
	"database/sql"
	"testing"
)

func TestSeesAllProtocols(t *testing.T) {
	cases := map[string]bool{
		"admin":   true,
		"ADMIN":   true,
		" editor": true,
		"viewer":  true,
		"partner": false,
		"PARTNER": false,
		"":        false,
		"garbage": false,
	}

	for role, want := range cases {
		if got := seesAllProtocols(role); got != want {
			t.Errorf("seesAllProtocols(%q) = %v, očekivano %v", role, got, want)
		}
	}
}

func TestCanAccessProtocol(t *testing.T) {
	own := sql.NullInt64{Int64: 7, Valid: true}
	other := sql.NullInt64{Int64: 8, Valid: true}
	unowned := sql.NullInt64{}

	cases := []struct {
		name  string
		role  string
		uid   int64
		owner sql.NullInt64
		want  bool
	}{
		{"admin vidi tuđe", "admin", 1, other, true},
		{"editor vidi tuđe", "editor", 1, other, true},
		{"viewer vidi tuđe", "viewer", 1, other, true},
		{"partner vidi svoje", "partner", 7, own, true},
		{"partner ne vidi tuđe", "partner", 7, other, false},
		{"partner ne vidi bez vlasnika", "partner", 7, unowned, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := canAccessProtocol(tc.role, tc.uid, tc.owner); got != tc.want {
				t.Fatalf("canAccessProtocol(%q, %d, %+v) = %v, očekivano %v",
					tc.role, tc.uid, tc.owner, got, tc.want)
			}
		})
	}
}
