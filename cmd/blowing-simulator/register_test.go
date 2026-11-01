package main

import (
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestValidateRegistration(t *testing.T) {
	cases := []struct {
		name     string
		email    string
		pw, pw2  string
		code     string
		wantFail bool
	}{
		{"sve ispravno", "a@b.de", "12345678", "12345678", "ABCD2345EFGH", false},
		{"bez koda", "a@b.de", "12345678", "12345678", "  ", true},
		{"loš email", "nope", "12345678", "12345678", "ABCD2345EFGH", true},
		{"kratka lozinka", "a@b.de", "1234", "1234", "ABCD2345EFGH", true},
		{"lozinke se ne poklapaju", "a@b.de", "12345678", "87654321", "ABCD2345EFGH", true},
	}
	for _, c := range cases {
		got := validateRegistration(c.email, c.pw, c.pw2, c.code)
		if c.wantFail && got == "" {
			t.Errorf("%s: očekivana greška, dobijeno ok", c.name)
		}
		if !c.wantFail && got != "" {
			t.Errorf("%s: neočekivana greška %q", c.name, got)
		}
	}
}

func TestFormTimingOK(t *testing.T) {
	build := func(ts time.Time, honeypot string) url.Values {
		v := url.Values{}
		v.Set("t", strconv.FormatInt(ts.Unix(), 10))
		v.Set(regHoneypotField, honeypot)
		return v
	}

	now := time.Now()
	cases := []struct {
		name string
		vals url.Values
		want bool
	}{
		{"prebrzo", build(now, ""), false},
		{"ok posle 5s", build(now.Add(-5*time.Second), ""), true},
		{"honeypot popunjen", build(now.Add(-5*time.Second), "http://spam.example"), false},
		{"istekla forma", build(now.Add(-2*time.Hour), ""), false},
		{"nema timestampa", url.Values{}, false},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "/register", strings.NewReader(c.vals.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if got := formTimingOK(r); got != c.want {
			t.Errorf("%s: formTimingOK() = %v, očekivano %v", c.name, got, c.want)
		}
	}
}
