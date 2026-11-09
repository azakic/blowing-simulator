package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"

	"github.com/jung-kurt/gofpdf"
)

// fiberColor je jedno polje iz mreže boja vlakana (Röhrchenfarbe / genutzte
// Faserfarben). `Key` ide u bazu, `Hex` se koristi za ispunu u PDF-u.
type fiberColor struct {
	Key   string
	Label string
	Hex   [3]int
}

var haFiberColors = []fiberColor{
	{"rot", "rot", [3]int{226, 0, 26}},
	{"gruen", "grün", [3]int{0, 150, 64}},
	{"blau", "blau", [3]int{0, 80, 160}},
	{"gelb", "gelb", [3]int{255, 215, 0}},
	{"weiss", "weiß", [3]int{255, 255, 255}},
	{"grau", "grau", [3]int{158, 158, 158}},
	{"braun", "braun", [3]int{109, 76, 65}},
	{"violett", "violett", [3]int{123, 31, 162}},
	{"tuerkis", "türkis", [3]int{38, 198, 218}},
	{"schwarz", "schwarz", [3]int{0, 0, 0}},
	{"orange", "orange", [3]int{255, 109, 0}},
	{"rosa", "rosa", [3]int{248, 187, 208}},
}

// Geometrija A4 stranice (mm).
const (
	haPageW   = 210.0
	haTableW  = 190.0
	haMarginX = 10.0
)

// buildHAPDF generiše obrazac u rasporedu Westnetz obrasca 1.5.1.
func buildHAPDF(h haRow, photos []haPhotoRow, images map[string][]byte, onts []ontRow, ontImages map[int64][]byte) ([]byte, error) {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(haMarginX, 10, haMarginX)
	pdf.SetAutoPageBreak(false, 0)
	pdf.AddPage()
	pdf.SetLineWidth(0.25)

	// Core fontovi su cp1252; bez translatora "ü" postaje "Ã¼".
	tr := pdf.UnicodeTranslatorFromDescriptor("")

	x0 := haMarginX
	y := 20.0

	// --- zaglavlje ---
	pdf.SetFont("Helvetica", "B", 17)
	pdf.SetTextColor(0, 60, 47)
	pdf.SetXY(x0, 8)
	pdf.CellFormat(20, 8, tr("west"), "", 0, "L", false, 0, "")
	pdf.SetTextColor(141, 198, 63)
	pdf.CellFormat(18, 8, tr("netz"), "", 0, "L", false, 0, "")
	pdf.SetTextColor(0, 0, 0)
	pdf.SetFont("Helvetica", "B", 14)
	pdf.SetXY(x0, 8)
	pdf.CellFormat(haTableW, 8, tr("Hausanschlussprotokoll"), "", 0, "C", false, 0, "")

	// --- kolone ---
	colW := []float64{85, 45, 35, 25}
	colX := []float64{x0, x0 + colW[0], x0 + colW[0] + colW[1], x0 + colW[0] + colW[1] + colW[2]}

	headerRow := func(labels, values []string) {
		const labelH, valueH = 4.6, 6.4
		x := x0
		pdf.SetFont("Helvetica", "", 6.5)
		for i, l := range labels {
			pdf.SetXY(x+1, y+0.9)
			pdf.CellFormat(colW[i]-2, labelH-0.9, tr(l), "", 0, "L", false, 0, "")
			x += colW[i]
		}
		y += labelH
		x = x0
		pdf.SetFont("Helvetica", "B", 9)
		for i, v := range values {
			pdf.SetXY(x+1, y+0.5)
			pdf.CellFormat(colW[i]-2, valueH-0.8, tr(v), "", 0, "L", false, 0, "")
			x += colW[i]
		}
		y += valueH
		pdf.Line(x0, y, x0+haTableW, y)
	}

	units := ""
	if h.Units.Valid {
		units = fmt.Sprintf("%d WE", h.Units.Int64)
	}

	headerRow(
		[]string{"Firma", "Adresse", "Nachname", "Datum"},
		[]string{h.Auftragnehmer, h.Address, h.ClientLastName, formatHaDate(h.ProtocolDate)})
	headerRow(
		[]string{"Projektnummer", "Ort", "Name", "Monteur"},
		[]string{h.ProjectNumber, h.City, h.ClientFirstName, h.Monteur})
	headerRow(
		[]string{"", "Bauabschnitt", "Wohneinheiten", "Prüfer"},
		[]string{"", h.Bauabschnitt, units, h.Pruefer})

	for _, cx := range colX[1:] {
		pdf.Line(cx, 20, cx, y)
	}

	// --- tehnički blok ---
	blockTop := y
	const blockH = 30.0
	midX := x0 + 130.0
	pdf.Line(x0, blockTop+blockH, x0+haTableW, blockTop+blockH)
	pdf.Line(midX, blockTop, midX, blockTop+blockH)

	const leftLabelW = 52.0
	const rowH = 4.3
	ly := blockTop + 2.0

	leftRow := func(label, value string) {
		pdf.SetFont("Helvetica", "", 7.5)
		pdf.SetXY(x0, ly)
		pdf.CellFormat(leftLabelW, rowH, tr(label), "", 0, "R", false, 0, "")
		if value != "" {
			pdf.SetFont("Helvetica", "B", 8.5)
			pdf.SetXY(x0+leftLabelW+2, ly)
			pdf.CellFormat(midX-(x0+leftLabelW+2)-2, rowH, tr(value), "1", 0, "L", false, 0, "")
		}
		ly += rowH
	}

	sel := splitColors(h.FiberColors)
	leftRow("Zugehöriger GVS", h.GVS)
	pdf.SetFont("Helvetica", "", 7.5)
	pdf.SetXY(x0, ly)
	pdf.CellFormat(leftLabelW, rowH, tr("Rohr"), "", 0, "R", false, 0, "")
	drawColorSwatches(pdf, x0+leftLabelW+2, ly+0.35, midX-(x0+leftLabelW+2)-3, nil, 12)
	ly += rowH
	pdf.SetXY(x0, ly)
	pdf.CellFormat(leftLabelW, rowH, tr("Röhrchenfarbe"), "", 0, "R", false, 0, "")
	drawColorSwatches(pdf, x0+leftLabelW+2, ly+0.35, midX-(x0+leftLabelW+2)-3, nil, 12)
	ly += rowH
	pdf.SetXY(x0, ly)
	pdf.CellFormat(leftLabelW, rowH, tr("Mehrfarbig!->"), "", 0, "R", false, 0, "")
	drawColorSwatches(pdf, x0+leftLabelW+2, ly+0.35, midX-(x0+leftLabelW+2)-3, nil, 12)
	ly += rowH
	leftRow("Kasette B-Seite", h.KassetteB)
	leftRow("Splitter (MUX) Nummer", h.MuxNumber)

	const rightLabelW = 38.0
	rightX := midX + 2
	ry := blockTop + 2.0
	rightRow := func(label, value string) {
		pdf.SetFont("Helvetica", "", 7.5)
		pdf.SetXY(rightX, ry)
		pdf.CellFormat(rightLabelW, rowH, tr(label), "", 0, "R", false, 0, "")
		if value != "" {
			pdf.SetFont("Helvetica", "B", 8.5)
			pdf.SetXY(rightX+rightLabelW+1, ry)
			pdf.CellFormat(x0+haTableW-rightX-rightLabelW-2, rowH, tr(value), "1", 0, "L", false, 0, "")
		}
		ry += rowH
	}
	rightRow("Spleiße:", intOrEmpty(h.Splices)+" Stück")
	pdf.SetFont("Helvetica", "", 7.5)
	pdf.SetXY(rightX, ry)
	pdf.CellFormat(rightLabelW, rowH, tr("Genutzte Faserfarben:"), "", 0, "R", false, 0, "")
	swRows := drawColorSwatches(pdf, rightX+rightLabelW+1, ry+0.2, x0+haTableW-rightX-rightLabelW-2, sel, 6)
	ry += rowH
	if swRows > 1 {
		ry += float64(swRows-1) * 4.4
	}
	rightRow("GF Kabel Charge:", h.CableCharge)
	length := ""
	if h.CableLength.Valid {
		length = fmt.Sprintf("%.1f m", h.CableLength.Float64)
	}
	rightRow("GF Kabel Länge:", length)
	rightRow("Splitter (MUX) Fasern", h.MuxFibers)

	y = blockTop + blockH

	// --- fotografije 2×2 ---
	const capH, photoH = 4.6, 44.0
	halfW := haTableW / 2

	photoCell := func(kind, caption string, px, top float64) {
		pdf.SetFont("Helvetica", "", 7)
		pdf.SetXY(px+1, top+0.9)
		pdf.CellFormat(halfW-2, capH-1, tr(caption), "", 0, "L", false, 0, "")
		rx, ry, rw, rh := drawImageFit(pdf, "ph-"+kind, images[kind], px+2, top+capH+1, halfW-4, photoH-4)
		if p := findPhoto(photos, kind); p != nil {
			if c := photoStampText(p, h); c != "" {
				const barH = 4.2
				pdf.SetAlpha(0.6, "Normal")
				pdf.SetFillColor(0, 0, 0)
				pdf.Rect(rx, ry+rh-barH, rw, barH, "F")
				pdf.SetAlpha(1, "Normal")
				pdf.SetFillColor(255, 255, 255)
				pdf.SetTextColor(255, 255, 255)
				pdf.SetFont("Helvetica", "", 6.3)
				pdf.SetXY(rx+1.5, ry+rh-barH+0.8)
				pdf.CellFormat(rw-3, 3, tr(c), "", 0, "L", false, 0, "")
				pdf.SetTextColor(0, 0, 0)
			}
		}
	}

	photoBlock := func(lk, lc, rk, rc string) {
		top := y
		photoCell(lk, lc, x0, top)
		photoCell(rk, rc, x0+halfW, top)
		y = top + capH + photoH
		pdf.Line(x0, y, x0+haTableW, y)
		pdf.Line(x0+halfW, top, x0+halfW, y)
	}
	photoBlock("bild1", "Bild 1      Hausanschluss gesamt", "bild2", "Bild 2      APL offen")
	photoBlock("bild3", "Bild 3      APL geschlossen/Foto mit Aufkleber", "bild4", "Bild 4      Foto ONT")

	// --- potvrdna polja + ONT nalepnica ---
	const checkH = 22.0
	checkTop := y
	pdf.Line(x0, checkTop+checkH, x0+haTableW, checkTop+checkH)
	pdf.Line(x0+halfW, checkTop, x0+halfW, checkTop+checkH)

	checks := []struct {
		on   bool
		text string
	}{
		{h.CheckFiber, "Die Durchgängigkeit der Faser wurde überprüft"},
		{h.CheckComplete, "Die Arbeiten wurden vollständig abgeschlossen"},
		{h.CheckClean, "Die Arbeitstelle wurde ordentlich und sauber hinterlassen"},
	}
	cy := checkTop + 3.5
	for _, c := range checks {
		pdf.Rect(x0+12, cy, 4, 4, "D")
		if c.on {
			pdf.SetFont("Helvetica", "B", 10)
			pdf.SetXY(x0+12.3, cy-1.0)
			pdf.CellFormat(4, 5, tr("X"), "", 0, "C", false, 0, "")
		}
		pdf.SetFont("Helvetica", "", 7.5)
		pdf.SetXY(x0+20, cy+0.3)
		pdf.CellFormat(halfW-22, 4, tr(c.text), "", 0, "L", false, 0, "")
		cy += 5.6
	}

	pdf.SetFont("Helvetica", "", 7)
	pdf.SetXY(x0+halfW+2, checkTop+1.2)
	pdf.CellFormat(halfW-4, 4, tr("ONT Aufkleber:"), "", 0, "L", false, 0, "")
	// Prva ONT nalepnica; ako je nema, koristi se stari fiksni slot.
	ontImg := images["ont_label"]
	for _, o := range onts {
		if d := ontImages[o.ID]; len(d) > 0 {
			ontImg = d
			break
		}
	}
	drawImageFit(pdf, "ont", ontImg, x0+halfW+2, checkTop+5.5, halfW-4, checkH-7)

	y = checkTop + checkH

	// --- potpisi + APL nalepnica + Licht test ---
	const sigH = 26.0
	sigTop := y
	pdf.Line(x0, sigTop+sigH, x0+haTableW, sigTop+sigH)
	pdf.Line(x0+halfW, sigTop, x0+halfW, sigTop+sigH)

	// Bez digitalnih potpisa: donji blok koriste APL nalepnica (levo) i
	// Licht test (desno), sa većim slikama.
	pdf.SetFont("Helvetica", "", 7)
	pdf.SetXY(x0+2, sigTop+1.2)
	pdf.CellFormat(40, 4, tr("APL-Codes:"), "", 0, "L", false, 0, "")
	aplCodes := parseAPLCodesJSON(h.APLCodes)
	if len(aplCodes) == 0 {
		aplCodes = []string{"—"}
	}
	pdf.SetFont("Helvetica", "", 7.5)
	pdf.SetXY(x0+2, sigTop+5.5)
	pdf.MultiCell(halfW-4, 4, tr(strings.Join(aplCodes, "\n")), "", "L", false)

	pdf.SetXY(x0+halfW+2, sigTop+1.2)
	pdf.CellFormat(30, 4, tr("Licht test"), "", 0, "L", false, 0, "")
	drawImageFit(pdf, "light", images["light_test"], x0+halfW+2, sigTop+5.5, halfW-4, sigH-8)

	// Spoljni okvir tabele — od zaglavlja do kraja bloka potpisa.
	pdf.Rect(x0, 20, haTableW, (sigTop+sigH)-20, "D")

	// --- footer ---
	footTop := 282.0
	pdf.Line(x0, footTop, x0+haTableW, footTop)
	footerRow := func(label string, lx, lw float64, value string, vx, vw float64) {
		pdf.SetFont("Helvetica", "", 6.5)
		pdf.SetXY(lx, footTop+1)
		pdf.CellFormat(lw, 4, tr(label), "", 0, "L", false, 0, "")
		pdf.SetFont("Helvetica", "B", 6.5)
		pdf.SetXY(vx, footTop+1)
		pdf.CellFormat(vw, 4, tr(value), "", 0, "L", false, 0, "")
	}
	footerRow("Dateiname:", x0+1, 20, "Hausanschlussprotokoll", x0+22, 70)
	footerRow("", x0+150, 14, "Seite 1 von 1", x0+164, 26)
	footTop += 5
	footerRow("Ersteller:", x0+1, 20, "Louis Vieting", x0+22, 70)
	footerRow("Version:", x0+150, 14, "1.5.1", x0+164, 26)
	footTop += 5
	footerRow("Freigabe:", x0+1, 20, "Roland Wehn", x0+22, 70)
	footerRow("Stand:", x0+150, 14, "22.05.2023", x0+164, 26)

	// --- aneks: svi ONT uređaji (SN + nalepnica) ---
	if len(onts) > 0 {
		pdf.AddPage()
		header := func() {
			pdf.SetFont("Helvetica", "B", 13)
			pdf.CellFormat(0, 8, tr("ONT-Geräte / Anhang"), "", 1, "L", false, 0, "")
			pdf.SetFont("Helvetica", "", 8)
			pdf.CellFormat(0, 4, tr(fmt.Sprintf("%s · %s", h.Address, h.ProtocolDate)), "", 1, "L", false, 0, "")
			pdf.Ln(2)
			pdf.SetFont("Helvetica", "B", 9)
			pdf.SetFillColor(235, 235, 235)
			pdf.CellFormat(10, 6, "#", "1", 0, "C", true, 0, "")
			pdf.CellFormat(75, 6, tr("Seriennummer (SN)"), "1", 0, "L", true, 0, "")
			pdf.CellFormat(105, 6, tr("Foto der Nalepnica"), "1", 1, "L", true, 0, "")
			pdf.SetFillColor(255, 255, 255)
		}
		header()
		for i, o := range onts {
			const rowH = 32.0
			if pdf.GetY()+rowH > 280 {
				pdf.AddPage()
				header()
			}
			y := pdf.GetY()
			pdf.SetFont("Helvetica", "", 9)
			pdf.CellFormat(10, rowH, fmt.Sprintf("%d", i+1), "1", 0, "C", false, 0, "")
			xSN := pdf.GetX()
			pdf.CellFormat(75, rowH, "", "1", 0, "L", false, 0, "")
			pdf.SetFont("Helvetica", "", 7.5)
			pdf.SetXY(xSN+1.5, y+2)
			pdf.MultiCell(72, 4, tr(strings.Join(ontAnnexLines(o), "\n")), "", "L", false)
			pdf.SetXY(xSN+75, y)
			x := pdf.GetX()
			pdf.CellFormat(105, rowH, "", "1", 1, "L", false, 0, "")
			if data := ontImages[o.ID]; len(data) > 0 {
				drawImageFit(pdf, fmt.Sprintf("ont-annex-%d", o.ID), data, x+2, y+2, 101, rowH-4)
			}
		}
	}

	// --- aneks: APL kodovi pročitani iz Bild 3 ---
	if len(aplCodes) > 0 {
		pdf.AddPage()
		aplHeader := func() {
			pdf.SetFont("Helvetica", "B", 13)
			pdf.CellFormat(0, 8, tr("APL-Codes / Anhang"), "", 1, "L", false, 0, "")
			pdf.SetFont("Helvetica", "", 8)
			pdf.CellFormat(0, 4, tr(fmt.Sprintf("%s · %s", h.Address, h.ProtocolDate)), "", 1, "L", false, 0, "")
			pdf.CellFormat(0, 4, tr("Gelesen aus Bild 3 — APL geschlossen / Aufkleber"), "", 1, "L", false, 0, "")
			pdf.Ln(2)
			pdf.SetFont("Helvetica", "B", 9)
			pdf.SetFillColor(235, 235, 235)
			pdf.CellFormat(12, 6, "#", "1", 0, "C", true, 0, "")
			pdf.CellFormat(178, 6, tr("APL-Code"), "1", 1, "L", true, 0, "")
			pdf.SetFillColor(255, 255, 255)
		}
		aplHeader()
		for i, code := range aplCodes {
			if pdf.GetY()+8 > 280 {
				pdf.AddPage()
				aplHeader()
			}
			pdf.SetFont("Helvetica", "", 9)
			pdf.CellFormat(12, 8, fmt.Sprintf("%d", i+1), "1", 0, "C", false, 0, "")
			pdf.CellFormat(178, 8, tr(code), "1", 1, "L", false, 0, "")
		}
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func findPhoto(photos []haPhotoRow, kind string) *haPhotoRow {
	for i := range photos {
		if photos[i].Kind == kind {
			return &photos[i]
		}
	}
	return nil
}

// photoStampText vraća tekst koji se ispisuje preko fotografije u PDF-u:
// GPS + vreme ako postoje, inače adresa gradilišta.
func photoStampText(p *haPhotoRow, h haRow) string {
	var parts []string
	if p.Lat.Valid && p.Lon.Valid {
		parts = append(parts, fmt.Sprintf("%.6f, %.6f", p.Lat.Float64, p.Lon.Float64))
		if p.Altitude.Valid && p.Altitude.Float64 != 0 {
			parts = append(parts, fmt.Sprintf("%.0f m", p.Altitude.Float64))
		}
	}
	if p.TakenAt.Valid && p.TakenAt.String != "" {
		parts = append(parts, p.TakenAt.String)
	}
	if len(parts) == 0 {
		addr := strings.TrimSpace(h.Address)
		if h.City != "" {
			if addr != "" {
				addr += ", "
			}
			addr += h.City
		}
		if addr != "" {
			parts = append(parts, addr)
		}
	}
	return strings.Join(parts, " · ")
}

func photoCaption(p *haPhotoRow) string {
	var parts []string
	if p.TakenAt.Valid && p.TakenAt.String != "" {
		parts = append(parts, p.TakenAt.String)
	}
	if p.Lat.Valid && p.Lon.Valid {
		parts = append(parts, fmt.Sprintf("%.5f, %.5f", p.Lat.Float64, p.Lon.Float64))
		if p.Altitude.Valid && p.Altitude.Float64 != 0 {
			parts = append(parts, fmt.Sprintf("%.0f m", p.Altitude.Float64))
		}
	}
	return strings.Join(parts, " · ")
}

// drawImageFit ubacuje sliku u okvir čuvajući odnos stranica.
func drawImageFit(pdf *gofpdf.Fpdf, name string, data []byte, x, y, w, h float64) (rx, ry, rw, rh float64) {
	if len(data) == 0 {
		pdf.SetDrawColor(205, 205, 205)
		pdf.Rect(x, y, w, h, "D")
		pdf.SetDrawColor(0, 0, 0)
		return x, y, w, h
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width == 0 || cfg.Height == 0 {
		return x, y, w, h
	}
	imgType := "JPG"
	if format == "png" {
		imgType = "PNG"
	}
	opts := gofpdf.ImageOptions{ImageType: imgType}
	info := pdf.RegisterImageOptionsReader(name, opts, bytes.NewReader(data))
	if info == nil {
		return x, y, w, h
	}
	ratio := float64(cfg.Width) / float64(cfg.Height)
	iw, ih := w, w/ratio
	if ih > h {
		ih, iw = h, h*ratio
	}
	rx = x + (w-iw)/2
	ry = y + (h-ih)/2
	rw, rh = iw, ih
	pdf.ImageOptions(name, rx, ry, rw, rh, false, opts, 0, "")
	return
}

func drawColorSwatches(pdf *gofpdf.Fpdf, x, y, maxW float64, selected []string, perRow int) int {
	sel := map[string]bool{}
	for _, s := range selected {
		sel[strings.TrimSpace(s)] = true
	}
	const sz, gap = 3.6, 1.2
	if perRow <= 0 {
		perRow = 6
	}
	rows := 0
	for i, c := range haFiberColors {
		col, row := i%perRow, i/perRow
		cx := x + float64(col)*(sz+gap)
		cy := y + float64(row)*(sz+0.8)
		if cx+sz > x+maxW {
			continue
		}
		if sel[c.Key] {
			pdf.SetFillColor(c.Hex[0], c.Hex[1], c.Hex[2])
		} else {
			pdf.SetFillColor(255, 255, 255)
		}
		pdf.Rect(cx, cy, sz, sz, "FD")
		if row+1 > rows {
			rows = row + 1
		}
	}
	pdf.SetDrawColor(0, 0, 0)
	return rows
}

func splitColors(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func intOrEmpty(v sql.NullInt64) string {
	if !v.Valid {
		return ""
	}
	return fmt.Sprintf("%d", v.Int64)
}

func formatHaDate(d string) string {
	if len(d) == 10 && d[4] == '-' {
		return d[8:10] + "." + d[5:7] + "." + d[0:4]
	}
	return d
}
