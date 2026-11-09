package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestReverseGeocodeNominatim(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reverse" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("User-Agent je obavezan za Nominatim")
		}
		if r.URL.Query().Get("lat") == "" || r.URL.Query().Get("lon") == "" {
			t.Error("nedostaju koordinate")
		}
		io.WriteString(w, `{"display_name":"Osthausstraße 48, 58300 Wetter (Ruhr)",
			"address":{"road":"Osthausstraße","house_number":"48","postcode":"58300","city":"Wetter (Ruhr)"}}`)
	}))
	defer srv.Close()
	t.Setenv("GEOCODER_URL", srv.URL)
	oldGap := geoMinGap
	geoMinGap = 0
	defer func() { geoMinGap = oldGap }()

	addr, err := reverseGeocode(context.Background(), 51.3618, 7.3684)
	if err != nil {
		t.Fatal(err)
	}
	if addr != "Osthausstraße 48, 58300 Wetter (Ruhr)" {
		t.Fatalf("addr = %q", addr)
	}
}

func TestReverseGeocodeFallsBackToDisplayName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"display_name":"Irgendwo in Wetter","address":{}}`)
	}))
	defer srv.Close()
	t.Setenv("GEOCODER_URL", srv.URL)
	oldGap := geoMinGap
	geoMinGap = 0
	defer func() { geoMinGap = oldGap }()

	addr, err := reverseGeocode(context.Background(), 51.3618, 7.3684)
	if err != nil {
		t.Fatal(err)
	}
	if addr != "Irgendwo in Wetter" {
		t.Fatalf("addr = %q", addr)
	}
}

func TestReverseGeocodeDisabled(t *testing.T) {
	t.Setenv("GEOCODER_DISABLED", "1")
	if _, err := reverseGeocode(context.Background(), 51.3618, 7.3684); err == nil {
		t.Fatal("očekivana greška kada je geocoder isključen")
	}
}

func TestGeocoderRateLimit(t *testing.T) {
	oldGap := geoMinGap
	geoMinGap = 20 * time.Millisecond
	defer func() { geoMinGap = oldGap }()
	geoMu.Lock()
	geoLast = time.Time{}
	geoMu.Unlock()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"display_name":"x"}`)
	}))
	defer srv.Close()
	t.Setenv("GEOCODER_URL", srv.URL)

	start := time.Now()
	for i := 0; i < 2; i++ {
		if _, err := reverseGeocode(context.Background(), 1, 2); err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(start); elapsed < geoMinGap {
		t.Fatalf("dva poziva su bila brža od limita: %v", elapsed)
	}
}

func TestStreetMatches(t *testing.T) {
	cases := []struct {
		hap, reverse string
		want         bool
	}{
		{"An der Borg 25", "An der Borg 25, 58300 Wetter (Ruhr)", true},
		{"Osthausstraße 48", "Osthausstrasse 48, 58300 Wetter", true}, // ß -> ss
		{"An der Borg 25", "An der Zollschranke 4, 58300 Wetter (Ruhr)", false},
		{"", "Irgendwo 1", true},
	}
	for _, c := range cases {
		if got := streetMatches(c.hap, c.reverse); got != c.want {
			t.Errorf("streetMatches(%q, %q) = %v, očekivano %v", c.hap, c.reverse, got, c.want)
		}
	}
}
