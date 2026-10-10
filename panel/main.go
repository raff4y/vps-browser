// Command panel is the control panel for vps-browser: the first-run setup
// wizard, the sign-in page, the settings dashboard, and the auth check that
// Caddy runs in front of the browser.
package main

import (
	"embed"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const version = "1.0.0"

//go:embed web
var webFS embed.FS

type App struct {
	store       *Store
	tmpl        *template.Template
	limiter     *Limiter
	publicIP    *PublicIP
	certs       *CertChecker
	tlsMode     string // "acme" (Let's Encrypt), "internal" (self-signed, local testing) or "proxy" (HTTPS done by e.g. Coolify)
	policiesDir string // Chromium "recommended" policies, shared with the browser container
	controlDir  string // restart requests, shared with the browser container
}

func main() {
	log.SetFlags(0)
	store, err := OpenStore(filepath.Join(envOr("DATA_DIR", "/data"), "config.json"))
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "serve":
		serve(store)
	case "setup-url":
		printSetupURL(store)
	case "reset":
		err := store.Update(func(c *Config) error {
			c.Configured = false
			c.SetupToken = randomToken()
			c.SessionVersion++
			return nil
		})
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("Setup has been re-opened and everyone is signed out.")
		fmt.Println("Your browser profile (bookmarks, logins) is kept.")
		printSetupURL(store)
	case "version":
		fmt.Println(version)
	default:
		fmt.Fprintln(os.Stderr, "usage: panel [serve | setup-url | reset | version]")
		os.Exit(2)
	}
}

func serve(store *Store) {
	tlsMode := envOr("TLS_MODE", "acme")
	app := &App{
		store:       store,
		tmpl:        template.Must(template.New("").ParseFS(webFS, "web/templates/*.html")),
		limiter:     NewLimiter(),
		publicIP:    &PublicIP{override: os.Getenv("PUBLIC_IP")},
		certs:       &CertChecker{addr: envOr("CADDY_TLS_ADDR", "caddy:443"), insecure: tlsMode == "internal"},
		tlsMode:     tlsMode,
		policiesDir: envOr("POLICIES_DIR", "/policies"),
		controlDir:  envOr("CONTROL_DIR", "/control"),
	}
	if err := app.writePolicy(store.Get()); err != nil {
		log.Printf("warning: could not write browser policy: %v", err)
	}

	if cfg := store.Get(); !cfg.Configured {
		go func() {
			log.Printf("Not set up yet. Open the setup page: %s", setupURL(tlsMode, app.publicIP.Get(), cfg.SetupToken))
		}()
	}

	// Caddy asks this (internal-only) port before requesting an HTTPS certificate.
	internal := http.NewServeMux()
	internal.HandleFunc("GET /tls-allowed", app.tlsAllowed)
	go func() { log.Fatal(http.ListenAndServe(":8081", internal)) }()

	srv := &http.Server{
		Addr:              ":8080",
		Handler:           app.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("vps-browser panel %s listening on :8080 (TLS mode: %s)", version, tlsMode)
	log.Fatal(srv.ListenAndServe())
}

func setupURL(tlsMode, ip, token string) string {
	if tlsMode == "proxy" {
		return "https://YOUR-DOMAIN/_vb/setup?token=" + token
	}
	if ip == "" {
		ip = "YOUR-SERVER-IP"
	}
	return fmt.Sprintf("http://%s/_vb/setup?token=%s", ip, token)
}

func printSetupURL(store *Store) {
	cfg := store.Get()
	if cfg.Configured {
		fmt.Printf("Already set up. Sign in at https://%s/\n", cfg.Domain)
		fmt.Println("To run setup again (e.g. forgotten password): vps-browser reset")
		return
	}
	tlsMode := envOr("TLS_MODE", "acme")
	ip := ""
	if tlsMode != "proxy" {
		ip = (&PublicIP{override: os.Getenv("PUBLIC_IP")}).Get()
	}
	fmt.Println(setupURL(tlsMode, ip, cfg.SetupToken))
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
