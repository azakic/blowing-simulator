package main

import (
	"database/sql"
	"os"
	"path/filepath"
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

func TestTempPDFPath(t *testing.T) {
	tmp := os.TempDir()

	cases := map[string]string{
		"report.pdf":              filepath.Join(tmp, "report.pdf"),
		"../../etc/passwd":        filepath.Join(tmp, "passwd"),
		"/abs/path/protokoll.pdf": filepath.Join(tmp, "protokoll.pdf"),
		"":                        filepath.Join(tmp, "upload.pdf"),
		"..":                      filepath.Join(tmp, "upload.pdf"),
	}

	for in, want := range cases {
		if got := tempPDFPath(in); got != want {
			t.Errorf("tempPDFPath(%q) = %q, očekivano %q", in, got, want)
		}
	}
}

func TestSourceObjectKey(t *testing.T) {
	s := &SourceStore{prefix: "blowing/protocols/"}
	sha := "9f2c" + string([]byte("012345678901234567890123456789012345678901234567890123456789"))

	if got, want := s.objectKey(sha), "blowing/protocols/9f/"+sha+".pdf"; got != want {
		t.Errorf("objectKey = %q, očekivano %q", got, want)
	}
	if got, want := s.objectKey("ab"), "blowing/protocols/ab/ab.pdf"; got != want {
		t.Errorf("objectKey(2 znaka) = %q, očekivano %q", got, want)
	}
	// Hash kraći od 2 znaka ne sme da panikuje (nikad se ne javlja u praksi).
	if got, want := s.objectKey("a"), "blowing/protocols/a.pdf"; got != want {
		t.Errorf("objectKey(1 znak) = %q, očekivano %q", got, want)
	}
}

func TestSourceSHA256(t *testing.T) {
	// Stabilan vektor: SHA-256 od "abc".
	if got, want := sourceSHA256([]byte("abc")), "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"; got != want {
		t.Errorf("sourceSHA256 = %q, očekivano %q", got, want)
	}
}

func TestSanitizeHeaderFilename(t *testing.T) {
	in := "protokoll \"1\"" + "\r\n" + "X.pdf"
	if got, want := sanitizeHeaderFilename(in), "protokoll 1X.pdf"; got != want {
		t.Errorf("sanitizeHeaderFilename = %q, očekivano %q", got, want)
	}
}
