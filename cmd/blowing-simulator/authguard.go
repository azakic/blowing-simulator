package main

import (
	"context"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// authGuard drži stanje koje mora da bude zajedničko za sve replike:
// brojače neuspešnih prijava (brute-force zaštita) i opozive JWT-a.
//
// Redis je opcion: ako `REDIS_ADDR` nije postavljen ili Redis ne odgovori,
// koristi se procesna memorija (radi tačno, ali samo za jedan pod — sa dve
// replike brojači se ne dele). Zato se pri startu loguje koji je backend.
type authGuard interface {
	// LoginBlocked vraća da li je par (IP, email) prekoračio dozvoljeni broj
	// neuspešnih prijava u tekućem prozoru.
	LoginBlocked(ip, email string) (bool, time.Duration)
	// RecordLoginFailure uvećava brojače za IP i email.
	RecordLoginFailure(ip, email string)
	// ResetLoginFailures briše brojače posle uspešne prijave.
	ResetLoginFailures(ip, email string)

	// DenyToken opoziva jedan JWT (logout) do isteka njegovog `exp`.
	DenyToken(jti string, ttl time.Duration)
	// TokenDenied kaže da li je taj JWT opozvan.
	TokenDenied(jti string) bool

	// InvalidateUser opoziva **sve** sesije korisnika izdate pre `at`
	// (promena lozinke, reset, admin akcija).
	InvalidateUser(userID int64, at time.Time)
	// UserInvalidBefore vraća trenutak pre kojeg sesije više ne važe.
	UserInvalidBefore(userID int64) time.Time
}

const (
	loginMaxFailures = 10
	loginWindow      = 15 * time.Minute
	keyLoginIP       = "auth:loginfail:ip:"
	keyLoginEmail    = "auth:loginfail:email:"
	keyDenyJTI       = "auth:deny:jti:"
	keyValidAfter    = "auth:valid_after:"
)

// loginKeys vraća ključeve brojača za par (IP, email); prazan email se preskače.
func loginKeys(ip, email string) []string {
	keys := make([]string, 0, 2)
	if ip != "" {
		keys = append(keys, keyLoginIP+ip)
	}
	if e := strings.ToLower(strings.TrimSpace(email)); e != "" {
		keys = append(keys, keyLoginEmail+e)
	}
	return keys
}

// --- Redis backend ---

type redisGuard struct {
	rdb *redis.Client
	ctx context.Context
}

func newRedisGuard(addr, password string, db int) *redisGuard {
	rdb := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           db,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	})
	return &redisGuard{rdb: rdb, ctx: context.Background()}
}

func (g *redisGuard) ping() error {
	ctx, cancel := context.WithTimeout(g.ctx, 3*time.Second)
	defer cancel()
	return g.rdb.Ping(ctx).Err()
}

func (g *redisGuard) LoginBlocked(ip, email string) (bool, time.Duration) {
	ctx, cancel := context.WithTimeout(g.ctx, time.Second)
	defer cancel()
	for _, k := range loginKeys(ip, email) {
		n, err := g.rdb.Get(ctx, k).Int()
		if err != nil {
			continue
		}
		if n >= loginMaxFailures {
			ttl, _ := g.rdb.TTL(ctx, k).Result()
			if ttl < 0 {
				ttl = loginWindow
			}
			return true, ttl
		}
	}
	return false, 0
}

func (g *redisGuard) RecordLoginFailure(ip, email string) {
	ctx, cancel := context.WithTimeout(g.ctx, time.Second)
	defer cancel()
	pipe := g.rdb.Pipeline()
	for _, k := range loginKeys(ip, email) {
		pipe.Incr(ctx, k)
		pipe.Expire(ctx, k, loginWindow)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("auth guard: redis record failure: %v", err)
	}
}

func (g *redisGuard) ResetLoginFailures(ip, email string) {
	ctx, cancel := context.WithTimeout(g.ctx, time.Second)
	defer cancel()
	if err := g.rdb.Del(ctx, loginKeys(ip, email)...).Err(); err != nil {
		log.Printf("auth guard: redis reset failures: %v", err)
	}
}

func (g *redisGuard) DenyToken(jti string, ttl time.Duration) {
	if jti == "" {
		return
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	ctx, cancel := context.WithTimeout(g.ctx, time.Second)
	defer cancel()
	if err := g.rdb.Set(ctx, keyDenyJTI+jti, 1, ttl).Err(); err != nil {
		log.Printf("auth guard: redis deny token: %v", err)
	}
}

func (g *redisGuard) TokenDenied(jti string) bool {
	if jti == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(g.ctx, time.Second)
	defer cancel()
	n, err := g.rdb.Exists(ctx, keyDenyJTI+jti).Result()
	return err == nil && n > 0
}

func (g *redisGuard) InvalidateUser(userID int64, at time.Time) {
	ctx, cancel := context.WithTimeout(g.ctx, time.Second)
	defer cancel()
	key := keyValidAfter + strconv.FormatInt(userID, 10)
	if err := g.rdb.Set(ctx, key, at.Unix(), 0).Err(); err != nil {
		log.Printf("auth guard: redis invalidate user: %v", err)
	}
}

func (g *redisGuard) UserInvalidBefore(userID int64) time.Time {
	ctx, cancel := context.WithTimeout(g.ctx, time.Second)
	defer cancel()
	v, err := g.rdb.Get(ctx, keyValidAfter+strconv.FormatInt(userID, 10)).Int64()
	if err != nil {
		return time.Time{}
	}
	return time.Unix(v, 0)
}

// --- Memorijski backend (fallback i testovi) ---

type memoryGuard struct {
	mu      sync.Mutex
	now     func() time.Time
	fails   map[string]memoryFail
	denied  map[string]time.Time
	invalid map[int64]time.Time
}

type memoryFail struct {
	count   int
	expires time.Time
}

func newMemoryGuard() *memoryGuard {
	return &memoryGuard{
		now:     time.Now,
		fails:   map[string]memoryFail{},
		denied:  map[string]time.Time{},
		invalid: map[int64]time.Time{},
	}
}

func (g *memoryGuard) LoginBlocked(ip, email string) (bool, time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	for _, k := range loginKeys(ip, email) {
		f, ok := g.fails[k]
		if !ok {
			continue
		}
		if now.After(f.expires) {
			delete(g.fails, k)
			continue
		}
		if f.count >= loginMaxFailures {
			return true, f.expires.Sub(now)
		}
	}
	return false, 0
}

func (g *memoryGuard) RecordLoginFailure(ip, email string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	for _, k := range loginKeys(ip, email) {
		f := g.fails[k]
		if now.After(f.expires) {
			f = memoryFail{}
		}
		f.count++
		f.expires = now.Add(loginWindow)
		g.fails[k] = f
	}
}

func (g *memoryGuard) ResetLoginFailures(ip, email string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, k := range loginKeys(ip, email) {
		delete(g.fails, k)
	}
}

func (g *memoryGuard) DenyToken(jti string, ttl time.Duration) {
	if jti == "" {
		return
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.denied[jti] = g.now().Add(ttl)
}

func (g *memoryGuard) TokenDenied(jti string) bool {
	if jti == "" {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	exp, ok := g.denied[jti]
	if !ok {
		return false
	}
	if g.now().After(exp) {
		delete(g.denied, jti)
		return false
	}
	return true
}

func (g *memoryGuard) InvalidateUser(userID int64, at time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.invalid[userID] = at
}

func (g *memoryGuard) UserInvalidBefore(userID int64) time.Time {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.invalid[userID]
}

// --- init ---

// authGuardState je globalni guard; nil znači "nema zaštite" (samo ako
// Redis nije dostupan i fallback je isključen — u praksi se nikad ne dešava).
var authGuardState authGuard

// initAuthGuard pravi Redis backend ako je `REDIS_ADDR` postavljen i dostupan,
// inače se vraća na procesnu memoriju.
func initAuthGuard() authGuard {
	addr := strings.TrimSpace(os.Getenv("REDIS_ADDR"))
	if addr == "" {
		log.Println("auth guard: REDIS_ADDR not set — using in-memory state (single pod only)")
		return newMemoryGuard()
	}
	db := 0
	if v := strings.TrimSpace(os.Getenv("REDIS_DB")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			db = n
		}
	}
	g := newRedisGuard(addr, os.Getenv("REDIS_PASSWORD"), db)
	if err := g.ping(); err != nil {
		log.Printf("auth guard: Redis %s unavailable (%v) — falling back to memory", addr, err)
		return newMemoryGuard()
	}
	log.Printf("auth guard: Redis %s (db %d)", addr, db)
	return g
}

// --- Pomoćne funkcije koje koristi auth sloj ---

// loginBlocked vraća da li je par (IP, email) trenutno blokiran.
func loginBlocked(ip, email string) (bool, time.Duration) {
	if authGuardState == nil {
		return false, 0
	}
	return authGuardState.LoginBlocked(ip, email)
}

func recordLoginFailure(ip, email string) {
	if authGuardState == nil {
		return
	}
	authGuardState.RecordLoginFailure(ip, email)
}

func resetLoginFailures(ip, email string) {
	if authGuardState == nil {
		return
	}
	authGuardState.ResetLoginFailures(ip, email)
}

// denySession opoziva jedan JWT do njegovog `exp` (logout).
func denySession(claims *AuthClaims) {
	if authGuardState == nil || claims == nil {
		return
	}
	ttl := 24 * time.Hour
	if claims.ExpiresAt != nil {
		ttl = time.Until(claims.ExpiresAt.Time)
	}
	authGuardState.DenyToken(claims.ID, ttl)
}

// invalidateUserSessions opoziva sve sesije korisnika izdate pre sada.
// Koristi se posle promene lozinke (sopstvene ili od strane admina).
func invalidateUserSessions(userID int64) {
	if authGuardState == nil || userID == 0 {
		return
	}
	authGuardState.InvalidateUser(userID, time.Now().Truncate(time.Second))
}

// sessionStillValid proverava opozive za token iz kolačića.
func sessionStillValid(claims *AuthClaims) bool {
	if authGuardState == nil || claims == nil {
		return true
	}
	if authGuardState.TokenDenied(claims.ID) {
		return false
	}
	if before := authGuardState.UserInvalidBefore(claims.UserID); !before.IsZero() {
		if claims.IssuedAt == nil || claims.IssuedAt.Time.Before(before) {
			return false
		}
	}
	return true
}
