package main

import (
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
