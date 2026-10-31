package simulator

import "testing"

// Tekst je stvarni OCR izlaz (tesseract 5.5, -l deu+eng --psm 4, 400 dpi) sa
// prve strane „Jettlogger / Einblas-Protokoll" dokumenta. Namerno sadrži
// OCR greške („JetLoqger", „Einbläser" bez umlauta u nekim prolazima).
const jettingOCRHeader = `M.A.X. Bauservice GmbH

JetLoqger Einblas-Protokoll
Bauvorhaben SM209214964
Streckenabschnitt/ NVt Eiermarkt 14 B NVT 1V2200
Firma M.A.X. Bauservice GmbH
Einbläser Vladimir S
Bemerkungen
Zeit - Dauer [hh:mm:ss] 00:22:46 29.10.2025, 16:26 | Ort(GPS) 52.4935°;9.84684°;111.037 m
Rohrparameter Kabelparameter Einblasgerät / Kompressor
`

func TestExtractJettingHeaderOCR(t *testing.T) {
	h := extractJettingHeader(jettingOCRHeader)
	if h.ProjectNumber != "SM209214964" {
		t.Errorf("ProjectNumber = %q", h.ProjectNumber)
	}
	if h.SectionNVT != "Eiermarkt 14 B NVT 1V2200" {
		t.Errorf("SectionNVT = %q", h.SectionNVT)
	}
	if h.Company != "M.A.X. Bauservice GmbH" {
		t.Errorf("Company = %q", h.Company)
	}
	if h.Operator != "Vladimir S" {
		t.Errorf("Operator = %q", h.Operator)
	}
	// „Bemerkungen" je prazno — sledeći red je drugo polje, ne vrednost.
	if h.Remarks != "" {
		t.Errorf("Remarks = %q, očekivano prazno", h.Remarks)
	}
	if h.Date != "29.10.2025" || h.StartTime != "16:26" {
		t.Errorf("datum/vreme = %q %q", h.Date, h.StartTime)
	}
}

func TestExtractJettingHeaderFirmaWolken(t *testing.T) {
	h := extractJettingHeader(`Firma Wolken-ASM GMBH
Einblaser Vladimir S
Bemerkungen
Zeit - Dauer [hh:mm:ss] 00:10:00 30.10.2025, 08:05`)
	if h.Company != "Wolken-ASM GMBH" {
		t.Errorf("Company = %q", h.Company)
	}
	if h.Operator != "Vladimir S" {
		t.Errorf("Operator = %q", h.Operator)
	}
	if h.Date != "30.10.2025" || h.StartTime != "08:05" {
		t.Errorf("datum/vreme = %q %q", h.Date, h.StartTime)
	}
}

func TestLooksLikeLabel(t *testing.T) {
	labels := []string{
		"Zeit - Dauer [hh:mm:ss] 00:22:46 29.10.2025, 16:26",
		"Ort(GPS) 52.4935°;9.84684°",
		"Bemerkungen",
		"Rohrparameter Kabelparameter",
		"",
	}
	for _, l := range labels {
		if !looksLikeLabel(l) {
			t.Errorf("looksLikeLabel(%q) = false, očekivano true", l)
		}
	}
	if looksLikeLabel("Stoped 412m") {
		t.Error("looksLikeLabel(\"Stoped 412m\") = true, očekivano false")
	}
}
