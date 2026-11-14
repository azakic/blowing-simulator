package main

import "testing"

func TestProviderProfilesIntegrity(t *testing.T) {
	seen := map[string]bool{}
	var westnetz, telekom, verzatel *providerProfile
	for i := range providerProfiles {
		p := &providerProfiles[i]
		if p.Key == "" || p.Label == "" || p.Template == "" {
			t.Errorf("profil bez kljuca/labele/obrasca: %+v", *p)
		}
		if seen[p.Key] {
			t.Errorf("duplikat provajdera %q", p.Key)
		}
		seen[p.Key] = true
		if p.Status != "active" && p.Status != "draft" {
			t.Errorf("%s: status %q nije active/draft", p.Key, p.Status)
		}
		if p.Status == "active" && p.Version == "" {
			t.Errorf("%s: aktivan profil mora imati verziju obrasca", p.Key)
		}
		fieldKeys := map[string]bool{}
		for _, f := range p.Fields {
			if f.Key == "" || f.Label == "" || f.Kind == "" {
				t.Errorf("%s: nepotpuno polje %+v", p.Key, f)
			}
			if fieldKeys[f.Key] {
				t.Errorf("%s: duplikat polja %q", p.Key, f.Key)
			}
			fieldKeys[f.Key] = true
		}
		switch p.Key {
		case "westnetz":
			westnetz = p
		case "telekom":
			telekom = p
		case "verzatel":
			verzatel = p
		}
	}
	if westnetz == nil || telekom == nil || verzatel == nil {
		t.Fatal("nedostaje neki od profila")
	}
	if westnetz.Status != "active" || westnetz.Version != "1.5.1" {
		t.Errorf("westnetz: status=%q version=%q", westnetz.Status, westnetz.Version)
	}
	if len(westnetz.Fields) < 30 {
		t.Errorf("westnetz ima %d polja, ocekivano 30+", len(westnetz.Fields))
	}
	if telekom.Status != "draft" || verzatel.Status != "draft" {
		t.Error("telekom/verzatel treba da budu draft dok ne stignu obrasci")
	}
}

func TestProviderFallbackAndHelpers(t *testing.T) {
	if p := providerProfileFor("nepoznat"); p.Key != "westnetz" {
		t.Errorf("fallback = %q, ocekivano westnetz", p.Key)
	}
	if got := protocolTitle("telekom"); got != "Begehungs- und Übergabeprotokoll Glasfaser-Hausanschluss (HA)" {
		t.Errorf("protocolTitle(telekom) = %q", got)
	}
	if got := protocolTitle("westnetz"); got != "Hausanschlussprotokoll · Westnetz 1.5.1" {
		t.Errorf("protocolTitle = %q", got)
	}
	if got := providerLabel("telekom"); got != "Deutsche Telekom Technik GmbH telekom-ha-v1" {
		t.Errorf("providerLabel(telekom) = %q", got)
	}
	if !validProvider("telekom") || validProvider("nesto") {
		t.Error("validProvider ne radi")
	}
	choices := providerChoices()
	found := map[string]string{}
	for _, c := range choices {
		found[c.Value] = c.Label
	}
	if found["westnetz"] != "Westnetz (1.5.1)" {
		t.Errorf("westnetz label = %q", found["westnetz"])
	}
	if found["telekom"] != "Deutsche Telekom Technik GmbH (Entwurf)" {
		t.Errorf("telekom label = %q", found["telekom"])
	}
}

// Telekom: specifikacija po ZTV TK-Netz 39A/40 (Gemini predlog, usvojen).
func TestTelekomProfileSpec(t *testing.T) {
	p := providerProfileFor("telekom")
	if p.Status != "draft" {
		t.Errorf("status = %q, ocekivano draft dok ne stigne zvanicni obrazac", p.Status)
	}
	if p.DocumentTitle == "" || p.Version == "" {
		t.Error("document_title i version moraju biti popunjeni")
	}
	if len(p.Fields) < 35 {
		t.Fatalf("telekom ima %d polja, ocekivano 35+", len(p.Fields))
	}

	groups := map[string]int{}
	byKey := providerFieldKeys("telekom")
	photos, signs, enums, extras := 0, 0, 0, 0
	for _, f := range p.Fields {
		if f.Group == "" {
			t.Errorf("polje %q nema grupu", f.Key)
		}
		groups[f.Group]++
		switch f.Kind {
		case "photo":
			photos++
			if !f.Required {
				t.Errorf("%q: fotoslot mora biti obavezan", f.Key)
			}
		case "signature":
			signs++
		case "enum":
			enums++
			if len(f.Options) < 2 {
				t.Errorf("%q: enum bez opcija", f.Key)
			}
		}
		if f.Extra {
			extras++
		}
	}
	if len(groups) != 6 {
		t.Errorf("grupa = %d, ocekivano 6 (%v)", len(groups), groups)
	}
	if photos != 5 {
		t.Errorf("foto slotova = %d, ocekivano 5", photos)
	}
	if signs != 2 {
		t.Errorf("potpisa = %d, ocekivano 2 (monteur + eigentuemer)", signs)
	}
	if enums != 5 {
		t.Errorf("enuma = %d, ocekivano 5", enums)
	}
	if extras < 20 {
		t.Errorf("extra polja = %d, ocekivano 20+", extras)
	}

	for _, key := range []string{
		"nvt", "kls_id", "units", "address", "postcode", "city",
		"client_last_name", "client_first_name", "client_company",
		"client_phone", "client_email", "auftraggeber", "auftragnehmer",
		"trassenlaenge_m", "bauweise", "oberflaeche", "verlegetiefe_cm",
		"trassenwarnband", "hauseinfuehrung_typ", "abdichtung_ok",
		"montageort_gf_ap", "gf_ap_seriennr", "leitungsweg_innen", "kabelkanal_m",
		"brandschottung", "gf_ta_montiert", "gf_ta_seriennr", "mikrorohr_farbe",
		"einblasprotokoll_vorhanden", "daempfung_db", "oberflaeche_wiederhergestellt",
		"remarks", "protocol_date", "start_time",
		"photo_graben_tiefe", "photo_hauseinfuehrung_aussen", "photo_abdichtung_innen",
		"photo_gf_ap_ta", "photo_oberflaeche_fertig",
		"sig_monteur", "sig_eigentuemer",
	} {
		if _, ok := byKey[key]; !ok {
			t.Errorf("nedostaje polje %q", key)
		}
	}

	for _, key := range []string{
		"nvt", "kls_id", "address", "postcode", "city", "client_last_name",
		"trassenlaenge_m", "bauweise", "hauseinfuehrung_typ", "montageort_gf_ap",
		"gf_ap_seriennr", "protocol_date", "sig_monteur", "sig_eigentuemer",
	} {
		if !byKey[key].Required {
			t.Errorf("%q treba da bude obavezno", key)
		}
	}

	// Default vrednosti iz predloga.
	for key, want := range map[string]string{
		"units":                         "1",
		"verlegetiefe_cm":               "60",
		"trassenwarnband":               "true",
		"kabelkanal_m":                  "0",
		"brandschottung":                "false",
		"gf_ta_montiert":                "true",
		"einblasprotokoll_vorhanden":    "true",
		"oberflaeche_wiederhergestellt": "true",
		"auftraggeber":                  "Deutsche Telekom Technik GmbH",
	} {
		if byKey[key].Default != want {
			t.Errorf("%q default = %q, ocekivano %q", key, byKey[key].Default, want)
		}
	}

	// Enum opcije po predlogu.
	if len(byKey["bauweise"].Options) != 3 {
		t.Errorf("Bauweise opcije = %v", byKey["bauweise"].Options)
	}
	if len(byKey["oberflaeche"].Options) != 4 {
		t.Errorf("Oberflaeche opcije = %v", byKey["oberflaeche"].Options)
	}
	if len(byKey["hauseinfuehrung_typ"].Options) != 3 {
		t.Errorf("Hauseinfuehrung opcije = %v", byKey["hauseinfuehrung_typ"].Options)
	}
	if len(byKey["montageort_gf_ap"].Options) != 4 {
		t.Errorf("Montageort opcije = %v", byKey["montageort_gf_ap"].Options)
	}
	if len(byKey["leitungsweg_innen"].Options) != 3 {
		t.Errorf("Leitungsweg opcije = %v", byKey["leitungsweg_innen"].Options)
	}

	// Telekom-specificno ide u extra, univerzalno ne.
	if !byKey["kls_id"].Extra || !byKey["trassenlaenge_m"].Extra || !byKey["daempfung_db"].Extra {
		t.Error("Telekom-specificna polja moraju ici u extra")
	}
	if byKey["nvt"].Extra || byKey["address"].Extra || byKey["client_last_name"].Extra {
		t.Error("univerzalna polja ne idu u extra")
	}
}

func TestProviderFormSupported(t *testing.T) {
	if !providerFormSupported("westnetz") {
		t.Error("westnetz mora imati formu")
	}
	for _, k := range []string{"telekom", "verzatel"} {
		if providerFormSupported(k) {
			t.Errorf("%s još nema formu — HaP ne sme da se izda", k)
		}
	}
}
