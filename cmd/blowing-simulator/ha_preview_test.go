package main

import (
	"database/sql"
	"os"
	"testing"
)

// TestPreviewPDF generiše PDF iz lokalnih uzoraka da se raspored može
// pregledati bez deploy-a. Pokreće se samo sa HAP_PREVIEW=1.
//
//	HAP_PREVIEW=1 go test ./cmd/blowing-simulator -run TestPreviewPDF -v
func TestPreviewPDF(t *testing.T) {
	if os.Getenv("HAP_PREVIEW") != "1" {
		t.Skip("set HAP_PREVIEW=1 to generate /tmp/hap_preview.pdf")
	}
	h := haRow{
		ID:              0,
		Auftragnehmer:   "Wolken ASM GmbH",
		Address:         "An der Burg 25",
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
		FiberColors:     "rot,blau,gelb",
		CheckFiber:      true,
		CheckComplete:   true,
		CheckClean:      true,
	}
	h.Units.Valid, h.Units.Int64 = true, 1
	h.Splices.Valid, h.Splices.Int64 = true, 1
	h.CableLength.Valid, h.CableLength.Float64 = true, 379

	images := map[string][]byte{}
	for kind, path := range map[string]string{
		"bild1":       "/tmp/haphotos/bild1.png",
		"bild2":       "/tmp/haphotos/bild2.png",
		"bild3":       "/tmp/haphotos/bild3.png",
		"bild4":       "/tmp/haphotos/bild4.png",
		"sig_monteur": "/tmp/haphotos/sig_monteur.jpg",
	} {
		if data, err := os.ReadFile(path); err == nil {
			images[kind] = data
		}
	}

	photos := []haPhotoRow{{
		Kind:     "bild1",
		Lat:      sql.NullFloat64{Float64: 51.36178, Valid: true},
		Lon:      sql.NullFloat64{Float64: 7.36842, Valid: true},
		Altitude: sql.NullFloat64{Float64: 276.6, Valid: true},
		TakenAt:  sql.NullString{String: "2026-07-21 12:13", Valid: true},
	}}

	pdf, err := buildHAPDF(h, photos, images)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("/tmp/hap_preview.pdf", pdf, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote /tmp/hap_preview.pdf (%d bytes)", len(pdf))
}
