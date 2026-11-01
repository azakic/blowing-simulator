package simulator

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// OCRWord je reč iz tesseract TSV izlaza, sa koordinatama u pikselima slike.
type OCRWord struct {
	Block, Par, Line, WordNum int
	Left, Top, Width, Height  int
	Conf                      float64
	Text                      string
}

// ParseTSV čita tesseract `tsv` izlaz.
func ParseTSV(r *bufio.Scanner) []OCRWord {
	var words []OCRWord
	first := true
	for r.Scan() {
		line := r.Text()
		if first {
			first = false
			continue // header
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 12 {
			continue
		}
		text := strings.TrimSpace(fields[11])
		if text == "" {
			continue
		}
		atoi := func(s string) int { v, _ := strconv.Atoi(s); return v }
		atof := func(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
		words = append(words, OCRWord{
			Block: atoi(fields[2]), Par: atoi(fields[3]), Line: atoi(fields[4]), WordNum: atoi(fields[5]),
			Left: atoi(fields[6]), Top: atoi(fields[7]), Width: atoi(fields[8]), Height: atoi(fields[9]),
			Conf: atof(fields[10]), Text: text,
		})
	}
	return words
}

// TextFromWords rekonstruiše tekst po linijama (isto kao tesseract stdout).
func TextFromWords(words []OCRWord) string {
	sorted := append([]OCRWord(nil), words...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Block != b.Block {
			return a.Block < b.Block
		}
		if a.Par != b.Par {
			return a.Par < b.Par
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.WordNum < b.WordNum
	})

	var out strings.Builder
	lastBlock, lastPar, lastLine := -1, -1, -1
	for _, w := range sorted {
		if w.Block != lastBlock || w.Par != lastPar || w.Line != lastLine {
			if lastBlock != -1 {
				out.WriteString("\n")
			}
			lastBlock, lastPar, lastLine = w.Block, w.Par, w.Line
		} else {
			out.WriteString(" ")
		}
		out.WriteString(w.Text)
	}
	out.WriteString("\n")
	return out.String()
}

// OCRPageWords rasterizuje stranu i vraća i tekst i reči sa koordinatama.
// Jedan tesseract prolaz (TSV) daje oba, pa nema duplog OCR-a.
func OCRPageWords(pdfPath string, page, dpi int) (string, []OCRWord, error) {
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		return "", nil, fmt.Errorf("pdftoppm nije instaliran")
	}
	if _, err := exec.LookPath("tesseract"); err != nil {
		return "", nil, fmt.Errorf("tesseract nije instaliran")
	}

	ocrMu.Lock()
	defer ocrMu.Unlock()

	dir, err := os.MkdirTemp("", "ocr-*")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(dir)

	prefix := filepath.Join(dir, "page")
	if err := runTool("pdftoppm", "-r", strconv.Itoa(dpi), "-png",
		"-f", strconv.Itoa(page), "-l", strconv.Itoa(page), pdfPath, prefix); err != nil {
		return "", nil, fmt.Errorf("pdftoppm: %w", err)
	}
	images, err := filepath.Glob(prefix + "*.png")
	if err != nil || len(images) == 0 {
		return "", nil, fmt.Errorf("pdftoppm nije napravio sliku strane %d", page)
	}

	var lastErr error
	for _, lang := range []string{"deu+eng", "deu", "eng"} {
		var stdout, stderr bytes.Buffer
		cmd := exec.Command("tesseract", images[0], "stdout", "-l", lang, "--psm", "4", "tsv")
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			lastErr = fmt.Errorf("tesseract: %s", firstLine(stderr.String()))
			continue
		}
		scanner := bufio.NewScanner(&stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		words := ParseTSV(scanner)
		return TextFromWords(words), words, nil
	}
	return "", nil, lastErr
}
