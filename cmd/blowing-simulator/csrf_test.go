package main

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// csrfCookieFrom vraća csrf kolačić iz odgovora (ili nil).
func csrfCookieFrom(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == csrfCookieName {
			return c
		}
	}
	return nil
}

func TestCSRFSetsCookieOnGET(t *testing.T) {
	var got string
	h := withCSRF(func(w http.ResponseWriter, r *http.Request) {
		got = csrfTokenValue(r)
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	c := csrfCookieFrom(rec)
	if c == nil {
		t.Fatal("GET mora postaviti csrf_token kolačić")
	}
	if len(c.Value) < 32 {
		t.Fatalf("token prekratak: %q", c.Value)
	}
	if got != c.Value {
		t.Fatalf("token u kontekstu (%q) se razlikuje od kolačića (%q)", got, c.Value)
	}
	if !c.HttpOnly {
		t.Fatal("kolačić mora biti HttpOnly")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Fatal("kolačić mora biti SameSite=Lax")
	}
}

func TestCSRFReusesExistingCookie(t *testing.T) {
	h := withCSRF(func(w http.ResponseWriter, r *http.Request) {})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: strings.Repeat("a", 64)})
	h(rec, req)

	if c := csrfCookieFrom(rec); c != nil {
		t.Fatalf("postojeći kolačić ne treba ponovo postavljati, dobijeno %q", c.Value)
	}
}

func TestCSRFRejectsMissingToken(t *testing.T) {
	called := false
	h := withCSRF(func(w http.ResponseWriter, r *http.Request) { called = true })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("x=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: strings.Repeat("b", 64)})
	h(rec, req)

	if called {
		t.Fatal("handler ne sme da se pozove bez tokena")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("očekivan 403, dobijeno %d", rec.Code)
	}
}

func TestCSRFRejectsWrongToken(t *testing.T) {
	called := false
	h := withCSRF(func(w http.ResponseWriter, r *http.Request) { called = true })

	rec := httptest.NewRecorder()
	form := url.Values{csrfFormField: {strings.Repeat("c", 64)}}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: strings.Repeat("b", 64)})
	h(rec, req)

	if called {
		t.Fatal("handler ne sme da se pozove sa pogrešnim tokenom")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("očekivan 403, dobijeno %d", rec.Code)
	}
}

func TestCSRFAcceptsFormToken(t *testing.T) {
	called := false
	h := withCSRF(func(w http.ResponseWriter, r *http.Request) { called = true })

	tok := strings.Repeat("d", 64)
	form := url.Values{csrfFormField: {tok}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: tok})
	h(rec, req)

	if !called || rec.Code != http.StatusOK {
		t.Fatalf("validan token mora proći (called=%v code=%d)", called, rec.Code)
	}
}

func TestCSRFAcceptsHeaderToken(t *testing.T) {
	called := false
	h := withCSRF(func(w http.ResponseWriter, r *http.Request) { called = true })

	tok := strings.Repeat("e", 64)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	req.Header.Set(csrfHeaderName, tok)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: tok})
	h(rec, req)

	if !called || rec.Code != http.StatusOK {
		t.Fatalf("header token mora proći (called=%v code=%d)", called, rec.Code)
	}
}

func TestCSRFAllowsSafeMethods(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		called := false
		h := withCSRF(func(w http.ResponseWriter, r *http.Request) { called = true })
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(m, "/", nil))
		if !called {
			t.Fatalf("%s ne sme da traži token", m)
		}
	}
}

func TestCSRFAcceptsMultipartToken(t *testing.T) {
	called := false
	h := withCSRF(func(w http.ResponseWriter, r *http.Request) { called = true })

	tok := strings.Repeat("f", 64)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := mw.WriteField(csrfFormField, tok); err != nil {
		t.Fatal(err)
	}
	mw.WriteField("autoSave", "false")
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bulk-upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: tok})
	h(rec, req)

	if !called || rec.Code != http.StatusOK {
		t.Fatalf("multipart token mora proći (called=%v code=%d)", called, rec.Code)
	}
}

func TestCSRFRejectsMultipartWithoutToken(t *testing.T) {
	called := false
	h := withCSRF(func(w http.ResponseWriter, r *http.Request) { called = true })

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("autoSave", "false")
	mw.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bulk-upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: strings.Repeat("f", 64)})
	h(rec, req)

	if called || rec.Code != http.StatusForbidden {
		t.Fatalf("multipart bez tokena mora 403 (called=%v code=%d)", called, rec.Code)
	}
}
