package main

import (
	"net/url"
	"os"
	"strings"
	"sync"
)

var (
	port    = envOr("PORT", ":80")
	mcpPort = envOr("MCP_PORT", ":8081")
	dbFile  = envOr("DB_FILE", "urls.db")
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

var (
	cfg              = &appConfig{}
	settingsUpdateMu sync.Mutex
)

func (c *appConfig) snapshot() (publicBase, publicHost, uiHost, internalHost, aliasHost string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.PublicBase, c.PublicHost, c.UIHost, c.InternalHost, c.AliasHost
}

func (c *appConfig) fullSnapshot() (publicBase, publicHost, uiHost, internalHost, aliasHost, publicAPIHost string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.PublicBase, c.PublicHost, c.UIHost, c.InternalHost, c.AliasHost, c.PublicAPIHost
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
	return aliasBaseFrom(c.AliasHost, c.PublicBase)
}

func aliasBaseFrom(aliasHost, publicBase string) string {
	if aliasHost == "" {
		return ""
	}
	v := strings.TrimRight(aliasHost, "/")
	if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
		return v
	}
	// Legacy bare hostname — derive scheme from PublicBase.
	u, _ := url.Parse(publicBase)
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

type settingsValues struct {
	PublicBase    string `json:"public_base"`
	PublicHost    string `json:"public_host"`
	UIHost        string `json:"ui_host"`
	InternalHost  string `json:"internal_host"`
	AliasHost     string `json:"alias_host"`
	PublicAPIHost string `json:"public_api_host"`
}

type settingsUpdate struct {
	PublicBase    *string `json:"public_base,omitempty" jsonschema:"public short URL base, including scheme"`
	UIHost        *string `json:"ui_host,omitempty" jsonschema:"web UI URL, including scheme"`
	InternalHost  *string `json:"internal_host,omitempty" jsonschema:"internal redirect URL, including scheme"`
	AliasHost     *string `json:"alias_host,omitempty" jsonschema:"optional alternate public URL, including scheme"`
	PublicAPIHost *string `json:"public_api_host,omitempty" jsonschema:"optional public API URL, including scheme"`
}

func currentSettings() settingsValues {
	publicBase, publicHost, uiHost, internalHost, aliasHost, publicAPIHost := cfg.fullSnapshot()
	return settingsValues{
		PublicBase:    publicBase,
		PublicHost:    publicHost,
		UIHost:        uiHost,
		InternalHost:  internalHost,
		AliasHost:     aliasHost,
		PublicAPIHost: publicAPIHost,
	}
}

func updateSettings(input settingsUpdate) (settingsValues, error) {
	settingsUpdateMu.Lock()
	defer settingsUpdateMu.Unlock()

	settings := currentSettings()
	if input.PublicBase != nil {
		settings.PublicBase = *input.PublicBase
	}
	if input.UIHost != nil {
		settings.UIHost = *input.UIHost
	}
	if input.InternalHost != nil {
		settings.InternalHost = *input.InternalHost
	}
	if input.AliasHost != nil {
		settings.AliasHost = *input.AliasHost
	}
	if input.PublicAPIHost != nil {
		settings.PublicAPIHost = *input.PublicAPIHost
	}

	tx, err := db.Begin()
	if err != nil {
		return settingsValues{}, err
	}
	defer tx.Rollback()
	for key, value := range map[string]string{
		"public_base":     settings.PublicBase,
		"ui_host":         settings.UIHost,
		"internal_host":   settings.InternalHost,
		"alias_host":      settings.AliasHost,
		"public_api_host": settings.PublicAPIHost,
	} {
		if _, err := tx.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", key, value); err != nil {
			return settingsValues{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return settingsValues{}, err
	}

	cfg.apply(settings.PublicBase, settings.UIHost, settings.InternalHost, settings.AliasHost, settings.PublicAPIHost)
	return currentSettings(), nil
}
