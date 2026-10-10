package main

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type LoginRecord struct {
	Time  time.Time `json:"time"`
	IP    string    `json:"ip"`
	Agent string    `json:"agent"`
}

// Config is everything the panel remembers. It lives in /data/config.json.
type Config struct {
	Configured     bool          `json:"configured"`
	Domain         string        `json:"domain"`
	Username       string        `json:"username"`
	PasswordHash   string        `json:"password_hash"`
	SessionSecret  string        `json:"session_secret"`
	SessionVersion int           `json:"session_version"` // bump to sign everyone out
	StartPage      string        `json:"start_page"`
	RestoreSession bool          `json:"restore_session"`
	SetupToken     string        `json:"setup_token,omitempty"`
	RecentLogins   []LoginRecord `json:"recent_logins,omitempty"`
}

// Store keeps Config on disk. It re-reads the file when it changes, so CLI
// commands (`panel reset`) take effect in the running server.
type Store struct {
	path    string
	mu      sync.Mutex
	cfg     Config
	modTime time.Time
}

func OpenStore(path string) (*Store, error) {
	s := &Store{path: path}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		s.cfg = Config{
			SessionSecret: randomHex(32),
			SetupToken:    randomToken(),
			StartPage:     "https://www.google.com",
		}
		return s, s.save()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.load(); err != nil {
		return nil, err
	}
	if s.cfg.SessionSecret == "" || (!s.cfg.Configured && s.cfg.SetupToken == "") {
		if s.cfg.SessionSecret == "" {
			s.cfg.SessionSecret = randomHex(32)
		}
		if !s.cfg.Configured && s.cfg.SetupToken == "" {
			s.cfg.SetupToken = randomToken()
		}
		return s, s.save()
	}
	return s, nil
}

func (s *Store) Get() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfChanged()
	c := s.cfg
	c.RecentLogins = append([]LoginRecord(nil), s.cfg.RecentLogins...)
	return c
}

// Update applies fn to a copy of the config and saves it if fn succeeds.
func (s *Store) Update(fn func(*Config) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfChanged()
	c := s.cfg
	c.RecentLogins = append([]LoginRecord(nil), s.cfg.RecentLogins...)
	if err := fn(&c); err != nil {
		return err
	}
	old := s.cfg
	s.cfg = c
	if err := s.save(); err != nil {
		s.cfg = old
		return err
	}
	return nil
}

func (s *Store) reloadIfChanged() {
	st, err := os.Stat(s.path)
	if err == nil && !st.ModTime().Equal(s.modTime) {
		_ = s.load()
	}
}

func (s *Store) load() error {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return err
	}
	s.cfg = c
	if st, err := os.Stat(s.path); err == nil {
		s.modTime = st.ModTime()
	}
	return nil
}

func (s *Store) save() error {
	b, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	if st, err := os.Stat(s.path); err == nil {
		s.modTime = st.ModTime()
	}
	return nil
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

func randomHex(n int) string { return hex.EncodeToString(randomBytes(n)) }

func randomToken() string {
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(randomBytes(20)))
}
