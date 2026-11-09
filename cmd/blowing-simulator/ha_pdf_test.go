package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestFormatHaHelpers(t *testing.T) {
	if got := formatHaDate("2026-07-21"); got != "21.07.2026" {
		t.Errorf("formatHaDate = %q", got)
	}
	if got := dateShort("2026-07-21 12:13"); got != "21.07.2026 12:13" {
		t.Errorf("dateShort = %q", got)
	}
	if got := sanitizeHAFilename(`HaP_An der Borg 25_2026-07-21.pdf`); got != "HaP_An_der_Borg_25_2026-07-21.pdf" {
		t.Errorf("sanitizeHAFilename = %q", got)
	}
	if got := splitColors("rot,blau"); len(got) != 2 || got[1] != "blau" {
		t.Errorf("splitColors = %v", got)
	}
	if got := splitColors(" "); got != nil {
		t.Errorf("splitColors(prazno) = %v", got)
	}
	la, lo, ok := parseLatLonText("51.3618, 7.3684")
	if !ok || la != 51.3618 || lo != 7.3684 {
		t.Errorf("parseLatLonText = %v %v %v", la, lo, ok)
	}
	if _, _, ok := parseLatLonText("nije koordinata"); ok {
		t.Error("neispravan unos mora da vrati false")
	}
}

func TestBuildHAPDFProducesPDF(t *testing.T) {
	h := haRow{
		ID:              1,
		Auftragnehmer:   "Wolken ASM GmbH",
		Address:         "An der Borg 25",
		City:            "Volmarstein",
		ClientLastName:  "Stahl",
		ClientFirstName: "Hellmuth",
		ProtocolDate:    "2026-07-21",
		Monteur:         "Daniel Popovic",
		Pruefer:         "Marko Zakic",
		GVS:             "N-1.31",
		KassetteB:       "R38",
		MuxNumber:       "2",
		MuxFibers:       "38",
		CableCharge:     "0293437",
		FiberColors:     "rot,blau",
		CheckFiber:      true,
		CheckComplete:   true,
		CheckClean:      true,
	}
	h.Units.Valid, h.Units.Int64 = true, 1
	h.Splices.Valid, h.Splices.Int64 = true, 1
	h.CableLength.Valid, h.CableLength.Float64 = true, 379

	pdf, err := buildHAPDF(h, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-1.")) {
		t.Fatalf("izlaz nije PDF: %q", pdf[:16])
	}
	if !bytes.Contains(pdf, []byte("%%EOF")) {
		t.Fatal("PDF nema trailing EOF marker")
	}
	if len(pdf) < 3000 {
		t.Fatalf("PDF je sumnjivo mali: %d bajta", len(pdf))
	}
}

// buildExifJPEG pravi minimalni JPEG sa EXIF GPS blokom (za test parsera).
func buildExifJPEG() []byte {
	le := binary.LittleEndian

	// TIFF počinje na offsetu 0 u ovom baferu.
	buf := make([]byte, 0, 512)
	appendU32 := func(v uint32) { b := make([]byte, 4); le.PutUint32(b, v); buf = append(buf, b...) }
	appendU16 := func(v uint16) { b := make([]byte, 2); le.PutUint16(b, v); buf = append(buf, b...) }

	buf = append(buf, 'I', 'I')
	appendU16(42)
	appendU32(8) // IFD0 na 8

	// IFD0: 1 ulaz (GPS IFD pointer), sledeći IFD = 0
	ifd0 := len(buf)
	appendU16(1)
	appendU16(0x8825) // GPS IFD
	appendU16(4)      // LONG
	appendU32(1)
	gpsPtrPos := len(buf)
	appendU32(0) // popunjava se kasnije
	appendU32(0) // next IFD

	// GPS IFD
	gpsOff := len(buf)
	le.PutUint32(buf[gpsPtrPos:], uint32(gpsOff))
	gpsEntries := 5
	appendU16(uint16(gpsEntries))
	// 0x0001 LatRef = "N"
	appendU16(0x0001)
	appendU16(2) // ASCII
	appendU32(2)
	buf = append(buf, 'N', 0, 0, 0)
	// 0x0002 Lat = 51/1, 21/1, 42/1
	appendU16(0x0002)
	appendU16(5) // RATIONAL
	appendU32(3)
	latOffPos := len(buf)
	appendU32(0)
	// 0x0003 LonRef = "E"
	appendU16(0x0003)
	appendU16(2)
	appendU32(2)
	buf = append(buf, 'E', 0, 0, 0)
	// 0x0004 Lon = 7/1, 22/1, 6/1
	appendU16(0x0004)
	appendU16(5)
	appendU32(3)
	lonOffPos := len(buf)
	appendU32(0)
	// 0x0009 GPSStatus - preskačemo
	appendU16(0x0005) // AltRef (BYTE)
	appendU16(1)
	appendU32(1)
	buf = append(buf, 0, 0, 0, 0)
	appendU32(0) // next IFD

	// racionalni podaci
	latOff := len(buf)
	le.PutUint32(buf[latOffPos:], uint32(latOff))
	for _, v := range []uint32{51, 1, 21, 1, 42, 1} {
		appendU32(v)
	}
	lonOff := len(buf)
	le.PutUint32(buf[lonOffPos:], uint32(lonOff))
	for _, v := range []uint32{7, 1, 22, 1, 6, 1} {
		appendU32(v)
	}
	_ = ifd0

	payload := append([]byte("Exif\x00\x00"), buf...)
	out := []byte{0xFF, 0xD8, 0xFF, 0xE1}
	seg := make([]byte, 2)
	binary.BigEndian.PutUint16(seg, uint16(len(payload)+2))
	out = append(out, seg...)
	out = append(out, payload...)
	out = append(out, 0xFF, 0xD9)
	return out
}

func TestParseExifGPS(t *testing.T) {
	info := parseExif(buildExifJPEG())
	if !info.HasLocation {
		t.Fatal("GPS nije pročitan")
	}
	// 51°21'42" ≈ 51.36167 ; 7°22'06" ≈ 7.36833
	if info.Lat < 51.36 || info.Lat > 51.37 {
		t.Errorf("lat = %v", info.Lat)
	}
	if info.Lon < 7.36 || info.Lon > 7.38 {
		t.Errorf("lon = %v", info.Lon)
	}
	cap := formatExifCaption(info)
	if cap == "" {
		t.Error("caption ne sme biti prazan")
	}
}

func TestParseExifIgnoresNonJpeg(t *testing.T) {
	if info := parseExif([]byte("nije jpeg")); info.HasLocation || info.HasTimestamp {
		t.Error("ne-JPEG ne sme da vrati EXIF podatke")
	}
	if info := parseExif([]byte{0xFF, 0xD8, 0xFF, 0xD9}); info.HasLocation {
		t.Error("JPEG bez EXIF-a ne sme da vrati lokaciju")
	}
}

func TestPhotoStampText(t *testing.T) {
	h := haRow{Address: "An der Borg 25", City: "Volmarstein"}
	p := &haPhotoRow{}
	if got := photoStampText(p, h); got != "An der Borg 25, Volmarstein" {
		t.Fatalf("bez GPS-a: %q", got)
	}
	p.Lat.Valid, p.Lat.Float64 = true, 51.3618
	p.Lon.Valid, p.Lon.Float64 = true, 7.3684
	p.TakenAt.Valid, p.TakenAt.String = true, "2026-07-21 17:49"
	got := photoStampText(p, h)
	if !strings.Contains(got, "51.361800") || !strings.Contains(got, "7.368400") || !strings.Contains(got, "2026-07-21 17:49") {
		t.Fatalf("sa GPS-om: %q", got)
	}
}

func TestBuildHAPDFStampsLocationOnPhoto(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext nije instaliran")
	}
	h := haRow{
		ID:            9,
		Auftragnehmer: "Wolken ASM GmbH",
		Address:       "An der Borg 25",
		City:          "Volmarstein",
		ProtocolDate:  "2026-07-21",
	}
	p := haPhotoRow{ID: 1, Kind: "bild1", Bytes: 1}
	p.Lat.Valid, p.Lat.Float64 = true, 51.3618
	p.Lon.Valid, p.Lon.Float64 = true, 7.3684
	p.TakenAt.Valid, p.TakenAt.String = true, "2026-07-21 17:49"

	img := makeONTSamplePNG(t, []string{"W006PF5Z"})
	pdf, err := buildHAPDF(h, []haPhotoRow{p}, map[string][]byte{"bild1": img}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/stamp.pdf"
	if err := os.WriteFile(path, pdf, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("pdftotext", path, "-").Output()
	if err != nil {
		t.Fatalf("pdftotext: %v", err)
	}
	text := string(out)
	for _, want := range []string{"51.361800, 7.368400", "2026-07-21 17:49"} {
		if !strings.Contains(text, want) {
			t.Errorf("u PDF-u nema %q", want)
		}
	}
}
