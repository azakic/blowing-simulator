package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDedupeEmails(t *testing.T) {
	got := dedupeEmails([]string{" A@B.de ", "a@b.de", "", "nope", "c@d.de"})
	if len(got) != 2 || got[0] != "a@b.de" || got[1] != "c@d.de" {
		t.Fatalf("dedupeEmails = %v", got)
	}
}

func TestMailerDisabledWithoutKey(t *testing.T) {
	t.Setenv("MAIL_API_KEY", "")
	t.Setenv("MAIL_FROM", "")
	m := NewMailerFromEnv()
	if m.Enabled() {
		t.Fatal("mailer bez ključa mora biti isključen")
	}
	// Send na isključenom maileru ne sme da paničari.
	m.Send([]string{"a@b.de"}, "s", "t")
}

func TestResendSend(t *testing.T) {
	var auth string
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &payload)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer srv.Close()

	m := &mailer{provider: "resend", apiKey: "re_test", from: "Reports <n@example.de>",
		apiBase: srv.URL, client: srv.Client()}
	if err := m.send([]string{"a@b.de"}, "Naslov", "Telo"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if auth != "Bearer re_test" {
		t.Errorf("Authorization = %q", auth)
	}
	if payload["subject"] != "Naslov" || payload["from"] != "Reports <n@example.de>" || payload["text"] != "Telo" {
		t.Errorf("payload = %v", payload)
	}
	to, _ := payload["to"].([]any)
	if len(to) != 1 || to[0] != "a@b.de" {
		t.Errorf("to = %v", payload["to"])
	}
}

func TestResendSendReportsProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"domain not verified"}`))
	}))
	defer srv.Close()

	m := &mailer{provider: "resend", apiKey: "k", from: "n@example.de",
		apiBase: srv.URL, client: srv.Client()}
	err := m.send([]string{"a@b.de"}, "s", "t")
	if err == nil || !strings.Contains(err.Error(), "domain not verified") {
		t.Fatalf("očekivana greška provajdera, dobijeno %v", err)
	}
}

func TestNotifyResetLinkDisabled(t *testing.T) {
	mail = nil
	if notifyResetLink(1, "/reset?t=x") {
		t.Fatal("bez mailera notifyResetLink mora vratiti false")
	}
}

func TestBuildMessage(t *testing.T) {
	msg := string(buildMessage("Wolken-ASM Reports <noreply@wolken-asm.de>",
		[]string{"a@b.de", "c@d.de"}, "Nova registracija čeka odobrenje",
		"Telo: čšđžć\n"))
	for _, want := range []string{
		"From: Wolken-ASM Reports <noreply@wolken-asm.de>\r\n",
		"To: a@b.de, c@d.de\r\n",
		"Subject: =?utf-8?",
		"Content-Type: text/plain; charset=utf-8\r\n",
		"Content-Transfer-Encoding: base64\r\n",
		"Auto-Submitted: auto-generated\r\n",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("poruci nedostaje %q\n---\n%s", want, msg)
		}
	}
	parts := strings.SplitN(msg, "\r\n\r\n", 2)
	if len(parts) != 2 {
		t.Fatal("nema tela poruke")
	}
	raw := strings.ReplaceAll(parts[1], "\r\n", "")
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("base64: %v", err)
	}
	if string(decoded) != "Telo: čšđžć\n" {
		t.Errorf("telo = %q", string(decoded))
	}
}

func TestMailerEnabledSMTP(t *testing.T) {
	t.Setenv("MAIL_PROVIDER", "smtp")
	t.Setenv("MAIL_FROM", "Wolken-ASM Reports <noreply@wolken-asm.de>")
	t.Setenv("MAIL_SMTP_USER", "info@wolken-asm.de")
	t.Setenv("MAIL_SMTP_PASS", "app-password")
	if m := NewMailerFromEnv(); !m.Enabled() {
		t.Fatal("smtp sa nalogom i lozinkom mora biti uključen")
	}
	t.Setenv("MAIL_SMTP_PASS", "")
	if m := NewMailerFromEnv(); m.Enabled() {
		t.Fatal("smtp bez lozinke mora biti isključen")
	}
}

func TestMailerEnabledSMTPRelay(t *testing.T) {
	t.Setenv("MAIL_PROVIDER", "smtp")
	t.Setenv("MAIL_FROM", "Wolken-ASM Reports <noreply@wolken-asm.de>")
	t.Setenv("MAIL_SMTP_USER", "")
	t.Setenv("MAIL_SMTP_PASS", "")
	t.Setenv("MAIL_SMTP_AUTH", "none")
	if m := NewMailerFromEnv(); !m.Enabled() {
		t.Fatal("relay mod (MAIL_SMTP_AUTH=none) mora biti uključen bez lozinke")
	}
	t.Setenv("MAIL_SMTP_AUTH", "")
	if m := NewMailerFromEnv(); m.Enabled() {
		t.Fatal("bez AUTH=none i bez lozinke mailer mora biti isključen")
	}
}

func TestEnvelopeAddress(t *testing.T) {
	cases := map[string]string{
		"Wolken-ASM Reports <noreply@wolken-asm.de>": "noreply@wolken-asm.de",
		"noreply@wolken-asm.de":                      "noreply@wolken-asm.de",
		"  <a@b.de>  ":                               "a@b.de",
	}
	for in, want := range cases {
		if got := envelopeAddress(in); got != want {
			t.Errorf("envelopeAddress(%q) = %q, očekivano %q", in, got, want)
		}
	}
}
