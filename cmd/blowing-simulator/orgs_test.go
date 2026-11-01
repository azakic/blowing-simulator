package main

import "testing"

func TestNormalizeOrgName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Wolken-ASM    GmbH", "WOLKEN ASM"},
		{"Wolken-ASM GMBH", "WOLKEN ASM"},
		{"  Wolken-ASM  ", "WOLKEN ASM"},
		{"M.A.X. Bauservice GmbH", "M.A.X. BAUSERVICE"},
		{"M.A.X. Bauservice", "M.A.X. BAUSERVICE"},
		{"Müller Bau GmbH & Co. KG", "MUELLER BAU"},
		{"Straßenbau AG", "STRASSENBAU"},
		{"", ""},
		{"Wolken-ASM            Einbläser: Nemanja", "WOLKEN ASM EINBLAESER: NEMANJA"},
	}
	for _, c := range cases {
		if got := normalizeOrgName(c.in); got != c.want {
			t.Errorf("normalizeOrgName(%q) = %q, željeno %q", c.in, got, c.want)
		}
	}
}

// Isti ključ za sve varijante istog naziva — to je uslov da alias tabela radi.
func TestNormalizeOrgNameSameKey(t *testing.T) {
	variants := []string{
		"Wolken-ASM", "Wolken-ASM GmbH", "Wolken-ASM    GMBH", "Wolken ASM",
	}
	want := normalizeOrgName(variants[0])
	for _, v := range variants[1:] {
		if got := normalizeOrgName(v); got != want {
			t.Errorf("normalizeOrgName(%q) = %q, očekivano %q", v, got, want)
		}
	}
}
