package simulator

import "testing"

func TestDetectFormat(t *testing.T) {
	fremcoText := `SpeedNet-System                                  Einblas - Protokoll
Firma                           Wolken-ASM    GmbH
                                M.A.X. Bauservice GmbH        Einbläser: Marko
Streckenlänge [m]   Geschwindigkeit [m/min]   Rohr-Druck [bar]   Drehmoment [%]   Uhrzeit [hh:mm:ss]`
	if got := DetectFormat(fremcoText); got != FormatFremco {
		t.Errorf("fremco tekst = %q, očekivano %q", got, FormatFremco)
	}

	oldJettingText := `Einblasprotokoll
Länge [m]   Schubkraft [N]   Einblasdruck [bar]   Uhrzeit [hh:mm:ss]
Zeit - Dauer   00:12:33`
	if got := DetectFormat(oldJettingText); got != FormatJetting {
		t.Errorf("stari jetting = %q, očekivano %q", got, FormatJetting)
	}

	newJettingText := `Jetting Protokoll
Messwerte
Streckenlänge   Geschwindigkeit   Einblasdruck   Uhrzeit`
	if got := DetectFormat(newJettingText); got != FormatJetting {
		t.Errorf("novi jetting = %q, očekivano %q", got, FormatJetting)
	}

	if got := DetectFormat(""); got != FormatUnknown {
		t.Errorf("prazan tekst = %q, očekivano %q", got, FormatUnknown)
	}
	if got := DetectFormat("irgendein Text ohne Signale"); got != FormatUnknown {
		t.Errorf("bez signala = %q, očekivano %q", got, FormatUnknown)
	}
}

// Fremco dokument sadrži „Uhrzeit" i „Streckenlänge"; ranija detekcija bi ga
// mogla svrstati u Jetting.
func TestDetectFormatFremcoNotJetting(t *testing.T) {
	text := "SpeedNet-System\nUhrzeit\nStreckenlänge [m]\nDrehmoment [%]"
	if got := DetectFormat(text); got != FormatFremco {
		t.Errorf("got %q, očekivano %q", got, FormatFremco)
	}
}
