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
	if got := protocolTitle("westnetz"); got != "Hausanschlussprotokoll · Westnetz 1.5.1" {
		t.Errorf("protocolTitle = %q", got)
	}
	if got := providerLabel("telekom"); got != "Telekom" {
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
	if found["westnetz"] != "Westnetz 1.5.1" {
		t.Errorf("westnetz label = %q", found["westnetz"])
	}
	if found["telekom"] != "Telekom (Entwurf)" {
		t.Errorf("telekom label = %q", found["telekom"])
	}
}

// Telekom: specifikacija po ZTV TK-Netz 39A/40 mora biti kompletna u profilu
// (grupe, obavezna polja, enumi, fotoslotovi i potpisi).
func TestTelekomProfileSpec(t *testing.T) {
	p := providerProfileFor("telekom")
	if p.Status != "draft" {
		t.Errorf("status = %q, ocekivano draft dok ne stigne zvanicni obrazac", p.Status)
	}
	if len(p.Fields) < 40 {
		t.Fatalf("telekom ima %d polja, ocekivano 40+", len(p.Fields))
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
	if photos != 6 {
		t.Errorf("foto slotova = %d, ocekivano 6", photos)
	}
	if signs != 2 {
		t.Errorf("potpisa = %d, ocekivano 2 (monteur + eigentumer)", signs)
	}
	if enums < 5 || extras < 20 {
		t.Errorf("enum = %d, extra = %d", enums, extras)
	}

	for _, key := range []string{
		"nvt", "kls_id", "units", "address", "postcode", "city",
		"client_last_name", "client_phone", "client_email",
		"auftraggeber", "auftragnehmer",
		"trench_length_m", "construction_method", "surface_type", "laying_depth_m",
		"warning_tape", "house_entry_type", "house_entry_sealed",
		"gfap_location", "gfap_sn", "cable_route", "cable_route_length_m",
		"fireproofing", "gfta_installed", "gfta_sn", "microduct_color", "fibre_no",
		"blowing_protocol", "attenuation_1310_db", "attenuation_1550_db",
		"attenuation_1625_db", "attenuation_pass", "surface_restored",
		"foto_graben", "foto_hauseinfuehrung", "foto_ringraum",
		"foto_gfap_offen", "foto_gfap_geschlossen", "foto_oberflaeche",
		"remarks", "protocol_date", "start_time", "sig_monteur", "sig_kunde",
	} {
		if _, ok := byKey[key]; !ok {
			t.Errorf("nedostaje polje %q", key)
		}
	}
	for _, key := range []string{"nvt", "kls_id", "address", "client_last_name", "protocol_date", "gfta_sn", "sig_monteur", "sig_kunde"} {
		if !byKey[key].Required {
			t.Errorf("%q treba da bude obavezno", key)
		}
	}
	if len(byKey["construction_method"].Options) != 3 {
		t.Errorf("Bauweise opcije = %v", byKey["construction_method"].Options)
	}
	if len(byKey["surface_type"].Options) != 4 {
		t.Errorf("Oberflaeche opcije = %v", byKey["surface_type"].Options)
	}
	if !byKey["kls_id"].Extra || !byKey["trench_length_m"].Extra {
		t.Error("Telekom-specificna polja moraju ici u extra")
	}
	if byKey["nvt"].Extra || byKey["address"].Extra {
		t.Error("univerzalna polja ne idu u extra")
	}
}
