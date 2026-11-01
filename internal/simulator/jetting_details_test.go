package simulator

import "testing"

func TestCleanOCRValue(t *testing.T) {
	cases := map[string]string{
		"I Gabocom":         "Gabocom",
		"l E9/125":          "E9/125",
		"glatt gerieft D":   "glatt gerieft",
		"glatt gerieft U)":  "glatt gerieft",
		"LS Cable | System": "LS Cable System",
		"Yangtze Optical":   "Yangtze Optical",
	}
	for in, want := range cases {
		if got := cleanOCRValue(in); got != want {
			t.Errorf("cleanOCRValue(%q) = %q, očekivano %q", in, got, want)
		}
	}
}

func TestNormalizePipeType(t *testing.T) {
	for in, want := range map[string]string{"l7x1,5": "7x1,5", "7x1,5": "7x1,5", "I7x1,5": "7x1,5"} {
		if got := normalizePipeType(in); got != want {
			t.Errorf("normalizePipeType(%q) = %q, očekivano %q", in, got, want)
		}
	}
}

func TestCleanColors(t *testing.T) {
	got := cleanColors([]string{"Grin", "weis", "Braun"})
	want := []string{"Grün", "Weiß", "Braun"}
	if len(got) != len(want) {
		t.Fatalf("cleanColors = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("cleanColors[%d] = %q, očekivano %q", i, got[i], want[i])
		}
	}
}

func TestLabelValueSkipsColumnHeader(t *testing.T) {
	lines := []string{
		"Einblasgerät / Kompressor",
		"and Cab] Einblasgerät",
		"| MJet V1/V2",
		"Gleitmittel micro",
	}
	if got := labelValue(lines, reEinblasgeraet); got != "MJet V1/V2" {
		t.Errorf("labelValue = %q, očekivano MJet V1/V2", got)
	}
	if got := labelValue(lines, reGleitmittel); got != "micro" {
		t.Errorf("labelValue(gleitmittel) = %q", got)
	}
}

func TestStreckenlangeWithoutUmlaut(t *testing.T) {
	lines := []string{"Streckenlange | 156"}
	if got := labelValue(lines, reStreckenl); got != "156" {
		t.Errorf("labelValue = %q, očekivano 156", got)
	}
}
