package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func aplRequest(form string) *http.Request {
	r := httptest.NewRequest("POST", "/ha/edit", strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

func TestResolveAPLCodesTouchedWins(t *testing.T) {
	r := httptest.NewRequest("POST", "/ha/edit",
		strings.NewReader(`apl_codes=["w006pf5z","W006PF5Z","W006PF4W"]&apl_codes_touched=1`))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	got := resolveAPLCodes(r, nil, 0)
	if len(got) != 2 || got[0] != "W006PF5Z" || got[1] != "W006PF4W" {
		t.Fatalf("got %v", got)
	}
}

// Kodovi koji su već sačuvani ne smeju da pokrenu dekodiranje sačuvane
// Bild 3 slike (haID=0 -> nema slike); rezultat ostaje nepromenjen.
func TestResolveAPLCodesKeepsExistingWithoutPhoto(t *testing.T) {
	r := httptest.NewRequest("POST", "/ha/edit",
		strings.NewReader(`apl_codes=["W006PF5Z","W006PF4W"]&apl_codes_touched=0`))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	got := resolveAPLCodes(r, nil, 0)
	if len(got) != 2 || got[0] != "W006PF5Z" || got[1] != "W006PF4W" {
		t.Fatalf("got %v", got)
	}
}
