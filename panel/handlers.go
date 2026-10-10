package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(webFS, "web/static")
	mux.Handle("GET /_vb/static/", http.StripPrefix("/_vb/static/", http.FileServerFS(static)))

	mux.HandleFunc("GET /_vb/auth/check", a.authCheck)
	mux.HandleFunc("GET /_vb/login", a.loginPage)
	mux.HandleFunc("POST /_vb/login", a.loginSubmit)
	mux.HandleFunc("POST /_vb/logout", a.logout)

	mux.HandleFunc("GET /_vb/setup", a.setupPage)
	mux.HandleFunc("GET /_vb/api/setup/info", a.requireSetup(a.setupInfo))
	mux.HandleFunc("POST /_vb/api/setup", a.requireSetup(a.setupSubmit))
	mux.HandleFunc("POST /_vb/api/dns-check", a.requireSetupOrSession(a.dnsCheck))
	mux.HandleFunc("GET /_vb/api/cert-status", a.certStatus)

	mux.HandleFunc("GET /_vb", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/_vb/", http.StatusFound) })
	mux.HandleFunc("GET /_vb/{$}", a.requireSession(a.dashboard))
	mux.HandleFunc("GET /_vb/api/settings", a.requireSession(a.getSettings))
	mux.HandleFunc("POST /_vb/api/settings/domain", a.requireSession(a.saveDomain))
	mux.HandleFunc("POST /_vb/api/settings/account", a.requireSession(a.saveAccount))
	mux.HandleFunc("POST /_vb/api/settings/browser", a.requireSession(a.saveBrowser))
	mux.HandleFunc("POST /_vb/api/browser/restart", a.requireSession(a.restartBrowser))
	mux.HandleFunc("POST /_vb/api/sessions/revoke", a.requireSession(a.revokeSessions))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { a.renderError(w, http.StatusNotFound, "Page not found") })
	return a.middleware(mux)
}

func (a *App) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		if !strings.HasPrefix(r.URL.Path, "/_vb/static/") {
			h.Set("Cache-Control", "no-store")
		}

		path := r.URL.Path
		if path == "/_vb/auth/check" { // Caddy's forward_auth: has its own rules
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodPost && !sameOrigin(r) {
			jsonError(w, http.StatusForbidden, "Request blocked (cross-site).")
			return
		}

		cfg := a.store.Get()
		if a.tlsMode == "proxy" {
			// HTTPS and the domain are handled by the outer proxy (e.g. Coolify).
		} else if !isHTTPS(r) {
			// Plain HTTP (http://SERVER-IP) is only for first-time setup and
			// the "waiting for HTTPS" check right after it.
			if cfg.Configured {
				if path == "/_vb/api/cert-status" || strings.HasPrefix(path, "/_vb/static/") {
					next.ServeHTTP(w, r)
					return
				}
				http.Redirect(w, r, "https://"+cfg.Domain+r.URL.RequestURI(), http.StatusFound)
				return
			}
			if !strings.HasPrefix(path, "/_vb/") {
				http.Redirect(w, r, "/_vb/setup", http.StatusFound)
				return
			}
		} else if cfg.Configured && hostOnly(r.Host) != cfg.Domain {
			// An old domain whose certificate Caddy still has: send people to the current one.
			http.Redirect(w, r, "https://"+cfg.Domain+r.URL.RequestURI(), http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authCheck is called by Caddy (forward_auth) before every request to the
// browser. 2xx lets the request through; anything else is sent to the client.
func (a *App) authCheck(w http.ResponseWriter, r *http.Request) {
	cfg := a.store.Get()
	uri := r.Header.Get("X-Forwarded-Uri")
	if uri == "" || !strings.HasPrefix(uri, "/") {
		uri = "/"
	}
	method := r.Header.Get("X-Forwarded-Method")
	wantsPage := (method == "" || method == http.MethodGet) && strings.Contains(r.Header.Get("Accept"), "text/html")
	deny := func(location string) {
		if wantsPage {
			http.Redirect(w, r, location, http.StatusFound)
			return
		}
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}
	switch {
	case !cfg.Configured:
		deny("/_vb/setup")
	case a.tlsMode != "proxy" && hostOnly(r.Host) != cfg.Domain:
		deny("https://" + cfg.Domain + uri)
	case validSession(r, cfg):
		w.WriteHeader(http.StatusOK)
	default:
		deny("/_vb/login?next=" + url.QueryEscape(uri))
	}
}

// tlsAllowed is Caddy's on-demand TLS "ask" endpoint: only the configured
// domain may get a certificate.
func (a *App) tlsAllowed(w http.ResponseWriter, r *http.Request) {
	cfg := a.store.Get()
	if d := normalizeDomain(r.URL.Query().Get("domain")); cfg.Domain != "" && d == cfg.Domain {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusForbidden)
}

// ---- Sign in / out ----

type loginData struct {
	Version  string
	Domain   string
	Next     string
	Username string
	Error    string
}

func (a *App) loginPage(w http.ResponseWriter, r *http.Request) {
	cfg := a.store.Get()
	if !cfg.Configured {
		http.Redirect(w, r, "/_vb/setup", http.StatusFound)
		return
	}
	next := safeNext(r.URL.Query().Get("next"))
	if validSession(r, cfg) {
		http.Redirect(w, r, next, http.StatusFound)
		return
	}
	a.render(w, http.StatusOK, "login.html", loginData{Version: version, Domain: cfg.Domain, Next: next})
}

func (a *App) loginSubmit(w http.ResponseWriter, r *http.Request) {
	cfg := a.store.Get()
	if !cfg.Configured {
		http.Redirect(w, r, "/_vb/setup", http.StatusFound)
		return
	}
	_ = r.ParseForm()
	user := strings.TrimSpace(r.PostForm.Get("username"))
	data := loginData{Version: version, Domain: cfg.Domain, Next: safeNext(r.PostForm.Get("next")), Username: user}
	ip := clientIP(r)

	if wait := a.limiter.Wait(ip); wait > 0 {
		data.Error = "Too many failed attempts. Try again in " + humanDuration(wait) + "."
		a.render(w, http.StatusTooManyRequests, "login.html", data)
		return
	}
	if !checkCredentials(cfg, user, r.PostForm.Get("password")) {
		a.limiter.Fail(ip)
		log.Printf("failed sign-in from %s", ip)
		data.Error = "Wrong username or password."
		if wait := a.limiter.Wait(ip); wait > 0 {
			data.Error = "Too many failed attempts. Try again in " + humanDuration(wait) + "."
		}
		a.render(w, http.StatusUnauthorized, "login.html", data)
		return
	}
	a.limiter.Reset(ip)
	_ = a.store.Update(func(c *Config) error {
		rec := LoginRecord{Time: time.Now().UTC(), IP: ip, Agent: truncate(r.UserAgent(), 200)}
		c.RecentLogins = append([]LoginRecord{rec}, c.RecentLogins...)
		if len(c.RecentLogins) > 8 {
			c.RecentLogins = c.RecentLogins[:8]
		}
		return nil
	})
	setSessionCookie(w, r, cfg)
	http.Redirect(w, r, data.Next, http.StatusSeeOther)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	clearCookie(w, sessionCookie, "/")
	http.Redirect(w, r, "/_vb/login", http.StatusSeeOther)
}

// ---- First-run setup ----

type setupLockedData struct {
	Version string
	Error   string
}

func (a *App) setupPage(w http.ResponseWriter, r *http.Request) {
	cfg := a.store.Get()
	if cfg.Configured {
		http.Redirect(w, r, "/_vb/", http.StatusFound)
		return
	}
	if tok := r.URL.Query().Get("token"); tok != "" {
		if tok != cfg.SetupToken {
			a.render(w, http.StatusForbidden, "setup_locked.html", setupLockedData{Version: version,
				Error: "This setup link isn't valid. It may be from an earlier install."})
			return
		}
		http.SetCookie(w, &http.Cookie{Name: setupCookie, Value: tok, Path: "/_vb/",
			HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode, MaxAge: 24 * 3600})
		http.Redirect(w, r, "/_vb/setup", http.StatusFound) // drop the token from the address bar
		return
	}
	if !validSetup(r, cfg) {
		a.render(w, http.StatusForbidden, "setup_locked.html", setupLockedData{Version: version})
		return
	}
	a.render(w, http.StatusOK, "setup.html", struct{ Version string }{version})
}

func (a *App) setupInfo(w http.ResponseWriter, r *http.Request) {
	cfg := a.store.Get()
	writeJSON(w, map[string]any{
		"system":   a.systemInfo(),
		"domain":   cfg.Domain,
		"username": cfg.Username,
	})
}

func (a *App) dnsCheck(w http.ResponseWriter, r *http.Request) {
	var in struct{ Domain string }
	if !readJSON(w, r, &in) {
		return
	}
	d := normalizeDomain(in.Domain)
	if !a.validDomain(d) {
		writeJSON(w, DNSResult{Domain: d, Status: "invalid", IPv4: []string{}, IPv6: []string{},
			Message: "That doesn't look like a domain. Use something like browse.example.com (no http://, no slashes)."})
		return
	}
	writeJSON(w, checkDNS(d, a.publicIP.Get()))
}

func (a *App) setupSubmit(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Domain, Username, Password, StartPage string
		RestoreSession                        bool
	}
	if !readJSON(w, r, &in) {
		return
	}
	domain := normalizeDomain(in.Domain)
	if a.tlsMode == "proxy" {
		domain = hostOnly(r.Host)
	}
	if !a.validDomain(domain) {
		jsonError(w, http.StatusBadRequest, "Please enter a valid domain.")
		return
	}
	if !usernameRe.MatchString(in.Username) {
		jsonError(w, http.StatusBadRequest, "Username can use letters, numbers and . _ @ - (up to 64).")
		return
	}
	if msg := validPassword(in.Password); msg != "" {
		jsonError(w, http.StatusBadRequest, msg)
		return
	}
	start, ok := normalizeURL(in.StartPage)
	if !ok {
		jsonError(w, http.StatusBadRequest, "Start page must be a web address like https://www.google.com.")
		return
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "Could not save the password.")
		return
	}
	err = a.store.Update(func(c *Config) error {
		if c.Configured {
			return fmt.Errorf("already set up")
		}
		c.Configured = true
		c.Domain = domain
		c.Username = in.Username
		c.PasswordHash = hash
		c.StartPage = start
		c.RestoreSession = in.RestoreSession
		c.SetupToken = ""
		c.SessionVersion++
		return nil
	})
	if err != nil {
		jsonError(w, http.StatusConflict, "Setup was already completed.")
		return
	}
	if err := a.writePolicy(a.store.Get()); err != nil {
		log.Printf("warning: could not write browser policy: %v", err)
	}
	clearCookie(w, setupCookie, "/_vb/")
	log.Printf("setup complete for %s", domain)
	url := "https://" + domain + "/"
	if a.tlsMode == "proxy" {
		url = "/"
	}
	writeJSON(w, map[string]any{"ok": true, "domain": domain, "url": url})
}

func (a *App) certStatus(w http.ResponseWriter, r *http.Request) {
	cfg := a.store.Get()
	if !cfg.Configured {
		writeJSON(w, map[string]any{"ready": false, "message": "Not set up yet."})
		return
	}
	if a.tlsMode == "proxy" {
		writeJSON(w, map[string]any{"ready": true, "domain": cfg.Domain, "message": "HTTPS is handled by your proxy."})
		return
	}
	ready, msg := a.certs.Check(cfg.Domain)
	writeJSON(w, map[string]any{"ready": ready, "domain": cfg.Domain, "message": msg})
}

// ---- Dashboard & settings ----

func (a *App) dashboard(w http.ResponseWriter, r *http.Request) {
	cfg := a.store.Get()
	a.render(w, http.StatusOK, "dashboard.html", struct {
		Version, Domain, Username string
	}{version, cfg.Domain, cfg.Username})
}

func (a *App) getSettings(w http.ResponseWriter, r *http.Request) {
	cfg := a.store.Get()
	writeJSON(w, map[string]any{
		"domain":         cfg.Domain,
		"username":       cfg.Username,
		"startPage":      cfg.StartPage,
		"restoreSession": cfg.RestoreSession,
		"recentLogins":   cfg.RecentLogins,
		"yourIP":         clientIP(r),
		"system":         a.systemInfo(),
	})
}

func (a *App) saveDomain(w http.ResponseWriter, r *http.Request) {
	if a.tlsMode == "proxy" {
		jsonError(w, http.StatusBadRequest, "The domain is managed by your proxy (e.g. Coolify). Change it there.")
		return
	}
	var in struct{ Domain string }
	if !readJSON(w, r, &in) {
		return
	}
	d := normalizeDomain(in.Domain)
	if !a.validDomain(d) {
		jsonError(w, http.StatusBadRequest, "Please enter a valid domain.")
		return
	}
	if err := a.store.Update(func(c *Config) error { c.Domain = d; return nil }); err != nil {
		jsonError(w, http.StatusInternalServerError, "Could not save.")
		return
	}
	log.Printf("domain changed to %s", d)
	writeJSON(w, map[string]any{"ok": true, "url": "https://" + d + "/_vb/"})
}

func (a *App) saveAccount(w http.ResponseWriter, r *http.Request) {
	var in struct{ CurrentPassword, Username, NewPassword string }
	if !readJSON(w, r, &in) {
		return
	}
	cfg := a.store.Get()
	if !checkPassword(cfg, in.CurrentPassword) {
		jsonError(w, http.StatusForbidden, "Current password is wrong.")
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	if !usernameRe.MatchString(in.Username) {
		jsonError(w, http.StatusBadRequest, "Username can use letters, numbers and . _ @ - (up to 64).")
		return
	}
	var hash string
	if in.NewPassword != "" {
		if msg := validPassword(in.NewPassword); msg != "" {
			jsonError(w, http.StatusBadRequest, msg)
			return
		}
		var err error
		if hash, err = hashPassword(in.NewPassword); err != nil {
			jsonError(w, http.StatusInternalServerError, "Could not save the password.")
			return
		}
	}
	err := a.store.Update(func(c *Config) error {
		c.Username = in.Username
		if hash != "" {
			c.PasswordHash = hash
		}
		c.SessionVersion++ // signs out other devices
		return nil
	})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "Could not save.")
		return
	}
	setSessionCookie(w, r, a.store.Get())
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) saveBrowser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		StartPage      string
		RestoreSession bool
	}
	if !readJSON(w, r, &in) {
		return
	}
	start, ok := normalizeURL(in.StartPage)
	if !ok {
		jsonError(w, http.StatusBadRequest, "Start page must be a web address like https://www.google.com.")
		return
	}
	if err := a.store.Update(func(c *Config) error {
		c.StartPage, c.RestoreSession = start, in.RestoreSession
		return nil
	}); err != nil {
		jsonError(w, http.StatusInternalServerError, "Could not save.")
		return
	}
	if err := a.writePolicy(a.store.Get()); err != nil {
		jsonError(w, http.StatusInternalServerError, "Saved, but couldn't update the browser: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "startPage": start})
}

func (a *App) restartBrowser(w http.ResponseWriter, r *http.Request) {
	if err := a.requestBrowserRestart(); err != nil {
		jsonError(w, http.StatusInternalServerError, "Could not restart the browser: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) revokeSessions(w http.ResponseWriter, r *http.Request) {
	if err := a.store.Update(func(c *Config) error { c.SessionVersion++; return nil }); err != nil {
		jsonError(w, http.StatusInternalServerError, "Could not save.")
		return
	}
	setSessionCookie(w, r, a.store.Get())
	writeJSON(w, map[string]any{"ok": true})
}

// ---- Middleware helpers ----

func (a *App) requireSession(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if validSession(r, a.store.Get()) {
			h(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/_vb/api/") {
			jsonError(w, http.StatusUnauthorized, "Your session has ended. Please sign in again.")
			return
		}
		http.Redirect(w, r, "/_vb/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
	}
}

func (a *App) requireSetup(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !validSetup(r, a.store.Get()) {
			jsonError(w, http.StatusForbidden, "Setup link missing or expired. Run: sudo vps-browser setup-url")
			return
		}
		h(w, r)
	}
}

func (a *App) requireSetupOrSession(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := a.store.Get()
		if !validSetup(r, cfg) && !validSession(r, cfg) {
			jsonError(w, http.StatusUnauthorized, "Not signed in.")
			return
		}
		h(w, r)
	}
}

func (a *App) render(w http.ResponseWriter, status int, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := a.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template %s: %v", name, err)
	}
}

func (a *App) renderError(w http.ResponseWriter, status int, msg string) {
	a.render(w, status, "error.html", struct {
		Version, Message string
		Status           int
	}{version, msg, status})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
		jsonError(w, http.StatusBadRequest, "Invalid request.")
		return false
	}
	return true
}

func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	return next
}

func humanDuration(d time.Duration) string {
	if d < 55*time.Second {
		return fmt.Sprintf("%d seconds", int(math.Ceil(d.Seconds())))
	}
	m := int(math.Ceil(d.Minutes()))
	if m == 1 {
		return "1 minute"
	}
	return fmt.Sprintf("%d minutes", m)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
