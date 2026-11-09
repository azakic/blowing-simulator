package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
)

// CSRF zaštita (double-submit cookie).
//
// Token živi u kolačiću `csrf_token` (HttpOnly, SameSite=Lax) i mora se vratiti
// u svakom POST/PUT/PATCH/DELETE zahtevu — bilo kao polje forme `csrf_token`,
// bilo kao header `X-CSRF-Token` (za `fetch` pozive). Vrednosti se porede u
// konstantnom vremenu.
//
// `SameSite=Lax` sam po sebi blokira cross-site POST, ali eksplicitni token
// štiti i od slučajeva kada pretraživač ne primeni SameSite politiku i od
// podmetnutih formi sa istog sajta (npr. XSS u drugom delu aplikacije).
const (
	csrfCookieName = "csrf_token"
	csrfFormField  = "csrf_token"
	csrfHeaderName = "X-CSRF-Token"
)

type csrfContextKey struct{}

// newCSRFToken vraća 32 slučajna bajta kao hex (64 znaka).
func newCSRFToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// csrfTokenFor vraća važeći token za zahtev, a kolačić postavlja ako ga nema.
// Token se dodaje i u sam zahtev da bi ga šablon mogao da ispiše u istom
// odgovoru (prvi GET posle brisanja kolačića).
func csrfTokenFor(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(csrfCookieName); err == nil && len(c.Value) >= 32 {
		return c.Value
	}
	tok := newCSRFToken()
	if tok == "" {
		return ""
	}
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: tok})
	return tok
}

// csrfTokenValue čita token koji je middleware stavio u kontekst (ili kolačić).
func csrfTokenValue(r *http.Request) string {
	if v, ok := r.Context().Value(csrfContextKey{}).(string); ok && v != "" {
		return v
	}
	if c, err := r.Cookie(csrfCookieName); err == nil {
		return c.Value
	}
	return ""
}

// csrfTemplateFuncs vraća funkcije za šablone. Vraća `map[string]any` da bi se
// mogao koristiti i sa text/template i sa html/template.
func csrfTemplateFuncs(r *http.Request) map[string]any {
	return map[string]any{
		"csrfToken": func() string { return csrfTokenValue(r) },
	}
}

// csrfTokenFromRequest traži token u telu zahteva (form ili multipart).
func csrfTokenFromRequest(r *http.Request) string {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(8 << 20); err == nil && r.MultipartForm != nil {
			if v := r.MultipartForm.Value[csrfFormField]; len(v) > 0 {
				return strings.TrimSpace(v[0])
			}
		}
		return ""
	}
	if err := r.ParseForm(); err == nil {
		return strings.TrimSpace(r.Form.Get(csrfFormField))
	}
	return ""
}

func csrfEqual(a, b string) bool {
	if a == "" || b == "" || len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// withCSRF proverava token na svim "menjajućim" metodama i obezbeđuje da
// handler dobije token u kontekstu (za `{{csrfToken}}` u šablonu).
//
// Redosled: `requireAuth(withCSRF(handler))` — autentifikacija ide prva da
// neautentifikovan zahtev ne bi terao server da parsira veliko telo uploada.
func withCSRF(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := csrfTokenFor(w, r)
		r = r.WithContext(context.WithValue(r.Context(), csrfContextKey{}, token))

		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			h(w, r)
			return
		}

		provided := strings.TrimSpace(r.Header.Get(csrfHeaderName))
		if provided == "" {
			provided = csrfTokenFromRequest(r)
		}
		if !csrfEqual(token, provided) {
			http.Error(w, "Invalid or missing CSRF token — reload the page and try again.", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}
