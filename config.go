package main

import (
	"net/url"
	"os"
	"strings"
	"sync"
)

var (
	port   = envOr("PORT", ":80")
	dbFile = envOr("DB_FILE", "urls.db")
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// appConfig holds the configurable hostnames. Safe for concurrent reads/writes
// since settings can be updated live via the web UI.
type appConfig struct {
	mu            sync.RWMutex
	PublicBase    string // full URL prefix, e.g. https://pmh.codes
	PublicHost    string // hostname only,  e.g. pmh.codes
	UIHost        string // full URL, e.g. https://links.pmh.codes
	InternalHost  string // full URL, e.g. http://go
	AliasHost     string // full URL, e.g. https://pmh.so (alternate public redirect host)
	PublicAPIHost string // full URL, e.g. https://api.pmh.codes (public API endpoint)
}

var cfg = &appConfig{}

func (c *appConfig) snapshot() (publicBase, publicHost, uiHost, internalHost, aliasHost string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.PublicBase, c.PublicHost, c.UIHost, c.InternalHost, c.AliasHost
}

// publicAPIBase returns the full URL prefix for the public API host (e.g. https://api.pmh.codes).
// Returns "" when no public API host is set. Handles both full URLs and legacy bare hostnames.
func (c *appConfig) publicAPIBase() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.PublicAPIHost == "" {
		return ""
	}
	v := strings.TrimRight(c.PublicAPIHost, "/")
	if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
		return v
	}
	// Legacy bare hostname — derive scheme from PublicBase.
	u, _ := url.Parse(c.PublicBase)
	if u != nil && u.Scheme != "" {
		return u.Scheme + "://" + v
	}
	return "https://" + v
}

func (c *appConfig) publicAPIHostVal() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.PublicAPIHost
}

// aliasBase returns the full URL prefix for the alias host (e.g. https://pmh.so).
// Returns "" when no alias host is set. Handles both full URLs and legacy bare hostnames.
func (c *appConfig) aliasBase() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.AliasHost == "" {
		return ""
	}
	v := strings.TrimRight(c.AliasHost, "/")
	if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
		return v
	}
	// Legacy bare hostname — derive scheme from PublicBase.
	u, _ := url.Parse(c.PublicBase)
	if u != nil && u.Scheme != "" {
		return u.Scheme + "://" + v
	}
	return "https://" + v
}

func (c *appConfig) apply(publicBase, uiHost, internalHost, aliasHost, publicAPIHost string) {
	publicBase = strings.TrimRight(publicBase, "/")
	u, _ := url.Parse(publicBase)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.PublicBase = publicBase
	c.PublicHost = u.Hostname()
	c.UIHost = uiHost
	c.InternalHost = internalHost
	c.AliasHost = aliasHost
	c.PublicAPIHost = publicAPIHost
}

func loadSettings() error {
	publicBase := envOr("BASE_URL", "http://localhost")
	uiHost := envOr("UI_HOST", "http://links.localhost")
	internalHost := envOr("INTERNAL_HOST", "http://go")
	aliasHost := envOr("ALIAS_HOST", "")
	publicAPIHost := envOr("PUBLIC_API_HOST", "")

	rows, err := db.Query("SELECT key, value FROM settings")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return err
		}
		switch k {
		case "public_base":
			publicBase = v
		case "ui_host":
			uiHost = v
		case "internal_host":
			internalHost = v
		case "alias_host":
			aliasHost = v
		case "public_api_host":
			publicAPIHost = v
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	cfg.apply(publicBase, uiHost, internalHost, aliasHost, publicAPIHost)
	return nil
}

func saveSetting(key, value string) error {
	_, err := db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", key, value)
	return err
}
