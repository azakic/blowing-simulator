package main

import "testing"

func TestProtocolHistoriesDedupAndSort(t *testing.T) {
	// Kako stižu iz baze: najnoviji prvo, ista adresa može da se ponovi kroz
	// više protokola (različiti datumi).
	rows := []protocolHistoryRow{
		{ID: 3, Address: "Oldeburger Koppel 20", ProtocolDate: "2025-12-09", ProjectNumber: "SM209214964", SectionNVT: "Oldeburger Koppel 20 / 1V3400"},
		{ID: 2, Address: "holsteiner weide 9", ProtocolDate: "2025-12-01", ProjectNumber: "SM209214964", SectionNVT: "Holsteiner Weide 9 / 1V3400"},
		{ID: 1, Address: "Oldeburger Koppel 20", ProtocolDate: "2025-11-01", ProjectNumber: "SM000000001", SectionNVT: "Oldeburger Koppel 20 / 1V3400"},
		{ID: 4, Address: "Haflinger Weg 7", ProtocolDate: "2025-12-09", ProjectNumber: "SM209214964", SectionNVT: "Haflinger Weg 7 / 1V3400"},
	}

	got := protocolHistories(rows, "1V3400")
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (jedna stavka po adresi)", len(got))
	}
	// Poređano po adresi (case-insensitive): Haflinger, holsteiner, Oldeburger.
	want := []string{"Haflinger Weg 7", "holsteiner weide 9", "Oldeburger Koppel 20"}
	for i, w := range want {
		if got[i].Address != w {
			t.Errorf("got[%d].Address = %q, want %q", i, got[i].Address, w)
		}
	}
	// Za istu adresu ostaje najnoviji protokol.
	if got[2].ProjectNumber != "SM209214964" {
		t.Errorf("stariji protokol je pobedio: %q", got[2].ProjectNumber)
	}
	// NVT se vadi iz section_nvt.
	if got[2].NVT != "1V3400" {
		t.Errorf("NVT = %q, want 1V3400", got[2].NVT)
	}
}

func TestProtocolHistoriesFallsBackToTypedNVT(t *testing.T) {
	rows := []protocolHistoryRow{{ID: 1, Address: "Dammstr.9", ProjectNumber: "SM1"}}
	got := protocolHistories(rows, "1V2200")
	if len(got) != 1 || got[0].NVT != "1V2200" {
		t.Fatalf("got %+v, want NVT 1V2200 iz unosa", got)
	}
}

func TestProtocolHistoriesSkipsEmptyAddress(t *testing.T) {
	rows := []protocolHistoryRow{
		{ID: 1, Address: "", SectionNVT: "1V3400"},
		{ID: 2, Address: "Am Weg 1", SectionNVT: "Am Weg 1 / 1V3400"},
	}
	got := protocolHistories(rows, "1V3400")
	if len(got) != 1 || got[0].Address != "Am Weg 1" {
		t.Fatalf("got %+v, want samo adresu Am Weg 1", got)
	}
}
