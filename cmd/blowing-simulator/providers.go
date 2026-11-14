package main

import (
	"fmt"

	"github.com/jmoiron/sqlx"
)

// Profili provajdera: jedno mesto gde je opisan obrazac koji provajder traži
// — verzija, polja (sa grupama i opcijama) i šta je obavezno. Westnetz je
// aktivan; Telekom ima specifikaciju po ZTV TK-Netz 39A/40 (čeka zvanični
// obrazac za verziju/layout); Verzatel je skeleton.

type providerField struct {
	Key      string   `json:"key"`             // univerzalno ime (kolona ili extra.<key>)
	Label    string   `json:"label"`           // naziv na obrascu (provajderov)
	Kind     string   `json:"kind"`            // text|number|date|time|check|enum|photo|signature
	Group    string   `json:"group,omitempty"` // sekcija obrasca
	Required bool     `json:"required,omitempty"`
	Extra    bool     `json:"extra,omitempty"`   // nema univerzalnu kolonu → jsonb `extra`
	Options  []string `json:"options,omitempty"` // za enum
	Hint     string   `json:"hint,omitempty"`
}

type providerProfile struct {
	Key      string          `json:"key"`
	Label    string          `json:"label"`
	Template string          `json:"template"`
	Version  string          `json:"version"`
	Status   string          `json:"status"` // active|draft
	Note     string          `json:"note,omitempty"`
	Fields   []providerField `json:"fields"`
}

// providerChoice je opcija u admin formi (projekat bira vrstu protokola).
type providerChoice struct {
	Value string
	Label string
}

const (
	groupObjekt  = "Objekt & Anschluss"
	groupKunde   = "Anschlussnehmer"
	groupAuftrag = "Auftrag & Team"
	groupTechnik = "Technik"
	groupChecks  = "Potvrde i prilozi"
	groupSign    = "Potpisi"
)

var providerProfiles = []providerProfile{
	{
		Key:      "westnetz",
		Label:    "Westnetz",
		Template: "Hausanschlussprotokoll",
		Version:  "1.5.1",
		Status:   "active",
		Note:     "HaP po obrascu Westnetz 1.5.1 (u produkciji).",
		Fields: []providerField{
			// 1. Objekt & Anschluss
			{Key: "address", Label: "Adresse", Kind: "text", Group: groupObjekt, Required: true},
			{Key: "city", Label: "Ort", Kind: "text", Group: groupObjekt},
			{Key: "bauabschnitt", Label: "Bauabschnitt", Kind: "text", Group: groupObjekt},
			{Key: "units", Label: "Wohneinheiten (WE)", Kind: "number", Group: groupObjekt},
			{Key: "nvt", Label: "NVT", Kind: "text", Group: groupObjekt},
			// 2. Anschlussnehmer
			{Key: "client_company", Label: "Firma / Hausverwaltung", Kind: "text", Group: groupKunde},
			{Key: "client_last_name", Label: "Nachname", Kind: "text", Group: groupKunde},
			{Key: "client_first_name", Label: "Name", Kind: "text", Group: groupKunde},
			{Key: "client_phone", Label: "Telefon", Kind: "text", Group: groupKunde},
			{Key: "client_mobile", Label: "Mobil", Kind: "text", Group: groupKunde},
			{Key: "client_email", Label: "E-Mail", Kind: "text", Group: groupKunde},
			// 3. Auftrag & Team
			{Key: "auftragnehmer", Label: "Firma (Auftragnehmer)", Kind: "text", Group: groupAuftrag},
			{Key: "monteur", Label: "Monteur", Kind: "text", Group: groupAuftrag},
			{Key: "pruefer", Label: "Prüfer", Kind: "text", Group: groupAuftrag},
			{Key: "protocol_date", Label: "Datum", Kind: "date", Group: groupAuftrag, Required: true},
			{Key: "start_time", Label: "Beginn", Kind: "time", Group: groupAuftrag},
			{Key: "project_number", Label: "Projektnummer", Kind: "text", Group: groupAuftrag},
			{Key: "remarks", Label: "Bemerkungen", Kind: "text", Group: groupAuftrag},
			// 4. Technik
			{Key: "gvs", Label: "Zugehöriger GVS", Kind: "text", Group: groupTechnik},
			{Key: "kassette_b", Label: "Kasette B-Seite", Kind: "text", Group: groupTechnik},
			{Key: "mux_number", Label: "Splitter (MUX) Nummer", Kind: "text", Group: groupTechnik},
			{Key: "mux_fibers", Label: "Splitter (MUX) Fasern", Kind: "text", Group: groupTechnik},
			{Key: "splices", Label: "Spleiße (Stück)", Kind: "number", Group: groupTechnik},
			{Key: "fiber_colors", Label: "Genutzte Faserfarben", Kind: "enum", Group: groupTechnik},
			{Key: "cable_charge", Label: "GF Kabel Charge", Kind: "text", Group: groupTechnik},
			{Key: "cable_length_m", Label: "GF Kabel Länge (m)", Kind: "number", Group: groupTechnik},
			{Key: "rohr", Label: "Rohr (1–4)", Kind: "number", Group: groupTechnik},
			{Key: "rohr_color", Label: "Rohrfarbe", Kind: "enum", Group: groupTechnik},
			{Key: "roehrchen_color", Label: "Röhrchenfarbe", Kind: "enum", Group: groupTechnik},
			{Key: "apl_codes", Label: "APL-Codes", Kind: "text", Group: groupTechnik},
			// 5. Potvrde i prilozi
			{Key: "check_fiber", Label: "Durchgängigkeit geprüft", Kind: "check", Group: groupChecks},
			{Key: "check_complete", Label: "Arbeiten abgeschlossen", Kind: "check", Group: groupChecks},
			{Key: "check_clean", Label: "Arbeitsstelle sauber", Kind: "check", Group: groupChecks},
			{Key: "bild1", Label: "Bild 1 — Hausanschluss gesamt", Kind: "photo", Group: groupChecks},
			{Key: "bild2", Label: "Bild 2 — APL offen", Kind: "photo", Group: groupChecks},
			{Key: "bild3", Label: "Bild 3 — APL geschlossen / Aufkleber", Kind: "photo", Group: groupChecks},
			{Key: "bild4", Label: "Bild 4 — Foto ONT", Kind: "photo", Group: groupChecks},
			{Key: "light_test", Label: "Licht test", Kind: "photo", Group: groupChecks},
			// Potpisi se skupljaju na papiru (odluka za Westnetz).
			{Key: "sig_monteur", Label: "Unterschrift Monteur", Kind: "signature", Group: groupSign},
			{Key: "sig_pruefer", Label: "Unterschrift Prüfer", Kind: "signature", Group: groupSign},
			{Key: "sig_kunde", Label: "Unterschrift Kunde", Kind: "signature", Group: groupSign},
		},
	},
	{
		Key:      "telekom",
		Label:    "Telekom",
		Template: "Gf-Hausanschlussprotokoll",
		Version:  "",
		Status:   "draft",
		Note:     "Specifikacija po ZTV TK-Netz 39A/40; čeka zvanični obrazac (verzija i layout).",
		Fields: []providerField{
			// 1. Auftrags- & Objektdaten
			{Key: "nvt", Label: "NVT / KVz-Nr.", Kind: "text", Group: "Auftrags- & Objektdaten", Required: true, Hint: "npr. N-1.31"},
			{Key: "kls_id", Label: "KLS-ID / Home-ID / Folien-ID", Kind: "text", Group: "Auftrags- & Objektdaten", Required: true, Extra: true},
			{Key: "units", Label: "Wohneinheiten (WE)", Kind: "number", Group: "Auftrags- & Objektdaten", Hint: "EFH = 1, MFH = 2+"},
			{Key: "address", Label: "Adresse (Straße und Hausnummer)", Kind: "text", Group: "Auftrags- & Objektdaten", Required: true},
			{Key: "postcode", Label: "PLZ", Kind: "text", Group: "Auftrags- & Objektdaten"},
			{Key: "city", Label: "Ort", Kind: "text", Group: "Auftrags- & Objektdaten"},
			{Key: "client_last_name", Label: "Eigentümer / Ansprechpartner (Name)", Kind: "text", Group: "Auftrags- & Objektdaten", Required: true},
			{Key: "client_first_name", Label: "Vorname", Kind: "text", Group: "Auftrags- & Objektdaten"},
			{Key: "client_company", Label: "Firma / Hausverwaltung", Kind: "text", Group: "Auftrags- & Objektdaten"},
			{Key: "client_phone", Label: "Telefon", Kind: "text", Group: "Auftrags- & Objektdaten"},
			{Key: "client_email", Label: "E-Mail", Kind: "text", Group: "Auftrags- & Objektdaten"},
			{Key: "auftraggeber", Label: "Auftraggeber / GU", Kind: "text", Group: "Auftrags- & Objektdaten", Extra: true, Hint: "Deutsche Telekom Technik GmbH"},
			{Key: "auftragnehmer", Label: "Auftragnehmer (ausführende Firma)", Kind: "text", Group: "Auftrags- & Objektdaten", Hint: "Wolken-ASM GmbH"},

			// 2. Tiefbau & Hauseinführung
			{Key: "trench_length_m", Label: "Trassenlänge auf Privatgrund (m)", Kind: "number", Group: "Tiefbau & Hauseinführung", Extra: true},
			{Key: "construction_method", Label: "Bauweise", Kind: "enum", Group: "Tiefbau & Hauseinführung", Extra: true,
				Options: []string{"Offener Graben", "Spülbohrung / Erdrakete", "Leerrohr Bestand genutzt"}},
			{Key: "surface_type", Label: "Oberfläche", Kind: "enum", Group: "Tiefbau & Hauseinführung", Extra: true,
				Options: []string{"Rasen / Erde", "Pflaster", "Asphalt", "Platten"}},
			{Key: "laying_depth_m", Label: "Verlegetiefe (m)", Kind: "number", Group: "Tiefbau & Hauseinführung", Extra: true, Hint: "Standard 0,60 m"},
			{Key: "warning_tape", Label: "Trassenwarnband verlegt", Kind: "check", Group: "Tiefbau & Hauseinführung", Extra: true},
			{Key: "house_entry_type", Label: "Hauseinführung", Kind: "enum", Group: "Tiefbau & Hauseinführung", Extra: true,
				Options: []string{"Einsparten-Hauseinführung (EHE)", "Mehrsparten-Hauseinführung (MSH)", "Kernbohrung mit Ringraumdichtung"}},
			{Key: "house_entry_sealed", Label: "Dichtheitsprüfung / Abdichtung", Kind: "check", Group: "Tiefbau & Hauseinführung", Extra: true},

			// 3. Inneninstallation (Gf-AP / Gf-TA)
			{Key: "gfap_location", Label: "Montageort Gf-AP", Kind: "enum", Group: "Inneninstallation (Gf-AP / Gf-TA)", Extra: true,
				Options: []string{"Keller", "HWR / Technikraum", "Flur"}},
			{Key: "gfap_sn", Label: "Gf-AP Seriennummer / QR-Code", Kind: "text", Group: "Inneninstallation (Gf-AP / Gf-TA)", Extra: true},
			{Key: "cable_route", Label: "Leitungsweg im Haus", Kind: "enum", Group: "Inneninstallation (Gf-AP / Gf-TA)", Extra: true,
				Options: []string{"Leerrohr Bestand", "Aufputz im Kabelkanal"}},
			{Key: "cable_route_length_m", Label: "Kabelkanal-Länge (m)", Kind: "number", Group: "Inneninstallation (Gf-AP / Gf-TA)", Extra: true},
			{Key: "fireproofing", Label: "Brandschottung", Kind: "enum", Group: "Inneninstallation (Gf-AP / Gf-TA)", Extra: true,
				Options: []string{"nicht erforderlich", "erforderlich — ausgeführt", "erforderlich — offen"}},
			{Key: "gfta_installed", Label: "Gf-TA montiert", Kind: "check", Group: "Inneninstallation (Gf-AP / Gf-TA)", Extra: true},
			{Key: "gfta_sn", Label: "Gf-TA Seriennummer / Dose-ID", Kind: "text", Group: "Inneninstallation (Gf-AP / Gf-TA)", Extra: true, Required: true},
			{Key: "microduct_color", Label: "Mikrorohr-Farbe", Kind: "text", Group: "Inneninstallation (Gf-AP / Gf-TA)", Extra: true, Hint: "npr. rot / grün"},
			{Key: "fibre_no", Label: "Faser-Nr.", Kind: "number", Group: "Inneninstallation (Gf-AP / Gf-TA)", Extra: true},

			// 4. Prüfungen & Messwerte
			{Key: "blowing_protocol", Label: "Einblasprotokoll liegt vor", Kind: "check", Group: "Prüfungen & Messwerte", Extra: true},
			{Key: "attenuation_1310_db", Label: "Dämpfung 1310 nm (dB)", Kind: "number", Group: "Prüfungen & Messwerte", Extra: true},
			{Key: "attenuation_1550_db", Label: "Dämpfung 1550 nm (dB)", Kind: "number", Group: "Prüfungen & Messwerte", Extra: true},
			{Key: "attenuation_1625_db", Label: "Dämpfung 1625 nm (dB)", Kind: "number", Group: "Prüfungen & Messwerte", Extra: true},
			{Key: "attenuation_pass", Label: "Messung PASS (< 0,5 dB)", Kind: "check", Group: "Prüfungen & Messwerte", Extra: true},
			{Key: "surface_restored", Label: "Oberfläche wiederhergestellt", Kind: "check", Group: "Prüfungen & Messwerte", Extra: true},

			// 5. Fotodokumentation
			{Key: "foto_graben", Label: "Offener Graben mit Messlatte + Warnband", Kind: "photo", Group: "Fotodokumentation"},
			{Key: "foto_hauseinfuehrung", Label: "Hauseinführung vor Verfüllung", Kind: "photo", Group: "Fotodokumentation"},
			{Key: "foto_ringraum", Label: "Ringraumdichtung (Innenseite)", Kind: "photo", Group: "Fotodokumentation"},
			{Key: "foto_gfap_offen", Label: "Gf-AP offen (SN/QR lesbar)", Kind: "photo", Group: "Fotodokumentation"},
			{Key: "foto_gfap_geschlossen", Label: "Gf-AP geschlossen", Kind: "photo", Group: "Fotodokumentation"},
			{Key: "foto_oberflaeche", Label: "Sanierte Oberfläche", Kind: "photo", Group: "Fotodokumentation"},

			// 6. Bemerkungen & Unterschriften
			{Key: "remarks", Label: "Besonderheiten / Abweichungen", Kind: "text", Group: "Bemerkungen & Unterschriften"},
			{Key: "protocol_date", Label: "Datum", Kind: "date", Group: "Bemerkungen & Unterschriften", Required: true},
			{Key: "start_time", Label: "Uhrzeit (Fertigstellung)", Kind: "time", Group: "Bemerkungen & Unterschriften"},
			{Key: "sig_monteur", Label: "Unterschrift Techniker / Bauleiter", Kind: "signature", Group: "Bemerkungen & Unterschriften", Required: true},
			{Key: "sig_kunde", Label: "Unterschrift Eigentümer / Kunde", Kind: "signature", Group: "Bemerkungen & Unterschriften", Required: true},
		},
	},
	{
		Key:      "verzatel",
		Label:    "Verzatel",
		Template: "Hausanschlussprotokoll",
		Version:  "",
		Status:   "draft",
		Note:     "Naziv provajdera i obrazac još nisu potvrđeni.",
		Fields: []providerField{
			{Key: "address", Label: "Adresse", Kind: "text", Group: groupObjekt, Required: true},
			{Key: "city", Label: "Ort", Kind: "text", Group: groupObjekt},
			{Key: "nvt", Label: "NVT", Kind: "text", Group: groupObjekt},
			{Key: "protocol_date", Label: "Datum", Kind: "date", Group: groupAuftrag, Required: true},
			{Key: "monteur", Label: "Monteur", Kind: "text", Group: groupAuftrag},
			{Key: "remarks", Label: "Bemerkungen", Kind: "text", Group: groupAuftrag},
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
	if p.Status == "draft" {
		title += " (Entwurf)"
	}
	return title
}

// providerFieldKeys vraća ključeve polja (za proveru u testovima i budući
// render forme).
func providerFieldKeys(key string) map[string]providerField {
	out := map[string]providerField{}
	for _, f := range providerProfileFor(key).Fields {
		out[f.Key] = f
	}
	return out
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

// backfillProviderVersions upisuje verziju obrasca u HaP-ove koji je nemaju
// (izdate pre uvođenja kolone). Idempotentno.
func backfillProviderVersions(db *sqlx.DB) error {
	for _, p := range providerProfiles {
		if p.Version == "" {
			continue
		}
		if _, err := db.Exec(`UPDATE ha_protocols SET template_version=$1
			WHERE coalesce(template_version,'') = '' AND provider=$2`, p.Version, p.Key); err != nil {
			return err
		}
	}
	// Stariji redovi bez provajdera su Westnetz (jedini obrazac do sada).
	westnetz := providerProfileFor("westnetz")
	_, err := db.Exec(`UPDATE ha_protocols SET template_version=$1, provider='westnetz'
		WHERE coalesce(template_version,'') = '' AND coalesce(provider,'') = ''`, westnetz.Version)
	return err
}
