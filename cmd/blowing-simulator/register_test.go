package main

import "testing"

func TestValidateRegistration(t *testing.T) {
	cases := []struct {
		name     string
		email    string
		pw, pw2  string
		kind     string
		company  string
		orgID    int64
		wantFail bool
	}{
		{"worker ok", "a@b.de", "12345678", "12345678", RegKindWorker, "", 0, false},
		{"partner with org", "a@b.de", "12345678", "12345678", RegKindPartner, "", 2, false},
		{"partner with company text", "a@b.de", "12345678", "12345678", RegKindPartner, "M.A.X.", 0, false},
		{"partner without org or company", "a@b.de", "12345678", "12345678", RegKindPartner, "  ", 0, true},
		{"bad email", "nope", "12345678", "12345678", RegKindWorker, "", 0, true},
		{"short password", "a@b.de", "1234", "1234", RegKindWorker, "", 0, true},
		{"password mismatch", "a@b.de", "12345678", "87654321", RegKindWorker, "", 0, true},
		{"unknown kind", "a@b.de", "12345678", "12345678", "", "", 0, true},
	}
	for _, c := range cases {
		got := validateRegistration(c.email, c.pw, c.pw2, c.kind, c.company, c.orgID)
		if c.wantFail && got == "" {
			t.Errorf("%s: očekivana greška, dobijeno ok", c.name)
		}
		if !c.wantFail && got != "" {
			t.Errorf("%s: neočekivana greška %q", c.name, got)
		}
	}
}
