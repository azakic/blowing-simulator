package main

import (
	"bytes"
	"encoding/json"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"sort"
	"strings"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/aztec"
	"github.com/makiuchi-d/gozxing/datamatrix"
	"github.com/makiuchi-d/gozxing/oned"
	"github.com/makiuchi-d/gozxing/qrcode"
	"golang.org/x/image/draw"
)

// Čitanje barkodova sa ONT nalepnice. Na Nokia ONT-u tipično postoje tri
// barkoda (P/N, SN, MAC), a često i dodatne nalepnice (npr. S/N provajdera).
// Sliku dekodiramo u horizontalnim trakama da bismo dobili više kodova
// odjednom, jer standardni čitači vraćaju samo prvi pronađeni.

type ontCode struct {
	Value  string `json:"value"`
	Format string `json:"format"`
}

type ontCodeInfo struct {
	SN  string
	MAC string
	PN  string
}

const (
	ontDecodeMaxSide = 2400
	ontDecodeBands   = 8
)

type ontReaderSpec struct {
	format string
	reader gozxing.Reader
}

func ontFullReaders() []ontReaderSpec {
	return []ontReaderSpec{
		{"CODE_128", oned.NewCode128Reader()},
		{"CODE_39", oned.NewCode39Reader()},
		{"CODE_93", oned.NewCode93Reader()},
		{"ITF", oned.NewITFReader()},
		{"CODABAR", oned.NewCodaBarReader()},
		{"EAN_13", oned.NewEAN13Reader()},
		{"EAN_8", oned.NewEAN8Reader()},
		{"QR_CODE", qrcode.NewQRCodeReader()},
		{"DATA_MATRIX", datamatrix.NewDataMatrixReader()},
		{"AZTEC", aztec.NewAztecReader()},
	}
}

// ontBandReaders su 1D formati koje tražimo u trakama; 2D kodovi se traže na
// celoj slici (traka bi ih isekla).
func ontBandReaders() []ontReaderSpec {
	return []ontReaderSpec{
		{"CODE_128", oned.NewCode128Reader()},
		{"CODE_39", oned.NewCode39Reader()},
		{"CODE_93", oned.NewCode93Reader()},
		{"ITF", oned.NewITFReader()},
		{"CODABAR", oned.NewCodaBarReader()},
		{"EAN_13", oned.NewEAN13Reader()},
		{"EAN_8", oned.NewEAN8Reader()},
	}
}

// decodeONTImage vraća sve barkodove koje uspe da pročita sa slike.
func decodeONTImage(data []byte) ([]ontCode, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	img = scaleImageDown(img, ontDecodeMaxSide)
	codes := decodeONTBitmap(img)
	if len(codes) == 0 {
		codes = decodeONTBitmap(rotateImage90(img))
	}
	return codes, nil
}

func decodeONTBitmap(img image.Image) []ontCode {
	bitmap, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return nil
	}
	w, h := bitmap.GetWidth(), bitmap.GetHeight()
	hints := map[gozxing.DecodeHintType]interface{}{
		gozxing.DecodeHintType_TRY_HARDER: true,
	}
	seen := map[string]bool{}
	var out []ontCode
	add := func(res *gozxing.Result, format string) {
		if res == nil {
			return
		}
		v := strings.ToUpper(strings.TrimSpace(res.GetText()))
		if len(v) < 6 || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, ontCode{Value: v, Format: format})
	}

	// Cela slika: 1D + 2D.
	for _, spec := range ontFullReaders() {
		if res, err := spec.reader.Decode(bitmap, hints); err == nil {
			add(res, spec.format)
		}
	}

	// Horizontalne trake: svaki red nalepnice sa barkodom zasebno.
	if h > 80 {
		bandH := h / 4
		step := 0
		if ontDecodeBands > 1 {
			step = (h - bandH) / (ontDecodeBands - 1)
		}
		for i := 0; i < ontDecodeBands; i++ {
			y := i * step
			if y+bandH > h {
				y = h - bandH
			}
			sub, err := bitmap.Crop(0, y, w, bandH)
			if err != nil {
				continue
			}
			for _, spec := range ontBandReaders() {
				if res, err := spec.reader.Decode(sub, hints); err == nil {
					add(res, spec.format)
					break // jedan kod po traci je dovoljno
				}
			}
		}
	}
	return out
}

func scaleImageDown(img image.Image, maxSide int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	longest := w
	if h > longest {
		longest = h
	}
	if longest <= maxSide {
		return img
	}
	ratio := float64(maxSide) / float64(longest)
	nw, nh := int(float64(w)*ratio), int(float64(h)*ratio)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}

func rotateImage90(img image.Image) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(h-1-y, x, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// --- prepoznavanje koji je kod SN / MAC / P/N ---

func ontHasSNPrefix(v string) bool {
	for _, p := range []string{"ALCL", "ZTEG", "HWTC", "FHTT", "GNXS", "48575443"} {
		if strings.HasPrefix(v, p) {
			return true
		}
	}
	return false
}

func ontIsMAC(v string) bool {
	if len(v) != 12 {
		return false
	}
	for _, c := range v {
		if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func ontIsNokiaPN(v string) bool {
	if len(v) != 12 || !strings.HasPrefix(v, "3FE") {
		return false
	}
	for _, c := range v {
		if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// classifyONTCodes bira SN, MAC i P/N iz pročitanih kodova.
// Primere sa Nokia nalepnice: ALCLFD880FB9 (SN), 540463D8C39C (MAC),
// 3FE49165AAAA (P/N); M082431ALU00032905 i W006PF4W su dodatni kodovi.
func classifyONTCodes(codes []ontCode) ontCodeInfo {
	var info ontCodeInfo
	var rest []string
	for _, c := range codes {
		v := strings.ToUpper(strings.TrimSpace(c.Value))
		if v == "" {
			continue
		}
		switch {
		case info.PN == "" && ontIsNokiaPN(v):
			info.PN = v
		case info.MAC == "" && ontIsMAC(v):
			info.MAC = v
		default:
			rest = append(rest, v)
		}
	}
	for _, v := range rest {
		if ontHasSNPrefix(v) {
			info.SN = v
			break
		}
	}
	if info.SN == "" {
		info.SN = bestONTNode(rest, info)
	}
	return info
}

func bestONTNode(values []string, info ontCodeInfo) string {
	type cand struct {
		value string
		score int
	}
	var cands []cand
	for _, v := range values {
		if v == "" || v == info.MAC || v == info.PN {
			continue
		}
		score := 0
		hasDigit, hasLetter := false, false
		for _, c := range v {
			if c >= '0' && c <= '9' {
				hasDigit = true
			} else if c >= 'A' && c <= 'Z' {
				hasLetter = true
			}
		}
		if hasDigit && hasLetter {
			score += 2
		}
		if len(v) >= 10 && len(v) <= 16 {
			score += 2
		}
		if len(v) >= 8 && len(v) <= 24 {
			score++
		}
		if ontIsMAC(v) || ontIsNokiaPN(v) {
			score -= 5
		}
		cands = append(cands, cand{value: v, score: score})
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	if len(cands) == 0 {
		return ""
	}
	return cands[0].value
}

func pickONTSN(codes []ontCode) string {
	return classifyONTCodes(codes).SN
}

// --- JSON <-> ont_onts.codes ---

func parseONTCodesJSON(s string) []ontCode {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []ontCode
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

func encodeONTCodesJSON(codes []ontCode) string {
	if len(codes) == 0 {
		return ""
	}
	b, err := json.Marshal(codes)
	if err != nil {
		return ""
	}
	return string(b)
}
