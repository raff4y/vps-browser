package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookie = "vb_session"
	setupCookie   = "vb_setup"
	sessionTTL    = 30 * 24 * time.Hour
	bcryptCost    = 12
)

// A session cookie is "payload.signature", where payload is
// base64(username)|session version|expiry, signed with HMAC-SHA256.
func signSession(cfg Config, exp time.Time) string {
	payload := fmt.Sprintf("%s|%d|%d",
		base64.RawURLEncoding.EncodeToString([]byte(cfg.Username)), cfg.SessionVersion, exp.Unix())
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + sessionMAC(cfg, payload)
}

func sessionMAC(cfg Config, payload string) string {
	m := hmac.New(sha256.New, []byte(cfg.SessionSecret))
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func validSession(r *http.Request, cfg Config) bool {
	if !cfg.Configured {
		return false
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	enc, sig, ok := strings.Cut(c.Value, ".")
	if !ok {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return false
	}
	payload := string(raw)
	if !hmac.Equal([]byte(sig), []byte(sessionMAC(cfg, payload))) {
		return false
	}
	parts := strings.Split(payload, "|")
	if len(parts) != 3 {
		return false
	}
	user, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || string(user) != cfg.Username {
		return false
	}
	ver, err1 := strconv.Atoi(parts[1])
	exp, err2 := strconv.ParseInt(parts[2], 10, 64)
	return err1 == nil && err2 == nil && ver == cfg.SessionVersion && time.Now().Unix() < exp
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, cfg Config) {
	exp := time.Now().Add(sessionTTL)
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: signSession(cfg, exp), Path: "/", Expires: exp,
		HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode,
	})
}

func clearCookie(w http.ResponseWriter, name, path string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: path, MaxAge: -1, HttpOnly: true})
}

func hashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcryptCost)
	return string(h), err
}

// dummyHash makes a wrong username cost as much time as a wrong password.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("vps-browser-timing"), bcryptCost)

func checkCredentials(cfg Config, user, pw string) bool {
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(cfg.Username)) == 1
	hash := []byte(cfg.PasswordHash)
	if !userOK || len(hash) == 0 {
		hash = dummyHash
	}
	pwOK := bcrypt.CompareHashAndPassword(hash, []byte(pw)) == nil
	return userOK && pwOK && cfg.PasswordHash != ""
}

func checkPassword(cfg Config, pw string) bool {
	return cfg.PasswordHash != "" && bcrypt.CompareHashAndPassword([]byte(cfg.PasswordHash), []byte(pw)) == nil
}

func validSetup(r *http.Request, cfg Config) bool {
	if cfg.Configured || cfg.SetupToken == "" {
		return false
	}
	c, err := r.Cookie(setupCookie)
	return err == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(cfg.SetupToken)) == 1
}

// canSetup: setup isn't finished, and either the request has the setup link
// or we're still in the open window right after a fresh install.
func (a *App) canSetup(r *http.Request, cfg Config) bool {
	if cfg.Configured {
		return false
	}
	return validSetup(r, cfg) || time.Now().Before(a.openSetupUntil)
}

// Limiter locks out an IP after repeated failed sign-ins: 5 failures lock it
// for 1 minute, and each further failure doubles the lock, up to 1 hour.
type Limiter struct {
	mu sync.Mutex
	m  map[string]*attempts
}

type attempts struct {
	fails       int
	lockedUntil time.Time
	last        time.Time
}

func NewLimiter() *Limiter { return &Limiter{m: map[string]*attempts{}} }

func (l *Limiter) Wait(ip string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if a := l.m[ip]; a != nil {
		if d := time.Until(a.lockedUntil); d > 0 {
			return d
		}
	}
	return 0
}

func (l *Limiter) Fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.m) > 10000 {
		for k, a := range l.m {
			if now.Sub(a.last) > 24*time.Hour {
				delete(l.m, k)
			}
		}
	}
	a := l.m[ip]
	if a == nil || now.Sub(a.last) > 24*time.Hour {
		a = &attempts{}
		l.m[ip] = a
	}
	a.fails++
	a.last = now
	if a.fails >= 5 {
		lock := time.Minute << min(a.fails-5, 6)
		a.lockedUntil = now.Add(min(lock, time.Hour))
	}
}

func (l *Limiter) Reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, ip)
}

// clientIP trusts X-Real-IP / X-Forwarded-For because the panel is only
// reachable through Caddy, which sets X-Real-IP to the real client address
// (see the Caddyfiles).
func clientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[len(parts)-1])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func isHTTPS(r *http.Request) bool { return r.Header.Get("X-Forwarded-Proto") == "https" }

func hostOnly(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

// sameOrigin is the CSRF check for every POST.
func sameOrigin(r *http.Request) bool {
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	want := scheme + "://" + r.Host
	if o := r.Header.Get("Origin"); o != "" {
		return o == want
	}
	ref := r.Header.Get("Referer")
	return ref == want || strings.HasPrefix(ref, want+"/")
}
