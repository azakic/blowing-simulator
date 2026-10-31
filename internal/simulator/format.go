package simulator

import "strings"

// PDFFormat je tip protokola prepoznat iz teksta PDF-a.
type PDFFormat string

const (
	FormatFremco  PDFFormat = "fremco"
	FormatJetting PDFFormat = "jetting"
	FormatUnknown PDFFormat = "unknown"
)

// DetectFormat prepoznaje format po karakterističnim natpisima.
//
// Ranija detekcija u `main.go` je bila red `strings.Contains` sa niskim
// pragom (npr. samo „Uhrzeit" je bio dovoljan za Jetting), pa se Fremco
// dokument lako mogao svrstati u Jetting. Ovde se sabiraju težine i
// pobednik mora da ima dovoljno signala.
//
// Napomena: „Streckenlänge", „Geschwindigkeit", „Rohr-Druck", „Uhrzeit" i
// „Datum" postoje u oba formata — zato se ne boduju.
func DetectFormat(text string) PDFFormat {
	if strings.TrimSpace(text) == "" {
		return FormatUnknown
	}

	fremco := 0
	for _, sig := range []struct {
		needle string
		weight int
	}{
		{"SpeedNet", 5},
		{"Fremco", 4},
		{"MicroFlow", 4},
		{"Einblas - Protokoll", 3},
		{"Blowing distance", 3},
		{"Blowing time", 3},
		{"Streckenlänge [m]", 2},
		{"Drehmoment [%]", 2},
	} {
		if strings.Contains(text, sig.needle) {
			fremco += sig.weight
		}
	}

	jetting := 0
	for _, sig := range []struct {
		needle string
		weight int
	}{
		{"Schubkraft", 4},
		{"Einblasdruck", 3},
		{"Jetting", 3},
		{"Länge [m]", 2},
		{"Messwerte", 2},
		{"Lufttemperatur", 2},
		{"Zeit - Dauer", 2},
	} {
		if strings.Contains(text, sig.needle) {
			jetting += sig.weight
		}
	}

	switch {
	case fremco >= 4 && fremco > jetting:
		return FormatFremco
	case jetting >= 3 && jetting > fremco:
		return FormatJetting
	}
	return FormatUnknown
}
