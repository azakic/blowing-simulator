package simulator

import (
	"strings"
	"testing"
)

// Minimalni `pdftotext -bbox-layout` izlaz (jetting tabela, 2 reda).
const bboxJettingXML = `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><body><doc>
<page width="595" height="842">
<flow><block><line>
<word xMin="24.9" yMin="60.7" xMax="58.2" yMax="71.8">Länge</word>
<word xMin="116.4" yMin="60.7" xMax="194.4" yMax="71.8">Lufttemperatur</word>
<word xMin="207.9" yMin="60.7" xMax="240" yMax="71.8">Schubkraft</word>
<word xMin="299.5" yMin="60.7" xMax="340" yMax="71.8">Einblasdruck</word>
<word xMin="391.0" yMin="60.7" xMax="440" yMax="71.8">Geschwindigkeit</word>
<word xMin="482.5" yMin="60.7" xMax="502" yMax="71.8">Zeit</word>
<word xMin="505.8" yMin="60.7" xMax="510" yMax="71.8">-</word>
<word xMin="513.2" yMin="60.7" xMax="540" yMax="71.8">Dauer</word>
</line></block></flow>
<flow><block><line>
<word xMin="24.9" yMin="72.7" xMax="41.5" yMax="83.8">[m]</word>
<word xMin="116.4" yMin="72.7" xMax="140" yMax="83.8">[°C]</word>
<word xMin="207.9" yMin="72.7" xMax="230" yMax="83.8">[N]</word>
<word xMin="299.5" yMin="72.7" xMax="330" yMax="83.8">[bar]</word>
<word xMin="391.0" yMin="72.7" xMax="430" yMax="83.8">[m/min]</word>
<word xMin="482.5" yMin="72.7" xMax="520" yMax="83.8">[hh:mm:ss]</word>
</line></block></flow>
<flow><block><line>
<word xMin="24.9" yMin="88.7" xMax="41.5" yMax="99.8">0.0</word>
<word xMin="116.4" yMin="88.7" xMax="140" yMax="99.8">16.2</word>
<word xMin="207.9" yMin="88.7" xMax="230" yMax="99.8">1.7</word>
<word xMin="299.5" yMin="88.7" xMax="330" yMax="99.8">0.0</word>
<word xMin="391.0" yMin="88.7" xMax="430" yMax="99.8">0.0</word>
<word xMin="482.5" yMin="88.7" xMax="520" yMax="99.8">00:00:00</word>
</line></block></flow>
<flow><block><line>
<word xMin="24.9" yMin="104.7" xMax="41.5" yMax="115.8">1.0</word>
<word xMin="116.4" yMin="104.7" xMax="140" yMax="115.8">16.2</word>
<word xMin="207.9" yMin="104.7" xMax="230" yMax="115.8">12.6</word>
<word xMin="299.5" yMin="104.7" xMax="330" yMax="115.8">0.0</word>
<word xMin="391.0" yMin="104.7" xMax="430" yMax="115.8">18.1</word>
<word xMin="482.5" yMin="104.7" xMax="520" yMax="115.8">00:00:05</word>
</line></block></flow>
</page></doc></body></html>`

func TestParseBBoxXML(t *testing.T) {
	pages, err := ParseBBoxXML(strings.NewReader(bboxJettingXML))
	if err != nil {
		t.Fatalf("ParseBBoxXML: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("strana = %d, očekivano 1", len(pages))
	}
	if got := len(pageWords(pages[0])); got != 26 {
		t.Errorf("reči = %d, očekivano 26", got)
	}
}

func TestExtractMeasurementsBBoxJetting(t *testing.T) {
	pages, err := ParseBBoxXML(strings.NewReader(bboxJettingXML))
	if err != nil {
		t.Fatalf("ParseBBoxXML: %v", err)
	}
	rows := ExtractMeasurementsBBox(pages, "jetting")
	if len(rows) != 2 {
		t.Fatalf("merenja = %d, očekivano 2", len(rows))
	}
	if rows[0].Length != 0 || rows[0].Temperature != 16.2 || rows[0].Force != 1.7 || rows[0].Time != "00:00:00" {
		t.Errorf("prvi red = %+v", rows[0])
	}
	if rows[1].Speed != 18.1 || rows[1].Time != "00:00:05" {
		t.Errorf("drugi red = %+v", rows[1])
	}
}

func TestExtractMeasurementsBBoxSkipsBadRows(t *testing.T) {
	// Red bez vremena u poslednjoj koloni (npr. OCR šum) se odbacuje.
	bad := strings.Replace(bboxJettingXML, "00:00:05", "240", 1)
	pages, err := ParseBBoxXML(strings.NewReader(bad))
	if err != nil {
		t.Fatalf("ParseBBoxXML: %v", err)
	}
	if rows := ExtractMeasurementsBBox(pages, "jetting"); len(rows) != 1 {
		t.Errorf("merenja = %d, očekivano 1", len(rows))
	}
}
