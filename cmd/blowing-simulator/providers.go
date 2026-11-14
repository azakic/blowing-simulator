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
	Kind     string   `json:"kind"`            // text|textarea|number|date|time|check|enum|photo|signature
	Group    string   `json:"group,omitempty"` // sekcija obrasca
	Required bool     `json:"required,omitempty"`
	Extra    bool     `json:"extra,omitempty"`   // nema univerzalnu kolonu → jsonb `extra`
	Options  []string `json:"options,omitempty"` // za enum
	Default  string   `json:"default,omitempty"`
	Hint     string   `json:"hint,omitempty"`
}

type providerProfile struct {
	Key           string          `json:"key"`
	Label         string          `json:"label"`
	Template      string          `json:"template"`
	DocumentTitle string          `json:"document_title,omitempty"`
	Version       string          `json:"version"`
	Status        string          `json:"status"` // active|draft
	Note          string          `json:"note,omitempty"`
	Fields        []providerField `json:"fields"`
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
		Key:           "telekom",
		Label:         "Deutsche Telekom Technik GmbH",
		Template:      "Gf-Hausanschlussprotokoll",
		DocumentTitle: "Begehungs- und Übergabeprotokoll Glasfaser-Hausanschluss (HA)",
		Version:       "telekom-ha-v1",
		Status:        "draft",
		Note:          "Specifikacija po ZTV TK-Netz 39A/40; čeka zvanični obrazac (layout i konačna verzija).",
		Fields: []providerField{
			// 1. Auftrags- & Objektdaten
			{Key: "nvt", Label: "NVT / KVz-Nr.", Kind: "text", Group: "1. Auftrags- & Objektdaten", Required: true, Hint: "z.B. N-1.31"},
			{Key: "kls_id", Label: "KLS-ID / Home-ID / Folien-ID", Kind: "text", Group: "1. Auftrags- & Objektdaten", Required: true, Extra: true},
			{Key: "units", Label: "Wohneinheiten (WE)", Kind: "number", Group: "1. Auftrags- & Objektdaten", Default: "1", Hint: "EFH = 1, MFH = 2+"},
			{Key: "address", Label: "Straße / Hausnummer", Kind: "text", Group: "1. Auftrags- & Objektdaten", Required: true},
			{Key: "postcode", Label: "PLZ", Kind: "text", Group: "1. Auftrags- & Objektdaten", Required: true},
			{Key: "city", Label: "Ort", Kind: "text", Group: "1. Auftrags- & Objektdaten", Required: true},
			{Key: "client_last_name", Label: "Eigentümer / Ansprechpartner", Kind: "text", Group: "1. Auftrags- & Objektdaten", Required: true},
			{Key: "client_first_name", Label: "Vorname", Kind: "text", Group: "1. Auftrags- & Objektdaten"},
			{Key: "client_company", Label: "Firma / Hausverwaltung", Kind: "text", Group: "1. Auftrags- & Objektdaten"},
			{Key: "client_phone", Label: "Telefon", Kind: "text", Group: "1. Auftrags- & Objektdaten"},
			{Key: "client_email", Label: "E-Mail", Kind: "text", Group: "1. Auftrags- & Objektdaten"},
			{Key: "auftraggeber", Label: "Auftraggeber / GU", Kind: "text", Group: "1. Auftrags- & Objektdaten", Extra: true, Default: "Deutsche Telekom Technik GmbH"},
			{Key: "auftragnehmer", Label: "Auftragnehmer (ausführende Firma)", Kind: "text", Group: "1. Auftrags- & Objektdaten", Hint: "Wolken-ASM GmbH"},

			// 2. Tiefbau & Hauseinführung
			{Key: "trassenlaenge_m", Label: "Trassenlänge auf Privatgrund (m)", Kind: "number", Group: "2. Tiefbau & Hauseinführung (Privatgrund)", Required: true, Extra: true},
			{Key: "bauweise", Label: "Bauweise", Kind: "enum", Group: "2. Tiefbau & Hauseinführung (Privatgrund)", Required: true, Extra: true,
				Options: []string{"Offener Graben", "Spülbohrung / Erdrakete", "Leerrohr Bestand genutzt"}},
			{Key: "oberflaeche", Label: "Oberfläche", Kind: "enum", Group: "2. Tiefbau & Hauseinführung (Privatgrund)", Extra: true,
				Options: []string{"Rasen / Erde", "Pflaster", "Asphalt", "Platten"}},
			{Key: "verlegetiefe_cm", Label: "Verlegetiefe (cm)", Kind: "number", Group: "2. Tiefbau & Hauseinführung (Privatgrund)", Extra: true, Default: "60"},
			{Key: "trassenwarnband", Label: "Trassenwarnband verlegt", Kind: "check", Group: "2. Tiefbau & Hauseinführung (Privatgrund)", Extra: true, Default: "true"},
			{Key: "hauseinfuehrung_typ", Label: "Hauseinführung", Kind: "enum", Group: "2. Tiefbau & Hauseinführung (Privatgrund)", Required: true, Extra: true,
				Options: []string{"Einsparten-Hauseinführung (EHE)", "Mehrsparten-Hauseinführung (MSH)", "Kernbohrung mit Ringraumdichtung"}},
			{Key: "abdichtung_ok", Label: "Gas- und wasserdichte Abdichtung geprüft", Kind: "check", Group: "2. Tiefbau & Hauseinführung (Privatgrund)", Extra: true, Default: "true"},

			// 3. Inneninstallation
			{Key: "montageort_gf_ap", Label: "Montageort Gf-AP", Kind: "enum", Group: "3. Inneninstallation (Gf-AP / Gf-TA)", Required: true, Extra: true,
				Options: []string{"Keller", "Hausanschlussraum (HWR)", "Flur / Treppenhaus", "Außenwand"}},
			{Key: "gf_ap_seriennr", Label: "Gf-AP Seriennummer / QR-Code", Kind: "text", Group: "3. Inneninstallation (Gf-AP / Gf-TA)", Required: true, Extra: true},
			{Key: "leitungsweg_innen", Label: "Leitungsweg im Gebäude", Kind: "enum", Group: "3. Inneninstallation (Gf-AP / Gf-TA)", Extra: true,
				Options: []string{"Leerrohr Bestand", "Aufputz im Kabelkanal", "Direktmontage neben Hauseinführung"}},
			{Key: "kabelkanal_m", Label: "Länge Kabelkanal (m)", Kind: "number", Group: "3. Inneninstallation (Gf-AP / Gf-TA)", Extra: true, Default: "0"},
			{Key: "brandschottung", Label: "Brandschottung ausgeführt (falls erforderlich)", Kind: "check", Group: "3. Inneninstallation (Gf-AP / Gf-TA)", Extra: true, Default: "false"},
			{Key: "gf_ta_montiert", Label: "Gf-TA (Teilnehmeranschlussdose) montiert", Kind: "check", Group: "3. Inneninstallation (Gf-AP / Gf-TA)", Extra: true, Default: "true"},
			{Key: "gf_ta_seriennr", Label: "Gf-TA Seriennummer / Dose-ID", Kind: "text", Group: "3. Inneninstallation (Gf-AP / Gf-TA)", Extra: true},
			{Key: "mikrorohr_farbe", Label: "Mikrorohr-Farbe / Faser-Nr.", Kind: "text", Group: "3. Inneninstallation (Gf-AP / Gf-TA)", Extra: true},

			// 4. Prüfung, Messwerte & Wiederherstellung
			{Key: "einblasprotokoll_vorhanden", Label: "Einblasprotokoll liegt vor", Kind: "check", Group: "4. Prüfung, Messwerte & Wiederherstellung", Extra: true, Default: "true"},
			{Key: "daempfung_db", Label: "Dämpfungsmessung (dB @ 1310/1550 nm)", Kind: "text", Group: "4. Prüfung, Messwerte & Wiederherstellung", Extra: true, Hint: "z.B. 0,32 dB (PASS)"},
			{Key: "oberflaeche_wiederhergestellt", Label: "Oberfläche ordnungsgemäß wiederhergestellt", Kind: "check", Group: "4. Prüfung, Messwerte & Wiederherstellung", Extra: true, Default: "true"},
			{Key: "remarks", Label: "Besonderheiten / Abweichungen", Kind: "textarea", Group: "4. Prüfung, Messwerte & Wiederherstellung"},

			// 5. Fotodokumentation
			{Key: "photo_graben_tiefe", Label: "Offener Graben mit Zollstock & Warnband", Kind: "photo", Group: "Fotodokumentation", Required: true},
			{Key: "photo_hauseinfuehrung_aussen", Label: "Hauseinführung außen (vor Verfüllung)", Kind: "photo", Group: "Fotodokumentation", Required: true},
			{Key: "photo_abdichtung_innen", Label: "Abdichtung / Ringraumdichtung innen", Kind: "photo", Group: "Fotodokumentation", Required: true},
			{Key: "photo_gf_ap_ta", Label: "Gf-AP / Gf-TA (offen & geschlossen mit QR-Code)", Kind: "photo", Group: "Fotodokumentation", Required: true},
			{Key: "photo_oberflaeche_fertig", Label: "Wiederhergestellte Oberfläche", Kind: "photo", Group: "Fotodokumentation", Required: true},

			// 6. Unterschriften
			{Key: "protocol_date", Label: "Datum", Kind: "date", Group: "Unterschriften", Required: true},
			{Key: "start_time", Label: "Uhrzeit (Fertigstellung)", Kind: "time", Group: "Unterschriften"},
			{Key: "sig_monteur", Label: "Unterschrift Bauleiter / Monteur (Wolken-ASM GmbH)", Kind: "signature", Group: "Unterschriften", Required: true},
			{Key: "sig_eigentuemer", Label: "Unterschrift Eigentümer / Bevollmächtigter", Kind: "signature", Group: "Unterschriften", Required: true},
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
		switch {
		case p.Status == "draft":
			label += " (Entwurf)"
		case p.Version != "":
			label += " (" + p.Version + ")"
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
	if p.DocumentTitle != "" {
		return p.DocumentTitle
	}
	title := p.Template + " · " + p.Label
	if p.Version != "" {
		title += " " + p.Version
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
