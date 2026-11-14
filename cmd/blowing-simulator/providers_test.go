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
