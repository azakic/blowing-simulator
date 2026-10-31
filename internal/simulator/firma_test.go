package simulator

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestCleanOrgName(t *testing.T) {
	cases := map[string]string{
		"Wolken-ASM    GmbH":   "Wolken-ASM GmbH",
		"Wolken-ASM  GmbHGmbH": "Wolken-ASM GmbH",
		"Wolken-ASM    GmbH                          Einbläser": "Wolken-ASM GmbH Einbläser",
		"M.A.X. Bauservice GmbH":                                "M.A.X. Bauservice GmbH",
		":;  M.A.X. Bauservice GmbH  ;":                         "M.A.X. Bauservice GmbH",
		"":                                                      "",
	}
	for in, want := range cases {
		if got := cleanOrgName(in); got != want {
			t.Errorf("cleanOrgName(%q) = %q, očekivano %q", in, got, want)
		}
	}
}

func TestExtractFirmaInfoLayout(t *testing.T) {
	lines := []string{
		"        SpeedNet-System                                  Einblas - Protokoll",
		"",
		"Firma                           Wolken-ASM    GmbH",
		"                                M.A.X. Bauservice GmbH                                               Einbläser: Marko",
		"Bemerkungen                     Stoped 412m",
	}
	got := extractFirmaInfo(lines)
	if got.Company != "Wolken-ASM GmbH" {
		t.Errorf("Company = %q, očekivano %q", got.Company, "Wolken-ASM GmbH")
	}
	if got.ServiceProvider != "M.A.X. Bauservice GmbH" {
		t.Errorf("ServiceProvider = %q, očekivano %q", got.ServiceProvider, "M.A.X. Bauservice GmbH")
	}
	if got.Operator != "Marko" {
		t.Errorf("Operator = %q, očekivano %q", got.Operator, "Marko")
	}
}

// Bez uvlake (npr. NormalizeFremcoTxt trimuje razmake) drugi red se i dalje
// prepoznaje po pravnoj formi.
func TestExtractFirmaInfoWithoutIndent(t *testing.T) {
	lines := []string{
		"Firma Wolken-ASM GmbH",
		"M.A.X. Bauservice GmbH            Einbläser: Nemanja",
		"Bemerkungen Stoped 12m",
	}
	got := extractFirmaInfo(lines)
	if got.Company != "Wolken-ASM GmbH" {
		t.Errorf("Company = %q", got.Company)
	}
	if got.ServiceProvider != "M.A.X. Bauservice GmbH" {
		t.Errorf("ServiceProvider = %q", got.ServiceProvider)
	}
	if got.Operator != "Nemanja" {
		t.Errorf("Operator = %q", got.Operator)
	}
}

func TestExtractFirmaInfoMissing(t *testing.T) {
	got := extractFirmaInfo([]string{"Bauvorhaben Nr. SM1", "Bemerkungen Stoped"})
	if got.Company != "" || got.ServiceProvider != "" || got.Operator != "" {
		t.Errorf("očekivan prazan FirmaInfo, dobijeno %+v", got)
	}
}

// Gated: PDF_TEST_FILE=/path/to/protocol.pdf go test ./internal/simulator -run RealPDF
// Proverava ceo put: pdftotext -layout -> NormalizeJettingTxt -> Parser.
func TestFirmaFromRealPDF(t *testing.T) {
	path := os.Getenv("PDF_TEST_FILE")
	if path == "" {
		t.Skip("PDF_TEST_FILE nije postavljen")
	}
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext nije instaliran")
	}
	out, err := exec.Command("pdftotext", "-layout", path, "-").Output()
	if err != nil {
		t.Fatalf("pdftotext: %v", err)
	}
	raw := string(out)
	normalized := NormalizeJettingTxt(raw)

	info := extractFirmaInfo(strings.Split(normalized, "\n"))
	if info.Company != "Wolken-ASM GmbH" {
		t.Errorf("Company = %q", info.Company)
	}
	if info.ServiceProvider != "M.A.X. Bauservice GmbH" {
		t.Errorf("ServiceProvider = %q", info.ServiceProvider)
	}
	if info.Operator != "Marko" {
		t.Errorf("Operator = %q", info.Operator)
	}

	proto := ParseJettingProtocol(normalized)
	if proto == nil {
		t.Fatal("ParseJettingProtocol vratio nil")
	}
	pi := proto.ProtocolInfo
	if pi.Company != "Wolken-ASM GmbH" || pi.ServiceProvider != "M.A.X. Bauservice GmbH" {
		t.Errorf("ProtocolInfo: company=%q provider=%q", pi.Company, pi.ServiceProvider)
	}
	if pi.Operator == nil || *pi.Operator != "Marko" {
		t.Errorf("ProtocolInfo.Operator = %v", pi.Operator)
	}
}
