package main

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestMemoryGuardBlocksAfterLimit(t *testing.T) {
	g := newMemoryGuard()
	for i := 0; i < loginMaxFailures; i++ {
		if blocked, _ := g.LoginBlocked("1.2.3.4", "a@b.de"); blocked {
			t.Fatalf("blokiran prerano, posle %d pokušaja", i)
		}
		g.RecordLoginFailure("1.2.3.4", "a@b.de")
	}
	if blocked, retry := g.LoginBlocked("1.2.3.4", "a@b.de"); !blocked {
		t.Fatalf("posle %d neuspeha mora biti blokiran", loginMaxFailures)
	} else if retry <= 0 {
		t.Fatalf("Retry-After mora biti > 0, dobijeno %v", retry)
	}
}

func TestMemoryGuardBlocksByEmailAcrossIPs(t *testing.T) {
	g := newMemoryGuard()
	for i := 0; i < loginMaxFailures; i++ {
		g.RecordLoginFailure("10.0.0."+string(rune('1'+i%9)), "A@B.de")
	}
	// Isti nalog sa nove IP adrese mora biti blokiran.
	if blocked, _ := g.LoginBlocked("192.168.1.1", "a@b.de"); !blocked {
		t.Fatal("email mora biti blokiran bez obzira na IP")
	}
}

func TestMemoryGuardReset(t *testing.T) {
	g := newMemoryGuard()
	for i := 0; i < loginMaxFailures; i++ {
		g.RecordLoginFailure("1.2.3.4", "a@b.de")
	}
	g.ResetLoginFailures("1.2.3.4", "a@b.de")
	if blocked, _ := g.LoginBlocked("1.2.3.4", "a@b.de"); blocked {
		t.Fatal("posle uspešne prijave brojači moraju biti obrisani")
	}
}

func TestMemoryGuardWindowExpiry(t *testing.T) {
	g := newMemoryGuard()
	now := time.Now()
	g.now = func() time.Time { return now }

	for i := 0; i < loginMaxFailures; i++ {
		g.RecordLoginFailure("1.2.3.4", "a@b.de")
	}
	if blocked, _ := g.LoginBlocked("1.2.3.4", "a@b.de"); !blocked {
		t.Fatal("mora biti blokiran unutar prozora")
	}
	now = now.Add(loginWindow + time.Second)
	if blocked, _ := g.LoginBlocked("1.2.3.4", "a@b.de"); blocked {
		t.Fatal("posle isteka prozora blokada mora da padne")
	}
}

func TestMemoryGuardDenyToken(t *testing.T) {
	g := newMemoryGuard()
	g.DenyToken("jti-1", time.Minute)
	if !g.TokenDenied("jti-1") {
		t.Fatal("opozvan token mora biti odbijen")
	}
	if g.TokenDenied("jti-2") {
		t.Fatal("neopozvan token mora proći")
	}
	if g.TokenDenied("") {
		t.Fatal("prazan jti ne sme biti odbijen")
	}
}

func TestMemoryGuardUserInvalidation(t *testing.T) {
	g := newMemoryGuard()
	if !g.UserInvalidBefore(7).IsZero() {
		t.Fatal("novi korisnik nema opoziv")
	}
	at := time.Now().Truncate(time.Second)
	g.InvalidateUser(7, at)
	if got := g.UserInvalidBefore(7); !got.Equal(at) {
		t.Fatalf("očekivano %v, dobijeno %v", at, got)
	}
}

func claimsAt(userID int64, issued time.Time) *AuthClaims {
	return &AuthClaims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        "jti-x",
			IssuedAt:  jwt.NewNumericDate(issued),
			ExpiresAt: jwt.NewNumericDate(issued.Add(24 * time.Hour)),
		},
	}
}

func TestSessionStillValid(t *testing.T) {
	prev := authGuardState
	defer func() { authGuardState = prev }()

	g := newMemoryGuard()
	authGuardState = g

	issued := time.Now().Add(-time.Hour)
	c := claimsAt(7, issued)
	if !sessionStillValid(c) {
		t.Fatal("svež token mora važiti")
	}

	// Promena lozinke posle izdavanja tokena → sesija pada.
	g.InvalidateUser(7, time.Now())
	if sessionStillValid(c) {
		t.Fatal("token izdat pre promene lozinke mora biti odbijen")
	}

	// Novi token (izdat posle) prolazi.
	if !sessionStillValid(claimsAt(7, time.Now().Add(time.Second))) {
		t.Fatal("nov token posle opoziva mora proći")
	}

	// Logout opoziva konkretan token.
	g.DenyToken("jti-x", time.Minute)
	if sessionStillValid(claimsAt(9, time.Now())) {
		t.Fatal("opozvan jti mora biti odbijen")
	}
}

func TestSessionStillValidWithoutGuard(t *testing.T) {
	prev := authGuardState
	defer func() { authGuardState = prev }()
	authGuardState = nil
	if !sessionStillValid(claimsAt(1, time.Now())) {
		t.Fatal("bez guard-a sesija mora važiti (nema stanja)")
	}
}
