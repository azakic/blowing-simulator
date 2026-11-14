package main

import "testing"

func searchItem(road, number, postcode, town string) nominatimSearchItem {
	var it nominatimSearchItem
	it.Address.Road = road
	it.Address.HouseNumber = number
	it.Address.Postcode = postcode
	it.Address.Town = town
	return it
}

func TestCitiesFromSearch(t *testing.T) {
	items := []nominatimSearchItem{
		searchItem("", "", "58300", "Wetter (Ruhr)"),
		searchItem("", "", "58300", "Wetter (Ruhr)"),
		searchItem("", "", "58300", "Herdecke"),
	}
	got := citiesFromSearch(items)
	if len(got) != 2 || got[0] != "Wetter (Ruhr)" || got[1] != "Herdecke" {
		t.Fatalf("citiesFromSearch = %#v", got)
	}
	if got := citiesFromSearch([]nominatimSearchItem{searchItem("", "", "", "")}); len(got) != 0 {
		t.Fatalf("bez grada = %#v", got)
	}
}

func TestPostcodesFromSearch(t *testing.T) {
	items := []nominatimSearchItem{
		searchItem("Goethestraße", "28", "58300", "Wetter (Ruhr)"),
		// istoimena ulica u drugoj regiji — mora biti odbačena
		searchItem("Goethestraße", "28", "61184", "Karben"),
	}
	got := postcodesFromSearch(items, "Goethestraße 28", "Wetter")
	if len(got) != 1 || got[0] != "58300" {
		t.Fatalf("postcodesFromSearch = %#v", got)
	}

	// bez broja: dovoljno je da se ulica poklapa
	got = postcodesFromSearch(items, "Goethestraße", "Wetter")
	if len(got) != 1 || got[0] != "58300" {
		t.Fatalf("bez broja = %#v", got)
	}

	// dva različita PLZ-a za istu ulicu → nesigurno
	dup := []nominatimSearchItem{
		searchItem("Goethestraße", "28", "58300", "Wetter (Ruhr)"),
		searchItem("Goethestraße", "80", "58301", "Wetter (Ruhr)"),
	}
	if got := postcodesFromSearch(dup, "Goethestraße", "Wetter"); len(got) != 2 {
		t.Fatalf("dva PLZ = %#v", got)
	}
}
