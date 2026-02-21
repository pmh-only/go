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
	mu           sync.RWMutex
	PublicBase   string // full URL prefix, e.g. https://pmh.codes
	PublicHost   string // hostname only,  e.g. pmh.codes
	UIHost       string // e.g. links.pmh.codes
	InternalHost string // e.g. go
	AliasHost    string // e.g. pmh.so (alternate public redirect host)
}

var cfg = &appConfig{}

func (c *appConfig) snapshot() (publicBase, publicHost, uiHost, internalHost, aliasHost string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.PublicBase, c.PublicHost, c.UIHost, c.InternalHost, c.AliasHost
}

func (c *appConfig) apply(publicBase, uiHost, internalHost, aliasHost string) {
	publicBase = strings.TrimRight(publicBase, "/")
	u, _ := url.Parse(publicBase)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.PublicBase = publicBase
	c.PublicHost = u.Hostname()
	c.UIHost = uiHost
	c.InternalHost = internalHost
	c.AliasHost = aliasHost
}

func loadSettings() error {
	publicBase := envOr("BASE_URL", "http://localhost")
	uiHost := envOr("UI_HOST", "links.localhost")
	internalHost := envOr("INTERNAL_HOST", "go")
	aliasHost := envOr("ALIAS_HOST", "")

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
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	cfg.apply(publicBase, uiHost, internalHost, aliasHost)
	return nil
}

func saveSetting(key, value string) error {
	_, err := db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", key, value)
	return err
}
