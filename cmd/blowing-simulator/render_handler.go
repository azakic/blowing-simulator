package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"blowing-simulator/internal/simulator"
)

// renderFontPath je unutar image-a (COPY web ./web, WORKDIR /app).
const renderFontPath = "web/static/fonts/DejaVuSans.ttf"

// RenderProtocolHandler generiše PDF iz podataka u bazi.
//
// Dokument sadrži ista polja kao original (Firma blok, tabela merenja), pa ga
// isti parser može ponovo pročitati — to je round-trip provera
// (`protocolctl -verify`).
func RenderProtocolHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.URL.Query().Get("id"))
	if err != nil || id <= 0 {
		http.Error(w, "Invalid protocol ID", http.StatusBadRequest)
		return
	}

	var owner sql.NullInt64
	if err := db.QueryRow("SELECT user_id FROM protocols WHERE id = $1", id).Scan(&owner); err != nil {
		http.Error(w, "Protocol not found", http.StatusNotFound)
		return
	}
	claims := GetAuthClaims(r)
	if claims == nil || !hasProtocolAccess(db, claims.Role, claims.UserID, owner, id) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	loaded, err := LoadProtocol(db, id)
	if err != nil {
		http.Error(w, "Failed to load protocol: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var model simulator.RenderModel
	switch p := loaded.(type) {
	case *simulator.FremcoProtocol:
		model = simulator.RenderModelFromFremco(p)
	case *simulator.JettingProtocol:
		model = simulator.RenderModelFromJetting(p)
	default:
		http.Error(w, "Unsupported protocol type", http.StatusInternalServerError)
		return
	}

	font := renderFontPath
	if _, statErr := os.Stat(font); statErr != nil {
		font = "" // fallback na Helvetica (bez umlauta)
	}

	var buf bytes.Buffer
	if err := simulator.RenderProtocolPDF(&buf, model, font); err != nil {
		http.Error(w, "Failed to render PDF: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=protocol-%d.pdf", id))
	_, _ = w.Write(buf.Bytes())
}
