package simulator

import "testing"

func TestValueUntilLabels(t *testing.T) {
	cases := []struct {
		rest   string
		labels []string
		want   string
	}{
		{"SNRVe 22x7x1,5 Bezeichnung: A-D 2Y 1x6", []string{"Bezeichnung:"}, "SNRVe 22x7x1,5"},
		{"SNR 7x1,5 Faserzahl: 6 Gleitmittel: Prelube 5000", []string{"Faserzahl:", "Gleitmittel:"}, "SNR 7x1,5"},
		{"Grau Kabel-Durchmesser: 2.5 Kompressor: Kaiser m17a", []string{"Kabel-Durchmesser:", "Kompressor:"}, "Grau"},
		{"Prelube 5000", []string{"Kompressor:"}, "Prelube 5000"},
	}
	for _, c := range cases {
		if got := valueUntilLabels(c.rest, c.labels...); got != c.want {
			t.Errorf("valueUntilLabels(%q) = %q, očekivano %q", c.rest, got, c.want)
		}
	}
}

// Stvarne linije iz Fremco PDF-a (pdftotext -layout). Pre popravke su davale
// PipeBundle="SNRVe", PipeType="SNR", ColorCoding sa 6 đubreta, Diameter=0.
func TestExtractFremcoEquipmentRealLines(t *testing.T) {
	lines := []string{
		"Einblasgerät:     Fremco MicroFlow LOG",
		"Controller S/N:              9328.5155",
		"Rohrverband:              SNRVe 22x7x1,5 Bezeichnung:                         A-D 2Y 1x6",
		"Rohr:                          SNR 7x1,5 Faserzahl:                                     6 Gleitmittel:                  Prelube 5000",
		"Farbe-Kennung:                     Grau Kabel-Durchmesser:                          2.5 Kompressor:                    Kaiser m17a",
		"Rohrinnenwand:                    Gerieft",
		"Kabel-Temperatur:                          16°C",
	}
	e := extractFremcoEquipment(lines)

	if e.Pipe.PipeBundle != "SNRVe 22x7x1,5" {
		t.Errorf("PipeBundle = %q", e.Pipe.PipeBundle)
	}
	if e.Pipe.PipeType != "SNR 7x1,5" {
		t.Errorf("PipeType = %q", e.Pipe.PipeType)
	}
	if len(e.Pipe.ColorCoding) != 1 || e.Pipe.ColorCoding[0] != "Grau" {
		t.Errorf("ColorCoding = %v", e.Pipe.ColorCoding)
	}
	if e.Cable.Diameter != 2.5 {
		t.Errorf("Cable.Diameter = %v", e.Cable.Diameter)
	}
	if e.Cable.FiberCount != 6 {
		t.Errorf("FiberCount = %d", e.Cable.FiberCount)
	}
	if e.Cable.Lubricant != "Prelube 5000" {
		t.Errorf("Lubricant = %q", e.Cable.Lubricant)
	}
	if e.Compressor.Model != "Kaiser m17a" {
		t.Errorf("Compressor = %q", e.Compressor.Model)
	}
	if e.Pipe.InnerWall != "Gerieft" {
		t.Errorf("InnerWall = %q", e.Pipe.InnerWall)
	}
}
