package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestDetectDelimiter(t *testing.T) {
	cases := map[string]rune{
		"Adresse;NVT;Kunde\n":   ';',
		"Adresse,NVT,Kunde\n":   ',',
		"Adresse\tNVT\tKunde\n": '\t',
		"Adresse|NVT|Kunde\n":   '|',
		"Adresse;NVT;Kunde;x\n": ';',
		"Adresse,NVT;Kunde;x\n": ';',
		"Adresse\n":             ',',
	}
	for in, want := range cases {
		if got := detectDelimiter([]byte(in)); got != want {
			t.Errorf("detectDelimiter(%q) = %q, očekivano %q", in, got, want)
		}
	}
}

func TestCleanCell(t *testing.T) {
	cases := map[string]string{
		"4.923349096E+11":     "492334909600",
		"2.33561725E8":        "233561725",
		"1.10":                "1.10",
		"1.09":                "1.09",
		"0":                   "",
		"0.0":                 "",
		"#N/A":                "",
		"  Am Kornacker  14 ": "Am Kornacker 14",
		"'0123":               "0123",
		"+49 2335 96030":      "+49 2335 96030",
		"VWV GmbH":            "VWV GmbH",
	}
	for in, want := range cases {
		if got := cleanCell(in); got != want {
			t.Errorf("cleanCell(%q) = %q, očekivano %q", in, got, want)
		}
	}
}

func TestParseCustomerCSVWithHeader(t *testing.T) {
	data := []byte("Adresse;NVT;Kunde;Telefon;Mobil\r\n" +
		"Am Hang 10;1.25;Heinz Schmidt;0;49233522201\r\n" +
		"Bachstraße 30;1.25;Frank Rambau;0179 751295;\r\n")
	pf, err := parseCustomerCSV("test.csv", data)
	if err != nil {
		t.Fatal(err)
	}
	if !pf.HeaderDetected {
		t.Fatal("zaglavlje nije prepoznato")
	}
	if len(pf.Rows) != 2 {
		t.Fatalf("rows = %d, očekivano 2", len(pf.Rows))
	}
	r := pf.Rows[0]
	if r.Address != "Am Hang 10" || r.NVT != "1.25" || r.Kunde != "Heinz Schmidt" {
		t.Errorf("prvi red = %+v", r)
	}
	if r.Phone != "" {
		t.Errorf("phone = %q, očekivano prazno (0)", r.Phone)
	}
	if r.Mobile != "49233522201" {
		t.Errorf("mobile = %q", r.Mobile)
	}
}

func TestParseCustomerCSVPositional(t *testing.T) {
	data := []byte("Am Kornacker 14,1.09,VWV GmbH,,+49 2335 96030\n")
	pf, err := parseCustomerCSV("test.csv", data)
	if err != nil {
		t.Fatal(err)
	}
	if pf.HeaderDetected {
		t.Fatal("ne očekuje se zaglavlje")
	}
	if len(pf.Rows) != 1 {
		t.Fatalf("rows = %d", len(pf.Rows))
	}
	r := pf.Rows[0]
	if r.Address != "Am Kornacker 14" || r.NVT != "1.09" || r.Kunde != "VWV GmbH" || r.Mobile != "+49 2335 96030" {
		t.Errorf("red = %+v", r)
	}
}

func TestParseCustomerXLSX(t *testing.T) {
	f := excelize.NewFile()
	defer f.Close()
	f.SetSheetName("Sheet1", "Leer")
	if _, err := f.NewSheet("Daten"); err != nil {
		t.Fatal(err)
	}
	rows := [][]string{
		{"Adresse", "NVT", "Tiefbau", "Kunde", "Telefon", "Mobil"},
		{"Am Hang 10", "1.25", "x", "Heinz Schmidt", "0", "4.9233522201E10"},
		{"Goethestraße 13a", "1.10", "", "Tim Fladrich", "0171 7640390", ""},
	}
	for i, row := range rows {
		for j, v := range row {
			cell, _ := excelize.CoordinatesToCellName(j+1, i+1)
			_ = f.SetCellValue("Daten", cell, v)
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	pf, err := parseCustomerFile("kunden.xlsx", buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !pf.HeaderDetected {
		t.Fatal("zaglavlje nije prepoznato")
	}
	if len(pf.Rows) != 2 {
		t.Fatalf("rows = %d, očekivano 2: %+v", len(pf.Rows), pf.Rows)
	}
	if pf.Rows[0].Mobile != "49233522201" {
		t.Errorf("mobile = %q", pf.Rows[0].Mobile)
	}
	if pf.Rows[1].Address != "Goethestraße 13a" {
		t.Errorf("address = %q", pf.Rows[1].Address)
	}
}

func TestParseCustomerXLSInvalid(t *testing.T) {
	if _, err := parseCustomerXLS([]byte("ovo nije xls")); err == nil {
		t.Fatal("očekivana greška za neispravan xls")
	}
	if _, err := parseCustomerXLSX([]byte("ovo nije xlsx")); err == nil {
		t.Fatal("očekivana greška za neispravan xlsx")
	}
}

func TestSplitKunde(t *testing.T) {
	cases := []struct {
		in, first, last string
	}{
		{"Heinz Schmidt", "Heinz", "Schmidt"},
		{"Schneider, Jörg", "Jörg", "Schneider"},
		{"VWV GmbH", "", "VWV GmbH"},
		{"Meiners + Hinz Hausverwaltung OHG Heike Meiners", "", "Meiners + Hinz Hausverwaltung OHG Heike Meiners"},
		{"Herr Fricke", "", "Fricke"},
		{"Dr. Suliman Awad", "Suliman", "Awad"},
		{"Sarah Plith WEG", "", "Sarah Plith WEG"},
		{"Pelé", "", "Pelé"},
	}
	for _, c := range cases {
		first, last := splitKunde(c.in)
		if first != c.first || last != c.last {
			t.Errorf("splitKunde(%q) = (%q, %q), očekivano (%q, %q)", c.in, first, last, c.first, c.last)
		}
	}
}

func TestDedupeRows(t *testing.T) {
	rows := []customerRow{
		{NVT: "1.25", Address: "A", Kunde: "prvi"},
		{NVT: "1.25", Address: "A", Kunde: "drugi"},
		{NVT: "", Address: "B", Kunde: "bez nvt"},
		{NVT: "", Address: "B", Kunde: "bez nvt 2"},
	}
	out := dedupeRows(rows)
	if len(out) != 2 {
		t.Fatalf("len = %d, očekivano 2", len(out))
	}
	if out[0].Kunde != "drugi" {
		t.Errorf("poslednji red treba da pobeđuje: %+v", out[0])
	}
	if out[1].Kunde != "bez nvt 2" {
		t.Errorf("adresa dedupe: %+v", out[1])
	}
}

// TestParseVolmarsteinSample je lokalna provera na pravom fajlu. U CI se
// preskače; pokreni sa:
//
//	CUSTOMER_SAMPLE="/Users/azakic/Baustand Hausanschlüsse Volmarstein 13.02.2025.xlsx" go test ./cmd/blowing-simulator -run Volmarstein -v
func TestParseVolmarsteinSample(t *testing.T) {
	path := os.Getenv("CUSTOMER_SAMPLE")
	if path == "" {
		t.Skip("CUSTOMER_SAMPLE nije postavljen")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skip(err)
	}
	pf, err := parseCustomerFile(path, data)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("rows=%d header=%v format=%s sheets=%d", len(pf.Rows), pf.HeaderDetected, pf.Format, pf.Sheets)
	if !pf.HeaderDetected {
		t.Fatal("zaglavlje nije prepoznato")
	}
	if len(pf.Rows) < 500 {
		t.Fatalf("rows = %d, očekivano više od 500", len(pf.Rows))
	}
	var vwv, heinz bool
	for _, r := range pf.Rows {
		if r.Address == "Am Kornacker 14" && r.Kunde == "VWV GmbH" {
			vwv = true
		}
		if r.Address == "Am Hang 10" && r.Kunde == "Heinz Schmidt" && r.Mobile != "" {
			heinz = true
		}
	}
	if !vwv || !heinz {
		t.Errorf("očekivani redovi nisu pronađeni (vwv=%v, heinz=%v)", vwv, heinz)
	}
}
