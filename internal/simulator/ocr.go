package simulator

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
)

// ocrMu serijalizuje rasterizaciju + OCR. Tesseract na 300 dpi troši ~136 MB,
// a bulk upload obrađuje do 5 fajlova paralelno — bez ovoga bi nekoliko
// gorutina lako prešlo memorijski limit pod-a (256–320 Mi).
var ocrMu sync.Mutex

// OCRPage vraća tekst jedne strane PDF-a (tesseract, nemački + engleski).
//
// Ako alati nisu instalirani, vraća grešku koju pozivalac može da ignoriše
// (OCR je nadogradnja, ne uslov za rad).
func OCRPage(pdfPath string, page, dpi int) (string, error) {
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		return "", fmt.Errorf("pdftoppm nije instaliran")
	}
	if _, err := exec.LookPath("tesseract"); err != nil {
		return "", fmt.Errorf("tesseract nije instaliran")
	}

	ocrMu.Lock()
	defer ocrMu.Unlock()

	dir, err := os.MkdirTemp("", "ocr-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	prefix := filepath.Join(dir, "page")
	if err := runTool("pdftoppm", "-r", strconv.Itoa(dpi), "-png",
		"-f", strconv.Itoa(page), "-l", strconv.Itoa(page), pdfPath, prefix); err != nil {
		return "", fmt.Errorf("pdftoppm: %w", err)
	}
	images, err := filepath.Glob(prefix + "*.png")
	if err != nil || len(images) == 0 {
		return "", fmt.Errorf("pdftoppm nije napravio sliku strane %d", page)
	}

	// U image-u su deu i eng; ako neki paket nedostaje, probaj ostale.
	var lastErr error
	for _, lang := range []string{"deu+eng", "deu", "eng"} {
		text, err := runToolOutput("tesseract", images[0], "stdout", "-l", lang, "--psm", "4")
		if err == nil {
			return text, nil
		}
		lastErr = err
	}
	return "", lastErr
}

func runTool(name string, args ...string) error {
	var stderr bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %s", name, firstLine(stderr.String()))
	}
	return nil
}

func runToolOutput(name string, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %s", name, firstLine(stderr.String()))
	}
	return stdout.String(), nil
}

func firstLine(s string) string {
	for _, line := range bytes.Split([]byte(s), []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			return string(bytes.TrimSpace(line))
		}
	}
	return ""
}
