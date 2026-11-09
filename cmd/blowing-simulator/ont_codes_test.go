package main

import (
	"bytes"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"testing"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/code128"
)

// makeONTSamplePNG pravi test sliku sa vertikalno naslaganim Code128
// barkodovima (kao na Nokia nalepnici: P/N, SN, MAC).
func makeONTSamplePNG(t *testing.T, values []string) []byte {
	t.Helper()
	const width, height = 1200, 1200
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	y := 60
	for _, v := range values {
		bc, err := code128.Encode(v)
		if err != nil {
			t.Fatalf("code128.Encode(%q): %v", v, err)
		}
		scaled, err := barcode.Scale(bc, 900, 120)
		if err != nil {
			t.Fatalf("barcode.Scale: %v", err)
		}
		draw.Draw(img, image.Rect(150, y, 1050, y+120), scaled, image.Point{}, draw.Over)
		y += 200
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDecodeONTBarcodes(t *testing.T) {
	want := []string{"3FE49165AAAA", "ALCLFD880FB9", "540463D8C39C"}
	data := makeONTSamplePNG(t, want)
	codes, err := decodeONTImage(data)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, c := range codes {
		got[c.Value] = true
	}
	for _, v := range want {
		if !got[v] {
			t.Fatalf("barkod %s nije pročitan; dobijeno: %+v", v, codes)
		}
	}
}

func TestClassifyONTCodes(t *testing.T) {
	codes := []ontCode{
		{Value: "3FE49165AAAA"}, {Value: "ALCLFD880FB9"}, {Value: "540463D8C39C"},
		{Value: "M082431ALU00032905"}, {Value: "W006PF4W"},
	}
	info := classifyONTCodes(codes)
	if info.SN != "ALCLFD880FB9" {
		t.Errorf("SN = %q, očekivano ALCLFD880FB9", info.SN)
	}
	if info.MAC != "540463D8C39C" {
		t.Errorf("MAC = %q", info.MAC)
	}
	if info.PN != "3FE49165AAAA" {
		t.Errorf("PN = %q", info.PN)
	}
}

func TestONTCodesJSONRoundTrip(t *testing.T) {
	in := []ontCode{{Value: "ALCLFD880FB9", Format: "CODE_128"}, {Value: "W006PF4W", Format: "CODE_128"}}
	s := encodeONTCodesJSON(in)
	out := parseONTCodesJSON(s)
	if len(out) != 2 || out[0].Value != in[0].Value || out[1].Format != in[1].Format {
		t.Fatalf("round-trip = %+v", out)
	}
	if parseONTCodesJSON("") != nil {
		t.Fatal("prazan string mora dati nil")
	}
}

func TestAPLCodesJSON(t *testing.T) {
	in := []string{" w006pf5z ", "W006PF4W", "w006pf5z", ""}
	out := parseAPLCodesJSON(encodeAPLCodesJSON(in))
	if len(out) != 2 || out[0] != "W006PF5Z" || out[1] != "W006PF4W" {
		t.Fatalf("APL normalize = %+v", out)
	}
	if parseAPLCodesJSON("") != nil {
		t.Fatal("prazan APL string mora dati nil")
	}
}

// minimalni PNG sa samo IHDR chunk-om: DecodeConfig ga prihvata, pa test
// proverava pixel guard bez alokacije ogromne slike.
func hugePNGHeader(w, h uint32) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	payload := make([]byte, 13)
	payload[0] = byte(w >> 24)
	payload[1] = byte(w >> 16)
	payload[2] = byte(w >> 8)
	payload[3] = byte(w)
	payload[4] = byte(h >> 24)
	payload[5] = byte(h >> 16)
	payload[6] = byte(h >> 8)
	payload[7] = byte(h)
	payload[8] = 8 // bit depth
	payload[9] = 2 // truecolor
	crc := crc32.NewIEEE()
	crc.Write([]byte("IHDR"))
	crc.Write(payload)
	chunk := make([]byte, 0, 12+13)
	chunk = append(chunk, byte(0), byte(0), byte(0), byte(13))
	chunk = append(chunk, []byte("IHDR")...)
	chunk = append(chunk, payload...)
	sum := crc.Sum32()
	chunk = append(chunk, byte(sum>>24), byte(sum>>16), byte(sum>>8), byte(sum))
	b.Write(chunk)
	return b.Bytes()
}

func TestDecodeONTImageRejectsHugeImages(t *testing.T) {
	_, err := decodeONTImage(hugePNGHeader(8000, 6000))
	if err == nil {
		t.Fatal("očekivana greška za sliku veću od limita")
	}
}
