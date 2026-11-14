package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
)

// Reverse geocoding: koordinate → adresa preko OpenStreetMap Nominatim
// servisa. Besplatan je, ali traži identifikacioni User-Agent i najviše
// jedan zahtev u sekundi; rezultati se keširaju u geocode_cache.
//
// GEOCODER_URL menja endpoint (npr. self-hosted Nominatim), a
// GEOCODER_DISABLED=1 isključuje slanje koordinata trećoj strani.

var (
	geoHTTP       = &http.Client{Timeout: 4 * time.Second}
	geoMu         sync.Mutex
	geoLast       time.Time
	geoMinGap     = 1100 * time.Millisecond
	geoCacheTTL   = 180 * 24 * time.Hour
	geoGeocodeMax = 1 << 20
)

func geocoderEnabled() bool {
	return strings.TrimSpace(os.Getenv("GEOCODER_DISABLED")) != "1"
}

func geocoderURL() string {
	u := strings.TrimSpace(os.Getenv("GEOCODER_URL"))
	if u == "" {
		u = "https://nominatim.openstreetmap.org"
	}
	return strings.TrimRight(u, "/")
}

func geocoderUserAgent() string {
	ua := strings.TrimSpace(os.Getenv("GEOCODER_USER_AGENT"))
	if ua == "" {
		ua = "Wolken-ASM-Reports/1.0 (+https://ha.wolken-asm.de)"
	}
	return ua
}

func ensureGeocodeSchema(db *sqlx.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS geocode_cache (
			lat_key    TEXT NOT NULL,
			lon_key    TEXT NOT NULL,
			address    TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (lat_key, lon_key)
		)`,
		// Keš za dopunu adrese (PLZ→grad, ulica+grad→PLZ).
		`CREATE TABLE IF NOT EXISTS geo_address_cache (
			query_key  TEXT PRIMARY KEY,
			postcode   TEXT NOT NULL DEFAULT '',
			city       TEXT NOT NULL DEFAULT '',
			ambiguous  BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// geoKey zaokružuje koordinate na ~11 m, da sve fotografije sa istog
// gradilišta pogode isti keš zapis.
func geoKey(v float64) string { return fmt.Sprintf("%.4f", v) }

type nominatimReverse struct {
	DisplayName string `json:"display_name"`
	Address     struct {
		HouseNumber  string `json:"house_number"`
		Road         string `json:"road"`
		Pedestrian   string `json:"pedestrian"`
		Postcode     string `json:"postcode"`
		City         string `json:"city"`
		Town         string `json:"town"`
		Village      string `json:"village"`
		Municipality string `json:"municipality"`
	} `json:"address"`
}

// reverseGeocode vraća adresu u obliku „Straße Hausnr., PLZ Ort“.
func reverseGeocode(ctx context.Context, lat, lon float64) (string, error) {
	if !geocoderEnabled() {
		return "", fmt.Errorf("geocoder disabled")
	}
	geoMu.Lock()
	if wait := time.Until(geoLast.Add(geoMinGap)); wait > 0 {
		time.Sleep(wait)
	}
	geoLast = time.Now()
	geoMu.Unlock()

	q := url.Values{}
	q.Set("format", "jsonv2")
	q.Set("lat", fmt.Sprintf("%.6f", lat))
	q.Set("lon", fmt.Sprintf("%.6f", lon))
	q.Set("zoom", "18")
	q.Set("addressdetails", "1")
	q.Set("accept-language", "de")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, geocoderURL()+"/reverse?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", geocoderUserAgent())
	req.Header.Set("Accept", "application/json")
	resp, err := geoHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("geocoder HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(geoGeocodeMax)))
	if err != nil {
		return "", err
	}
	var out nominatimReverse
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	road := out.Address.Road
	if road == "" {
		road = out.Address.Pedestrian
	}
	city := out.Address.City
	if city == "" {
		city = out.Address.Town
	}
	if city == "" {
		city = out.Address.Village
	}
	if city == "" {
		city = out.Address.Municipality
	}
	var parts []string
	if road != "" {
		line := road
		if out.Address.HouseNumber != "" {
			line += " " + out.Address.HouseNumber
		}
		parts = append(parts, line)
	}
	loc := strings.TrimSpace(out.Address.Postcode)
	if city != "" {
		if loc != "" {
			loc += " "
		}
		loc += city
	}
	if loc != "" {
		parts = append(parts, loc)
	}
	if len(parts) == 0 {
		return strings.TrimSpace(out.DisplayName), nil
	}
	return strings.Join(parts, ", "), nil
}

func cachedGeoAddress(ctx context.Context, lat, lon float64) (string, bool) {
	var row struct {
		Address   string    `db:"address"`
		CreatedAt time.Time `db:"created_at"`
	}
	err := db.GetContext(ctx, &row, `SELECT address, created_at FROM geocode_cache
		WHERE lat_key = $1 AND lon_key = $2`, geoKey(lat), geoKey(lon))
	if err != nil || strings.TrimSpace(row.Address) == "" {
		return "", false
	}
	if time.Since(row.CreatedAt) > geoCacheTTL {
		return "", false
	}
	return row.Address, true
}

func storeGeoAddress(ctx context.Context, lat, lon float64, address string) {
	if _, err := db.ExecContext(ctx, `INSERT INTO geocode_cache (lat_key, lon_key, address, created_at)
			VALUES ($1, $2, $3, NOW())
			ON CONFLICT (lat_key, lon_key) DO UPDATE SET address = EXCLUDED.address, created_at = NOW()`,
		geoKey(lat), geoKey(lon), address); err != nil {
		log.Printf("geocode cache save: %v", err)
	}
}

// applyPhotoGeoAddresses popunjava ha_photos.geo_address za svaku fotografiju
// koja ima GPS. Adresa je vezana za sliku, ne za HaP — fotografije mogu biti
// sa različitih lokacija.
func applyPhotoGeoAddresses(ctx context.Context, haID int64) {
	if !geocoderEnabled() {
		return
	}
	var photos []haPhotoRow
	if err := db.SelectContext(ctx, &photos, `SELECT id, lat, lon, geo_address
		FROM ha_photos
		WHERE ha_id = $1 AND lat IS NOT NULL AND lon IS NOT NULL
		  AND COALESCE(geo_address, '') = ''
		ORDER BY id LIMIT 10`, haID); err != nil {
		return
	}
	for _, p := range photos {
		lat, lon := p.Lat.Float64, p.Lon.Float64
		addr, ok := cachedGeoAddress(ctx, lat, lon)
		if !ok {
			var err error
			addr, err = reverseGeocode(ctx, lat, lon)
			if err != nil {
				log.Printf("geocode photo %d: %v", p.ID, err)
				continue
			}
			storeGeoAddress(ctx, lat, lon, addr)
		}
		if strings.TrimSpace(addr) == "" {
			continue
		}
		if _, err := db.ExecContext(ctx, `UPDATE ha_photos SET geo_address = $1 WHERE id = $2`, addr, p.ID); err != nil {
			log.Printf("geocode photo %d save: %v", p.ID, err)
		}
	}
}
