package main

import (
	"fmt"

	"github.com/jmoiron/sqlx"
)

// Profili provajdera: jedno mesto gde je opisano koji obrazac koji provajder
// traži — verzija, nazivi polja i šta je obavezno. Westnetz je popunjen
// (HaP 1.5.1 je u produkciji), Telekom i Verzatel su skeleti dok ne dobijemo
// njihove obrasce: tada se samo dopuni lista polja, bez migracije šeme.

type providerField struct {
	Key      string `json:"key"`   // univerzalno ime polja (kolona)
	Label    string `json:"label"` // naziv na obrascu
	Kind     string `json:"kind"`  // text|number|date|time|check|enum|photo|signature
	Required bool   `json:"required"`
}

type providerProfile struct {
	Key      string          `json:"key"`
	Label    string          `json:"label"`
	Template string          `json:"template"`
	Version  string          `json:"version"`
	Status   string          `json:"status"` // active|draft
	Fields   []providerField `json:"fields"`
}

// providerChoice je opcija u admin formi (projekat bira vrstu protokola).
type providerChoice struct {
	Value string
	Label string
}

var providerProfiles = []providerProfile{
	{
		Key:      "westnetz",
		Label:    "Westnetz",
		Template: "Hausanschlussprotokoll",
		Version:  "1.5.1",
		Status:   "active",
		Fields: []providerField{
			// 1. Objekt & Anschluss
			{"address", "Adresse", "text", true},
			{"city", "Ort", "text", false},
			{"bauabschnitt", "Bauabschnitt", "text", false},
			{"units", "Wohneinheiten (WE)", "number", false},
			{"nvt", "NVT", "text", false},
			// 2. Anschlussnehmer
			{"client_company", "Firma / Hausverwaltung", "text", false},
			{"client_last_name", "Nachname", "text", false},
			{"client_first_name", "Name", "text", false},
			{"client_phone", "Telefon", "text", false},
			{"client_mobile", "Mobil", "text", false},
			{"client_email", "E-Mail", "text", false},
			// 3. Auftrag & Team
			{"auftragnehmer", "Firma (Auftragnehmer)", "text", false},
			{"monteur", "Monteur", "text", false},
			{"pruefer", "Prüfer", "text", false},
			{"protocol_date", "Datum", "date", true},
			{"start_time", "Beginn", "time", false},
			{"project_number", "Projektnummer", "text", false},
			{"remarks", "Bemerkungen", "text", false},
			// 4. Technik
			{"gvs", "Zugehöriger GVS", "text", false},
			{"kassette_b", "Kasette B-Seite", "text", false},
			{"mux_number", "Splitter (MUX) Nummer", "text", false},
			{"mux_fibers", "Splitter (MUX) Fasern", "text", false},
			{"splices", "Spleiße (Stück)", "number", false},
			{"fiber_colors", "Genutzte Faserfarben", "enum", false},
			{"cable_charge", "GF Kabel Charge", "text", false},
			{"cable_length_m", "GF Kabel Länge (m)", "number", false},
			{"rohr", "Rohr (1–4)", "number", false},
			{"rohr_color", "Rohrfarbe", "enum", false},
			{"roehrchen_color", "Röhrchenfarbe", "enum", false},
			{"apl_codes", "APL-Codes", "text", false},
			// 5. Potvrde i prilozi
			{"check_fiber", "Durchgängigkeit geprüft", "check", false},
			{"check_complete", "Arbeiten abgeschlossen", "check", false},
			{"check_clean", "Arbeitsstelle sauber", "check", false},
			{"bild1", "Bild 1 — Hausanschluss gesamt", "photo", false},
			{"bild2", "Bild 2 — APL offen", "photo", false},
			{"bild3", "Bild 3 — APL geschlossen / Aufkleber", "photo", false},
			{"bild4", "Bild 4 — Foto ONT", "photo", false},
			{"light_test", "Licht test", "photo", false},
			// Potpisi se skupljaju na papiru (odluka za Westnetz).
			{"sig_monteur", "Unterschrift Monteur", "signature", false},
			{"sig_pruefer", "Unterschrift Prüfer", "signature", false},
			{"sig_kunde", "Unterschrift Kunde", "signature", false},
		},
	},
	{
		Key:      "telekom",
		Label:    "Telekom",
		Template: "Hausanschlussprotokoll",
		Version:  "",
		Status:   "draft",
		// TODO: dopuniti kada dobijemo Telekomov obrazac. Za sada je opisan
		// samo univerzalni deo koji sigurno postoji; sve specifično ide u
		// `protocols.extra` (jsonb) i ovde se doda sa Kind "extra".
		Fields: []providerField{
			{"address", "Adresse", "text", true},
			{"city", "Ort", "text", false},
			{"nvt", "NVT", "text", false},
			{"client_company", "Firma", "text", false},
			{"client_last_name", "Nachname", "text", false},
			{"client_first_name", "Vorname", "text", false},
			{"client_phone", "Telefon", "text", false},
			{"client_mobile", "Mobil", "text", false},
			{"client_email", "E-Mail", "text", false},
			{"protocol_date", "Datum", "date", true},
			{"monteur", "Monteur", "text", false},
			{"remarks", "Bemerkungen", "text", false},
			{"sig_kunde", "Unterschrift Kunde", "signature", false},
		},
	},
	{
		Key:      "verzatel",
		Label:    "Verzatel",
		Template: "Hausanschlussprotokoll",
		Version:  "",
		Status:   "draft",
		// Naziv provajdera i obrazac još nisu potvrđeni.
		Fields: []providerField{
			{"address", "Adresse", "text", true},
			{"city", "Ort", "text", false},
			{"nvt", "NVT", "text", false},
			{"protocol_date", "Datum", "date", true},
			{"monteur", "Monteur", "text", false},
			{"remarks", "Bemerkungen", "text", false},
		},
	},
}

// providerProfileFor vraća profil; nepoznat ključ pada na Westnetz jer je to
// jedini obrazac koji je u produkciji.
func providerProfileFor(key string) providerProfile {
	for _, p := range providerProfiles {
		if p.Key == key {
			return p
		}
	}
	return providerProfiles[0]
}

// providerChoices su opcije za admin formu (projekat bira vrstu protokola).
func providerChoices() []providerChoice {
	out := make([]providerChoice, 0, len(providerProfiles))
	for _, p := range providerProfiles {
		label := p.Label
		if p.Version != "" {
			label += " " + p.Version
		}
		if p.Status == "draft" {
			label += " (Entwurf)"
		}
		out = append(out, providerChoice{Value: p.Key, Label: label})
	}
	return out
}

func providerLabel(key string) string {
	p := providerProfileFor(key)
	label := p.Label
	if p.Version != "" {
		label += " " + p.Version
	}
	return label
}

func validProvider(key string) bool {
	for _, p := range providerProfiles {
		if p.Key == key {
			return true
		}
	}
	return false
}

// protocolTitle je naslov u zaglavlju HaP forme i PDF-a.
func protocolTitle(key string) string {
	p := providerProfileFor(key)
	title := p.Template + " · " + p.Label
	if p.Version != "" {
		title += " " + p.Version
	}
	return title
}

// ensureProviderColumns dodaje verziju šablona i `extra` (provajderska polja).
func ensureProviderColumns(db *sqlx.DB) error {
	stmts := []string{
		`ALTER TABLE ha_protocols ADD COLUMN IF NOT EXISTS template_version TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ha_protocols ADD COLUMN IF NOT EXISTS extra JSONB`,
		`ALTER TABLE protocols ADD COLUMN IF NOT EXISTS extra JSONB`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("provider schema: %w", err)
		}
	}
	return nil
}
