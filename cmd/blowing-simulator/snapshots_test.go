package main

import (
	"encoding/json"
	"testing"
)

func TestBuildAddressSnapshot(t *testing.T) {
	raw := buildAddressSnapshot("Am Hang", "10", "58300", "Wetter (Ruhr)",
		"Am Hang 10, 58300 Wetter (Ruhr)", "1V3400", "KD-1", 7)
	if raw == nil {
		t.Fatal("snapshot je nil")
	}
	var got addressSnapshot
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Street != "Am Hang" || got.HouseNumber != "10" || got.Postcode != "58300" ||
		got.City != "Wetter (Ruhr)" || got.NVT != "1V3400" ||
		got.ExternalRef != "KD-1" || got.ConnectionID != 7 {
		t.Errorf("snapshot = %+v", got)
	}
	if got.TakenAt == "" {
		t.Error("taken_at je prazan")
	}
}

func TestBuildCustomerSnapshot(t *testing.T) {
	raw := buildCustomerSnapshot("Hausverwaltung Meier GmbH", "", "", "02335 1", "", "a@b.de")
	if raw == nil {
		t.Fatal("snapshot je nil")
	}
	var got customerSnapshot
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Company != "Hausverwaltung Meier GmbH" || got.Phone != "02335 1" || got.Email != "a@b.de" {
		t.Errorf("snapshot = %+v", got)
	}
	// Prazan klijent (importovani PDF) ostaje NULL.
	if buildCustomerSnapshot("", "", "", "", "", "") != nil {
		t.Error("prazan klijent treba da bude NULL")
	}
}
