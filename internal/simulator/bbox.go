package simulator

import (
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// BBoxWord je jedna reč sa koordinatama iz `pdftotext -bbox-layout`.
type BBoxWord struct {
	XMin float64 `xml:"xMin,attr"`
	YMin float64 `xml:"yMin,attr"`
	XMax float64 `xml:"xMax,attr"`
	YMax float64 `xml:"yMax,attr"`
	Text string  `xml:",chardata"`
}

type bboxLine struct {
	Words []BBoxWord `xml:"word"`
}

type bboxBlock struct {
	Lines []bboxLine `xml:"line"`
}

type bboxFlow struct {
	Blocks []bboxBlock `xml:"block"`
}

type BBoxPage struct {
	Width   float64     `xml:"width,attr"`
	Height  float64     `xml:"height,attr"`
	Flows   []bboxFlow  `xml:"flow"`
	Blocks  []bboxBlock `xml:"block"`
	PageNum int         `xml:"-"`
}

type bboxDoc struct {
	Pages []BBoxPage `xml:"page"`
}

type bboxHTML struct {
	Body struct {
		Doc bboxDoc `xml:"doc"`
	} `xml:"body"`
}

// ParseBBoxXML čita izlaz `pdftotext -bbox-layout` i vraća strane sa rečima.
func ParseBBoxXML(r io.Reader) ([]BBoxPage, error) {
	var doc bboxHTML
	if err := xml.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("bbox xml: %w", err)
	}
	pages := doc.Body.Doc.Pages
	for i := range pages {
		pages[i].PageNum = i + 1
	}
	return pages, nil
}

// MeasurementsFromPDFBBox pokreće pdftotext -bbox-layout i izvlači merenja
// iz tabele. Ovo je nezavisno od `-layout` teksta: reči se grupišu po `y`
// (red) i koloni po `x`, pa spajanje kolona u tekstu više ne pravi grešku.
func MeasurementsFromPDFBBox(pdfPath, format string) ([]RenderRow, error) {
	cmd := exec.Command("pdftotext", "-bbox-layout", pdfPath, "-")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("pdftotext -bbox-layout: %w", err)
	}
	pages, err := ParseBBoxXML(strings.NewReader(string(out)))
	if err != nil {
		return nil, err
	}
	return ExtractMeasurementsBBox(pages, format), nil
}

// ExtractMeasurementsBBox vraća merenja iz koordinata.
func ExtractMeasurementsBBox(pages []BBoxPage, format string) []RenderRow {
	labels := bboxLabels(format)
	if labels == nil {
		return nil
	}

	var rows []RenderRow
	var anchors []float64 // xMin kolona; ostaje kroz strane bez zaglavlja

	for _, page := range pages {
		words := pageWords(page)
		if len(words) == 0 {
			continue
		}
		lines := groupWordsByY(words, 5.0)

		for _, line := range lines {
			if a, ok := matchHeaderAnchors(line, labels); ok {
				anchors = a
				continue
			}
			if len(anchors) == 0 {
				continue
			}
			if row, ok := lineToRow(line, anchors, format); ok {
				rows = append(rows, row)
			}
		}
	}
	return rows
}

func bboxLabels(format string) [][]string {
	switch format {
	case "fremco":
		return [][]string{
			{"Streckenlänge", "Länge"}, {"Geschwindigkeit"}, {"Rohr-Druck"},
			{"Drehmoment"}, {"Uhrzeit"},
		}
	case "jetting":
		return [][]string{
			{"Länge"}, {"Lufttemperatur"}, {"Schubkraft"}, {"Einblasdruck"},
			{"Geschwindigkeit"}, {"Zeit"},
		}
	}
	return nil
}

func pageWords(page BBoxPage) []BBoxWord {
	var words []BBoxWord
	appendFromBlocks := func(blocks []bboxBlock) {
		for _, b := range blocks {
			for _, l := range b.Lines {
				words = append(words, l.Words...)
			}
		}
	}
	for _, f := range page.Flows {
		appendFromBlocks(f.Blocks)
	}
	appendFromBlocks(page.Blocks)
	return words
}

// groupWordsByY grupiše reči u redove po yMin (tolerancija u PDF jedinicama).
func groupWordsByY(words []BBoxWord, tolerance float64) [][]BBoxWord {
	sorted := append([]BBoxWord(nil), words...)
	sort.Slice(sorted, func(i, j int) bool {
		if math.Abs(sorted[i].YMin-sorted[j].YMin) < tolerance {
			return sorted[i].XMin < sorted[j].XMin
		}
		return sorted[i].YMin < sorted[j].YMin
	})

	var lines [][]BBoxWord
	for _, w := range sorted {
		if len(lines) == 0 {
			lines = append(lines, []BBoxWord{w})
			continue
		}
		last := lines[len(lines)-1]
		if math.Abs(w.YMin-last[0].YMin) < tolerance {
			lines[len(lines)-1] = append(last, w)
			continue
		}
		lines = append(lines, []BBoxWord{w})
	}
	for i := range lines {
		sort.Slice(lines[i], func(a, b int) bool { return lines[i][a].XMin < lines[i][b].XMin })
	}
	return lines
}

// matchHeaderAnchors prepoznaje red zaglavlja i vraća xMin svake kolone.
func matchHeaderAnchors(line []BBoxWord, labels [][]string) ([]float64, bool) {
	anchors := make([]float64, 0, len(labels))
	next := 0
	for _, w := range line {
		if next >= len(labels) {
			break
		}
		for _, alias := range labels[next] {
			if strings.EqualFold(strings.TrimSpace(w.Text), alias) {
				anchors = append(anchors, w.XMin)
				next++
				break
			}
		}
	}
	if next != len(labels) {
		return nil, false
	}
	return anchors, true
}

// lineToRow raspoređuje reči reda u kolone po najbližem xMin i validira red.
func lineToRow(line []BBoxWord, anchors []float64, format string) (RenderRow, bool) {
	cells := make([]string, len(anchors))
	for _, w := range line {
		col := nearestAnchor(w.XMin, anchors)
		if col < 0 {
			continue
		}
		if cells[col] == "" {
			cells[col] = strings.TrimSpace(w.Text)
		} else {
			cells[col] += " " + strings.TrimSpace(w.Text)
		}
	}

	nums := make([]float64, 0, len(cells)-1)
	timeCell := strings.TrimSpace(cells[len(cells)-1])
	if timeCell == "" {
		return RenderRow{}, false
	}

	if format == "jetting" {
		if !isTimeFormat(timeCell) || len(cells) != 6 {
			return RenderRow{}, false
		}
		for i := 0; i < 5; i++ {
			v, err := strconv.ParseFloat(strings.TrimSpace(cells[i]), 64)
			if err != nil {
				return RenderRow{}, false
			}
			nums = append(nums, v)
		}
		return RenderRow{Length: nums[0], Temperature: nums[1], Force: nums[2], Pressure: nums[3], Speed: nums[4], Time: timeCell}, true
	}

	// fremco
	if !strings.Contains(timeCell, ":") || len(cells) != 5 {
		return RenderRow{}, false
	}
	for i := 0; i < 4; i++ {
		v, err := strconv.ParseFloat(strings.TrimSpace(cells[i]), 64)
		if err != nil {
			return RenderRow{}, false
		}
		nums = append(nums, v)
	}
	return RenderRow{Length: nums[0], Speed: nums[1], Pressure: nums[2], Torque: nums[3], Time: timeCell}, true
}

func nearestAnchor(x float64, anchors []float64) int {
	best, bestDist := -1, math.MaxFloat64
	for i, a := range anchors {
		d := math.Abs(x - a)
		if d < bestDist {
			best, bestDist = i, d
		}
	}
	return best
}
