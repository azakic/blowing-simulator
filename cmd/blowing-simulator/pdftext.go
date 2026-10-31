package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"blowing-simulator/internal/simulator"
)

// extractPDFText vraća tekst PDF-a za parsiranje i izvor teksta
// ("text", "ocr" ili "mixed").
//
// Primarni put je `pdftotext -layout`. Ako prva strana nema upotrebljiv text
// layer (npr. „Jettlogger / Einblas-Protokoll" ima zaglavlje kao sliku), doda
// se OCR prve strane — ispred text layera, da brojevi iz OCR-a ne uđu u
// tabelu merenja koja dolazi iz pravog teksta.
//
// OCR se može isključiti sa OCR_DISABLED=true; DPI i prag se menjaju sa
// OCR_DPI / OCR_MIN_CHARS.
func extractPDFText(pdfPath string) (string, string, error) {
	text, err := pdftotextStdout(pdfPath)
	if err != nil {
		return "", "", err
	}
	if ocrDisabled() {
		return text, "text", nil
	}

	page1, pageErr := pdftotextPageStdout(pdfPath, 1)
	if pageErr == nil && len(strings.TrimSpace(page1)) >= ocrMinChars() {
		return text, "text", nil
	}

	ocrText, ocrErr := simulator.OCRPage(pdfPath, 1, ocrDPI())
	if ocrErr != nil {
		log.Printf("OCR strane 1 preskočen (%s): %v", pdfPath, ocrErr)
		return text, "text", nil
	}
	log.Printf("OCR strane 1: %d znakova (text layer prve strane imao %d)", len(strings.TrimSpace(ocrText)), len(strings.TrimSpace(page1)))

	source := "mixed"
	if len(strings.TrimSpace(page1)) == 0 {
		source = "ocr"
	}
	return ocrText + "\n" + text, source, nil
}

func pdftotextStdout(pdfPath string) (string, error) {
	return pdftotextArgs(pdfPath, nil)
}

func pdftotextPageStdout(pdfPath string, page int) (string, error) {
	return pdftotextArgs(pdfPath, []string{"-f", strconv.Itoa(page), "-l", strconv.Itoa(page)})
}

func pdftotextArgs(pdfPath string, extra []string) (string, error) {
	args := append([]string{"-layout"}, extra...)
	args = append(args, pdfPath, "-")

	var stdout, stderr bytes.Buffer
	cmd := exec.Command("pdftotext", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pdftotext: %s", strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func ocrDisabled() bool {
	return strings.EqualFold(os.Getenv("OCR_DISABLED"), "true")
}

func ocrDPI() int {
	if v, err := strconv.Atoi(os.Getenv("OCR_DPI")); err == nil && v >= 150 && v <= 600 {
		return v
	}
	return 300
}

func ocrMinChars() int {
	if v, err := strconv.Atoi(os.Getenv("OCR_MIN_CHARS")); err == nil && v >= 0 {
		return v
	}
	return 40
}
