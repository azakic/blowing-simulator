package main

import (
	"context"
	"testing"
)

func TestConnectionParts(t *testing.T) {
	cases := []struct {
		name                                              string
		street, house, plz, city, address, nvt            string
		wantStreet, wantHouse, wantPlz, wantCity, wantNVT string
	}{
		{
			name:   "task: polja već razdvojena",
			street: "Am Hang", house: "10", plz: "58300", city: "Wetter",
			address: "Am Hang 10, 58300 Wetter", nvt: "1V3400",
			wantStreet: "Am Hang", wantHouse: "10", wantPlz: "58300", wantCity: "Wetter", wantNVT: "1V3400",
		},
		{
			name: "HaP: samo adresa + grad",
			city: "Wetter (Ruhr)", address: "Am Hang 10", nvt: "1.25",
			wantStreet: "Am Hang", wantHouse: "10", wantPlz: "", wantCity: "Wetter (Ruhr)", wantNVT: "1.25",
		},
		{
			name:       "HaP: mesto je u adresi",
			address:    "Goethestraße 28, 58300 Wetter (Ruhr)",
			wantStreet: "Goethestraße", wantHouse: "28", wantPlz: "58300", wantCity: "Wetter (Ruhr)",
		},
		{
			name:    "protokol: ulica+broj bez mesta",
			address: "Dammstr.9", nvt: "1V2200",
			// Bez razmaka parseAddress ne deli broj — ostaje u ulici.
			wantStreet: "Dammstr.9", wantNVT: "1V2200",
		},
		{
			name:    "prazna adresa",
			nvt:     "1V3400",
			wantNVT: "1V3400",
		},
	}
	for _, c := range cases {
		st, no, plz, ct, nv := connectionParts(c.street, c.house, c.plz, c.city, c.address, c.nvt)
		if st != c.wantStreet || no != c.wantHouse || plz != c.wantPlz || ct != c.wantCity || nv != c.wantNVT {
			t.Errorf("%s: got %q %q %q %q %q, want %q %q %q %q %q",
				c.name, st, no, plz, ct, nv, c.wantStreet, c.wantHouse, c.wantPlz, c.wantCity, c.wantNVT)
		}
	}
}

func TestUpsertConnectionRequiresStreet(t *testing.T) {
	id, err := upsertConnection(context.Background(), nil, "", "", "58300", "Wetter", "1V3400")
	if err != nil {
		t.Fatalf("err = %v, očekivano nil", err)
	}
	if id != 0 {
		t.Fatalf("id = %d, očekivano 0 bez ulice", id)
	}
}
