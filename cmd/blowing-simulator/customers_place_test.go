package main

import "testing"

func TestNVTKeyOf(t *testing.T) {
	cases := map[string]string{
		"1V2200":   "1v2200",
		"1 V 2200": "1v2200",
		" 1v2200 ": "1v2200",
		"1.10":     "1.10",
		"":         "",
	}
	for in, want := range cases {
		if got := nvtKeyOf(in); got != want {
			t.Errorf("nvtKeyOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRowMatchesPlace(t *testing.T) {
	row := customerRow{Postcode: "58300", City: "Wetter (Ruhr)"}
	cases := []struct {
		name           string
		postcode, city string
		want           bool
	}{
		{name: "ista PLZ", postcode: "58300", want: true},
		{name: "druga PLZ", postcode: "58313", want: false},
		{name: "grad tolerantno", city: "Wetter", want: true},
		{name: "drugi grad", city: "Herdecke", want: false},
		{name: "bez unosa", want: true},
	}
	for _, c := range cases {
		if got := rowMatchesPlace(row, c.postcode, c.city); got != c.want {
			t.Errorf("%s: rowMatchesPlace = %v, want %v", c.name, got, c.want)
		}
	}
	// Red bez mesta nikad ne isključuje pogodak.
	empty := customerRow{}
	if !rowMatchesPlace(empty, "58300", "Wetter") {
		t.Error("row without place should always match")
	}
}

func TestRowsSharePlace(t *testing.T) {
	wetter := customerRow{Postcode: "58300", City: "Wetter (Ruhr)"}
	wetter2 := customerRow{Postcode: "58300", City: "Wetter"}
	herdecke := customerRow{Postcode: "58313", City: "Herdecke"}
	unknown := customerRow{}

	if !rowsSharePlace(nil) || !rowsSharePlace([]customerRow{wetter}) {
		t.Error("empty/single row should share place")
	}
	if !rowsSharePlace([]customerRow{wetter, wetter2}) {
		t.Error("Wetter and Wetter (Ruhr) should be the same place")
	}
	if rowsSharePlace([]customerRow{wetter, herdecke}) {
		t.Error("Wetter and Herdecke are different places")
	}
	if !rowsSharePlace([]customerRow{wetter, unknown}) {
		t.Error("unknown place should not count as a difference")
	}
}

func TestNarrowCustomerRows(t *testing.T) {
	// Ista ulica i broj u dva mesta — NVT je jedini pouzdan razdvajač.
	withPlace := []customerRow{
		{ID: 1, NVT: "1V2200", Postcode: "58300", City: "Wetter (Ruhr)", Kunde: "A"},
		{ID: 2, NVT: "1V9900", Postcode: "58313", City: "Herdecke", Kunde: "B"},
	}
	// Direktorijum bez PLZ/grada (današnji uvoz iz Excela).
	noPlace := []customerRow{
		{ID: 1, NVT: "1V2200", Kunde: "A"},
		{ID: 2, NVT: "1V9900", Kunde: "B"},
	}
	onlyOne := []customerRow{{ID: 1, NVT: "1V2200", Kunde: "A"}}

	cases := []struct {
		name      string
		rows      []customerRow
		nvt       string
		postcode  string
		city      string
		wantIDs   []int64
		confident bool
	}{
		{"NVT razdvaja", withPlace, "1V2200", "", "", []int64{1}, true},
		{"NVT sa razmacima", withPlace, "1 v 2200", "", "", []int64{1}, true},
		{"NVT druga strana", withPlace, "1V9900", "", "", []int64{2}, true},
		{"PLZ razdvaja", withPlace, "", "58313", "", []int64{2}, true},
		{"grad razdvaja", withPlace, "", "", "Wetter", []int64{1}, true},
		{"grad Herdecke", withPlace, "", "", "Herdecke", []int64{2}, true},
		{"bez NVT i mesta", withPlace, "", "", "", []int64{1, 2}, false},
		{"nepoznat NVT", withPlace, "1V0000", "", "", []int64{1, 2}, false},
		{"mesto se ne poklapa", withPlace, "", "99999", "Nirgendwo", []int64{1, 2}, false},
		{"mesto nepoznato u bazi", noPlace, "", "", "Wetter", []int64{1, 2}, false},
		{"NVT bez mesta u bazi", noPlace, "1V9900", "", "", []int64{2}, true},
		{"jedan red", onlyOne, "", "", "Wetter", []int64{1}, true},
		{"prazan skup", nil, "1V2200", "", "", nil, false},
	}

	for _, c := range cases {
		got, confident := narrowCustomerRows(c.rows, c.nvt, c.postcode, c.city)
		if confident != c.confident {
			t.Errorf("%s: confident = %v, want %v", c.name, confident, c.confident)
		}
		var ids []int64
		for _, r := range got {
			ids = append(ids, r.ID)
		}
		if len(ids) != len(c.wantIDs) {
			t.Errorf("%s: ids = %v, want %v", c.name, ids, c.wantIDs)
			continue
		}
		for i := range ids {
			if ids[i] != c.wantIDs[i] {
				t.Errorf("%s: ids = %v, want %v", c.name, ids, c.wantIDs)
				break
			}
		}
	}
}
