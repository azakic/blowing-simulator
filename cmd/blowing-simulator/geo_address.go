package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Dopunjavanje adrese preko Nominatim-a u oba smera:
//
//	PLZ → grad     58300 → Wetter (Ruhr)
//	ulica+grad → PLZ   Goethestraße 28 + Wetter → 58300
//
// Koristi isti limit kao reverse geocoding (1 zahtev/s) i kešira odgovore u
// geo_address_cache, jer se isti PLZ/grad ponavljaju kroz mnogo zadataka.

type nominatimSearchItem struct {
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

// geoAddressResult je odgovor za formu. Ambiguous znači da postoji više
// mogućnosti, pa forma ne popunjava ništa (bolje prazno nego pogrešno).
type geoAddressResult struct {
	Postcode  string `json:"postcode"`
	City      string `json:"city"`
	Ambiguous bool   `json:"ambiguous"`
}

func geoWait() {
	geoMu.Lock()
	if wait := time.Until(geoLast.Add(geoMinGap)); wait > 0 {
		time.Sleep(wait)
	}
	geoLast = time.Now()
	geoMu.Unlock()
}

func nominatimSearch(ctx context.Context, q url.Values) ([]nominatimSearchItem, error) {
	if !geocoderEnabled() {
		return nil, fmt.Errorf("geocoder disabled")
	}
	geoWait()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, geocoderURL()+"/search?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", geocoderUserAgent())
	req.Header.Set("Accept", "application/json")
	resp, err := geoHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("geocoder HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(geoGeocodeMax)))
	if err != nil {
		return nil, err
	}
	var items []nominatimSearchItem
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, err
	}
	return items, nil
}

// cityOf vraća grad/opštinu (Nominatim ih stavlja u jedno od četiri polja).
func cityOf(a nominatimSearchItem) string {
	for _, v := range []string{a.Address.City, a.Address.Town, a.Address.Village, a.Address.Municipality} {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func citiesFromSearch(items []nominatimSearchItem) []string {
	seen := map[string]bool{}
	var out []string
	for _, it := range items {
		c := cityOf(it)
		if c == "" || seen[strings.ToLower(c)] {
			continue
		}
		seen[strings.ToLower(c)] = true
		out = append(out, c)
	}
	return out
}

// sameStreet poredi ulicu (i broj ako je dat) sa pogotkom.
func sameStreet(item nominatimSearchItem, want string) bool {
	road := item.Address.Road
	if road == "" {
		road = item.Address.Pedestrian
	}
	st, no := splitStreetNumber(want)
	if no != "" {
		return streetKey(st, no) == streetKey(road, item.Address.HouseNumber)
	}
	return customerKey(road) == customerKey(st)
}

// sameCity toleriše razliku u zapisu („Wetter“ vs „Wetter (Ruhr)“).
func sameCity(a, b string) bool {
	x, y := customerKey(a), customerKey(b)
	if x == "" || y == "" {
		return false
	}
	if x == y {
		return true
	}
	if len(x) < 3 || len(y) < 3 {
		return false
	}
	return strings.Contains(x, y) || strings.Contains(y, x)
}

// postcodesFromSearch zadržava samo pogotke koji odgovaraju ulici i gradu
// (Nominatim ume da vrati istoimenu ulicu u drugoj regiji).
func postcodesFromSearch(items []nominatimSearchItem, street, city string) []string {
	seen := map[string]bool{}
	var out []string
	for _, it := range items {
		if !sameStreet(it, street) {
			continue
		}
		if city != "" && !sameCity(cityOf(it), city) {
			continue
		}
		p := strings.TrimSpace(it.Address.Postcode)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

type geoAddressCacheRow struct {
	Postcode  string    `db:"postcode"`
	City      string    `db:"city"`
	Ambiguous bool      `db:"ambiguous"`
	CreatedAt time.Time `db:"created_at"`
}

func cachedGeoAddressResult(ctx context.Context, key string) (geoAddressResult, bool) {
	var row geoAddressCacheRow
	err := db.GetContext(ctx, &row, `SELECT postcode, city, ambiguous, created_at
		FROM geo_address_cache WHERE query_key = $1`, key)
	if err != nil || time.Since(row.CreatedAt) > geoCacheTTL {
		return geoAddressResult{}, false
	}
	return geoAddressResult{Postcode: row.Postcode, City: row.City, Ambiguous: row.Ambiguous}, true
}

func storeGeoAddressResult(ctx context.Context, key string, res geoAddressResult) {
	if _, err := db.ExecContext(ctx, `INSERT INTO geo_address_cache
			(query_key, postcode, city, ambiguous, created_at)
		VALUES ($1,$2,$3,$4,NOW())
		ON CONFLICT (query_key) DO UPDATE SET postcode=$2, city=$3,
			ambiguous=$4, created_at=NOW()`,
		key, res.Postcode, res.City, res.Ambiguous); err != nil {
		log.Printf("geo address cache %q: %v", key, err)
	}
}

// cityByPostcode traži grad za poštanski broj — samo ako je jedinstven.
func cityByPostcode(ctx context.Context, plz string) geoAddressResult {
	key := "plz:" + customerKey(plz)
	if res, ok := cachedGeoAddressResult(ctx, key); ok {
		return res
	}
	q := url.Values{}
	q.Set("format", "jsonv2")
	q.Set("addressdetails", "1")
	q.Set("countrycodes", "de")
	q.Set("postalcode", plz)
	q.Set("limit", "5")
	q.Set("accept-language", "de")
	items, err := nominatimSearch(ctx, q)
	if err != nil {
		log.Printf("geo plz %q: %v", plz, err)
		return geoAddressResult{Ambiguous: true}
	}
	cities := citiesFromSearch(items)
	res := geoAddressResult{Ambiguous: len(cities) != 1}
	if len(cities) == 1 {
		res.City = cities[0]
	}
	storeGeoAddressResult(ctx, key, res)
	return res
}

// postcodeByStreet traži poštanski broj za ulicu i grad — samo ako je jedinstven.
func postcodeByStreet(ctx context.Context, street, city string) geoAddressResult {
	key := "street:" + customerKey(street) + "|" + customerKey(city)
	if res, ok := cachedGeoAddressResult(ctx, key); ok {
		return res
	}
	q := url.Values{}
	q.Set("format", "jsonv2")
	q.Set("addressdetails", "1")
	q.Set("countrycodes", "de")
	q.Set("street", street)
	q.Set("city", city)
	q.Set("limit", "10")
	q.Set("accept-language", "de")
	items, err := nominatimSearch(ctx, q)
	if err != nil {
		log.Printf("geo street %q %q: %v", street, city, err)
		return geoAddressResult{Ambiguous: true}
	}
	plz := postcodesFromSearch(items, street, city)
	res := geoAddressResult{Ambiguous: len(plz) != 1}
	if len(plz) == 1 {
		res.Postcode = plz[0]
	}
	storeGeoAddressResult(ctx, key, res)
	return res
}

// GeoAddressHandler dopunjuje adresu: PLZ→grad ili ulica+grad→PLZ.
//
//	GET /geo/address?plz=58300
//	GET /geo/address?street=Goethestraße%2028&city=Wetter
func GeoAddressHandler(w http.ResponseWriter, r *http.Request) {
	if GetAuthClaims(r) == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	plz := strings.TrimSpace(r.URL.Query().Get("plz"))
	city := strings.TrimSpace(r.URL.Query().Get("city"))
	street := strings.TrimSpace(r.URL.Query().Get("street"))

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	switch {
	case plz != "" && city == "":
		writeJSON(w, cityByPostcode(ctx, plz))
	case street != "" && city != "" && plz == "":
		writeJSON(w, postcodeByStreet(ctx, street, city))
	default:
		writeJSON(w, geoAddressResult{})
	}
}
