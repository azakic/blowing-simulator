package main

import "testing"

func TestParseCustomerCSVWithPlaceAndEmail(t *testing.T) {
	data := []byte("Adresse;NVT;Kunde;Telefon;Mobil;PLZ;Ort;E-Mail\r\n" +
		"Am Hang 10;1.25;Heinz Schmidt;0;49233522201;58300;Wetter (Ruhr);heinz@example.de\r\n" +
		"Bachstraße 30;1.25;Frank Rambau;0179 751295;;58313;Herdecke;\r\n")
	pf, err := parseCustomerCSV("test.csv", data)
	if err != nil {
		t.Fatal(err)
	}
	if !pf.HeaderDetected {
		t.Fatal("zaglavlje nije prepoznato")
	}
	if len(pf.Rows) != 2 {
		t.Fatalf("redova = %d, očekivano 2", len(pf.Rows))
	}
	first := pf.Rows[0]
	if first.Postcode != "58300" || first.City != "Wetter (Ruhr)" || first.Email != "heinz@example.de" {
		t.Errorf("prvi red: plz=%q city=%q email=%q", first.Postcode, first.City, first.Email)
	}
	second := pf.Rows[1]
	if second.Postcode != "58313" || second.City != "Herdecke" || second.Email != "" {
		t.Errorf("drugi red: plz=%q city=%q email=%q", second.Postcode, second.City, second.Email)
	}
}

func TestPlaceHeaders(t *testing.T) {
	postcodes := []string{"plz", "Postleitzahl", "Postcode", "ZIP", "zipcode"}
	for _, h := range postcodes {
		if !isPostcodeHeader(normalizeHeader(h)) {
			t.Errorf("%q nije prepoznat kao PLZ", h)
		}
	}
	cities := []string{"Ort", "Ortsteil", "Stadt", "Gemeinde", "Wohnort", "City"}
	for _, h := range cities {
		if !isCityHeader(normalizeHeader(h)) {
			t.Errorf("%q nije prepoznat kao mesto", h)
		}
	}
	emails := []string{"E-Mail", "Email", "e_mail", "Mail"}
	for _, h := range emails {
		if !isEmailHeader(normalizeHeader(h)) {
			t.Errorf("%q nije prepoznat kao e-pošta", h)
		}
	}
	// Ne sme da se pomeša sa postojećim kolonama.
	if isCityHeader(normalizeHeader("Telefon")) || isPostcodeHeader(normalizeHeader("Kunde")) {
		t.Error("lažno prepoznavanje kolone")
	}
}

func TestImportAddressParts(t *testing.T) {
	// PLZ/Ort iz adrese.
	st, no, plz, city := importAddressParts(customerRow{Address: "Am Hang 10, 58300 Wetter (Ruhr)"})
	if st != "Am Hang" || no != "10" || plz != "58300" || city != "Wetter (Ruhr)" {
		t.Errorf("iz adrese: %q %q %q %q", st, no, plz, city)
	}
	// Adresa bez mesta → dopuna iz kolona.
	_, _, plz, city = importAddressParts(customerRow{Address: "Am Hang 10", Postcode: "58313", City: "Herdecke"})
	if plz != "58313" || city != "Herdecke" {
		t.Errorf("iz kolona: plz=%q city=%q", plz, city)
	}
	// Adresa nosi mesto → kolona ne prepisuje.
	_, _, plz, city = importAddressParts(customerRow{Address: "Am Hang 10, 58300 Wetter", Postcode: "99999", City: "Nirgendwo"})
	if plz != "58300" || city != "Wetter" {
		t.Errorf("adresa treba da pobedi: plz=%q city=%q", plz, city)
	}
}
