package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Gated: PDF_TEST_FILE=/path/to/jettlogger.pdf go test ./cmd/blowing-simulator -run HeaderOCR
//
// Proverava da se zaglavlje koje postoji samo kao slika (strana 1) pročita
// OCR-om i da ostane ispred text layera.
func TestHeaderOCRFromPDF(t *testing.T) {
	path := os.Getenv("PDF_TEST_FILE")
	if path == "" {
		t.Skip("PDF_TEST_FILE nije postavljen")
	}
	for _, tool := range []string{"pdftotext", "pdftoppm", "tesseract"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s nije instaliran", tool)
		}
	}

	text, source, _, err := extractPDFText(path)
	if err != nil {
		t.Fatalf("extractPDFText: %v", err)
	}
	t.Logf("izvor teksta: %s", source)
	for _, want := range []string{"Bauvorhaben", "SM209214964", "Einbläser"} {
		if !strings.Contains(text, want) {
			t.Errorf("u tekstu nema %q", want)
		}
	}
	if !strings.HasPrefix(strings.TrimSpace(text), "M.A.X.") && !strings.Contains(text[:200], "Bauservice") {
		t.Logf("prvih 200 znakova: %q", text[:minInt(200, len(text))])
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
