package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PublicIP finds the server's public IPv4 address (cached for 10 minutes).
type PublicIP struct {
	override string
	mu       sync.Mutex
	ip       string
	at       time.Time
}

var ipServices = []string{"https://api.ipify.org", "https://ipv4.icanhazip.com", "https://ifconfig.me/ip"}

func (p *PublicIP) Get() string {
	if p.override != "" {
		return p.override
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ip != "" && time.Since(p.at) < 10*time.Minute {
		return p.ip
	}
	client := &http.Client{Timeout: 4 * time.Second}
	for _, u := range ipServices {
		resp, err := client.Get(u)
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
		resp.Body.Close()
		ip := net.ParseIP(strings.TrimSpace(string(b)))
		if resp.StatusCode == 200 && ip != nil && ip.To4() != nil {
			p.ip, p.at = ip.String(), time.Now()
			return p.ip
		}
	}
	return p.ip
}

type DNSResult struct {
	Domain   string   `json:"domain"`
	Status   string   `json:"status"` // ok | mismatch | missing | invalid
	IPv4     []string `json:"ipv4"`
	IPv6     []string `json:"ipv6"`
	PublicIP string   `json:"publicIP"`
	Message  string   `json:"message"`
}

func checkDNS(domain, publicIP string) DNSResult {
	res := DNSResult{Domain: domain, PublicIP: publicIP, IPv4: []string{}, IPv6: []string{}}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, domain)
	if err != nil || len(addrs) == 0 {
		res.Status = "missing"
		res.Message = "No DNS record found yet. Add the A record below. New records can take a few minutes to appear."
		return res
	}
	match := false
	for _, a := range addrs {
		if v4 := a.IP.To4(); v4 != nil {
			res.IPv4 = append(res.IPv4, v4.String())
			if v4.String() == publicIP {
				match = true
			}
		} else {
			res.IPv6 = append(res.IPv6, a.IP.String())
		}
	}
	switch {
	case match && len(res.IPv6) > 0:
		res.Status = "ok"
		res.Message = "Points to this server. It also has an IPv6 (AAAA) record: make sure that points here too, or delete it, or HTTPS may fail."
	case match:
		res.Status = "ok"
		res.Message = "Points to this server."
	case publicIP == "":
		res.Status = "mismatch"
		res.Message = "Couldn't detect this server's public IP to compare. Make sure the record points to this server."
	default:
		res.Status = "mismatch"
		res.Message = fmt.Sprintf("Points to %s, but this server is %s. Update the A record. If you use Cloudflare, set the record to \"DNS only\" (grey cloud).",
			strings.Join(append(res.IPv4, res.IPv6...), ", "), publicIP)
	}
	return res
}

// CertChecker connects to Caddy with the domain's name, which makes Caddy
// obtain the certificate if needed, and reports whether HTTPS works.
type CertChecker struct {
	addr     string
	insecure bool
	mu       sync.Mutex
	domain   string
	at       time.Time
	ready    bool
	msg      string
}

func (c *CertChecker) Check(domain string) (bool, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.domain == domain && time.Since(c.at) < 3*time.Second {
		return c.ready, c.msg
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 40 * time.Second}, "tcp", c.addr,
		&tls.Config{ServerName: domain, InsecureSkipVerify: c.insecure})
	c.domain, c.at = domain, time.Now()
	if err != nil {
		c.ready, c.msg = false, "Waiting for the HTTPS certificate…"
		return c.ready, c.msg
	}
	conn.Close()
	c.ready, c.msg = true, "HTTPS is ready."
	return c.ready, c.msg
}

var (
	domainRe   = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$`)
	usernameRe = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,64}$`)
)

func normalizeDomain(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if h, _, err := net.SplitHostPort(s); err == nil {
		s = h
	}
	return strings.TrimSuffix(s, ".")
}

func (a *App) validDomain(d string) bool {
	if a.tlsMode == "proxy" { // whatever host the outer proxy routes to us
		return d != "" && len(d) <= 253
	}
	if a.tlsMode == "internal" && d == "localhost" {
		return true
	}
	return len(d) <= 253 && domainRe.MatchString(d)
}

func normalizeURL(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(s) > 2048 {
		return "", false
	}
	return u.String(), true
}

func validPassword(pw string) string {
	switch {
	case len(pw) < 10:
		return "Password must be at least 10 characters."
	case len(pw) > 72:
		return "Password must be at most 72 characters."
	}
	return ""
}

// writePolicy sets the browser's start page through Chromium's
// "recommended" policies: defaults the user can still change in Chromium.
func (a *App) writePolicy(cfg Config) error {
	restore := 4 // open RestoreOnStartupURLs
	if cfg.RestoreSession {
		restore = 1 // continue where you left off
	}
	policy := map[string]any{
		"HomepageLocation":     cfg.StartPage,
		"HomepageIsNewTabPage": false,
		"ShowHomeButton":       true,
		"RestoreOnStartup":     restore,
		"RestoreOnStartupURLs": []string{cfg.StartPage},
	}
	b, _ := json.MarshalIndent(policy, "", "  ")
	tmp := filepath.Join(a.policiesDir, ".vps-browser.json.tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(a.policiesDir, "vps-browser.json"))
}

// requestBrowserRestart is picked up by a watcher inside the browser
// container, which stops Chromium; the image's watchdog starts it again.
func (a *App) requestBrowserRestart() error {
	return os.WriteFile(filepath.Join(a.controlDir, "restart"),
		[]byte(strconv.FormatInt(time.Now().UnixNano(), 10)), 0o644)
}

// While this file exists the browser is "closed": the watcher inside the
// browser container stops Chromium, and chromium-gate keeps it from starting
// again, so it uses no memory until someone opens the browser.
func (a *App) stoppedFlag() string { return filepath.Join(a.controlDir, "stopped") }

func (a *App) closeBrowser() error {
	return os.WriteFile(a.stoppedFlag(), []byte(time.Now().UTC().Format(time.RFC3339)), 0o644)
}

func (a *App) openBrowser() error {
	if err := os.Remove(a.stoppedFlag()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (a *App) browserClosed() bool {
	_, err := os.Stat(a.stoppedFlag())
	return err == nil
}

type SystemInfo struct {
	PublicIP string  `json:"publicIP"`
	MemoryGB float64 `json:"memoryGB"`
	CPUs     int     `json:"cpus"`
	Version  string  `json:"version"`
	TLSMode  string  `json:"tlsMode"`
}

func (a *App) systemInfo() SystemInfo {
	return SystemInfo{
		PublicIP: a.publicIP.Get(),
		MemoryGB: memTotalGB(),
		CPUs:     runtime.NumCPU(),
		Version:  version,
		TLSMode:  a.tlsMode,
	}
}

func memTotalGB() float64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			kb, _ := strconv.ParseFloat(fields[1], 64)
			return float64(int(kb/1024/1024*10+0.5)) / 10
		}
	}
	return 0
}
