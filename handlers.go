package main

import (
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strings"
)

//go:embed static
var staticFiles embed.FS

//go:embed static/index.html
var indexTmplSrc string

var indexTmpl = template.Must(
	template.New("index").Funcs(template.FuncMap{
		"truncate": func(s string, n int) string {
			if len(s) <= n {
				return s
			}
			return s[:n] + "..."
		},
		"stripScheme": func(s string) string {
			if i := strings.Index(s, "://"); i >= 0 {
				return s[i+3:]
			}
			return s
		},
	}).Parse(indexTmplSrc),
)

// effectiveHost returns the hostname the client used to reach the server.
// X-Forwarded-Host is preferred so that reverse-proxy deployments that rewrite
// the Host header still route correctly. Only deploy behind a trusted proxy;
// do not expose this service directly to the internet without one.
func effectiveHost(r *http.Request) string {
	if xfh := r.Header.Get("X-Forwarded-Host"); xfh != "" {
		h, _, _ := strings.Cut(xfh, ":")
		return h
	}
	h, _, _ := strings.Cut(r.Host, ":")
	return h
}

func renderIndex(w http.ResponseWriter, r *http.Request) {
	urls, _ := getAllURLs()
	pb, _, uh, ih, ah := cfg.snapshot()

	data := struct {
		URLs         []URLRow
		Base         string
		AliasBase    string
		UIHost       string
		InternalHost string
		AliasHost    string
	}{URLs: urls, Base: pb, AliasBase: cfg.aliasBase(), UIHost: uh, InternalHost: ih, AliasHost: ah}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := indexTmpl.Execute(w, data); err != nil {
		log.Println("template error:", err)
	}
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func shortenHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		URL             string `json:"url"`
		CustomCode      string `json:"custom_code"`
		PublicEnabled   *bool  `json:"public_enabled"`
		InternalEnabled *bool  `json:"internal_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.URL) == "" {
		jsonError(w, http.StatusBadRequest, "invalid JSON or missing url field")
		return
	}

	longURL := strings.TrimSpace(body.URL)
	customCode := strings.TrimSpace(body.CustomCode)
	publicEnabled := body.PublicEnabled == nil || *body.PublicEnabled
	internalEnabled := body.InternalEnabled == nil || *body.InternalEnabled

	if !publicEnabled && !internalEnabled {
		jsonError(w, http.StatusBadRequest, "at least one link type (public_enabled or internal_enabled) must be true")
		return
	}

	var code string
	if customCode != "" {
		if !validCode.MatchString(customCode) {
			jsonError(w, http.StatusBadRequest, "custom alias must be 1–32 chars: letters, numbers, hyphens, underscores")
			return
		}
		if err := saveURL(customCode, longURL, publicEnabled, internalEnabled); err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				jsonError(w, http.StatusConflict, fmt.Sprintf("alias '%s' is already taken", customCode))
			} else {
				jsonError(w, http.StatusInternalServerError, "database error")
			}
			return
		}
		code = customCode
	} else {
		for {
			var err error
			code, err = generateCode()
			if err != nil {
				jsonError(w, http.StatusInternalServerError, "internal error")
				return
			}
			err = saveURL(code, longURL, publicEnabled, internalEnabled)
			if err == nil {
				break
			}
			if !strings.Contains(err.Error(), "UNIQUE constraint failed") {
				jsonError(w, http.StatusInternalServerError, "database error")
				return
			}
		}
	}

	pb, _, _, ih, _ := cfg.snapshot()
	ab := cfg.aliasBase()
	resp := map[string]any{
		"code":             code,
		"long_url":         longURL,
		"public_enabled":   publicEnabled,
		"internal_enabled": internalEnabled,
	}
	if publicEnabled {
		resp["short_url"] = fmt.Sprintf("%s/%s", pb, code)
		if ab != "" {
			resp["alias_url"] = fmt.Sprintf("%s/%s", ab, code)
		}
	}
	if internalEnabled {
		resp["internal_url"] = fmt.Sprintf("%s/%s", ih, code)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

func urlsHandler(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimPrefix(r.URL.Path, "/urls/")
	if code == "" {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodDelete:
		if err := deleteURL(code); err == sql.ErrNoRows {
			jsonError(w, http.StatusNotFound, "not found")
		} else if err != nil {
			jsonError(w, http.StatusInternalServerError, "database error")
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	case http.MethodPatch:
		urlsPatchHandler(w, r, code)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func urlsPatchHandler(w http.ResponseWriter, r *http.Request, code string) {
	var body struct {
		NewCode         *string `json:"code"`
		LongURL         *string `json:"long_url"`
		PublicEnabled   *bool   `json:"public_enabled"`
		InternalEnabled *bool   `json:"internal_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	rec, err := getRecord(code)
	if err == sql.ErrNoRows {
		jsonError(w, http.StatusNotFound, "not found")
		return
	} else if err != nil {
		jsonError(w, http.StatusInternalServerError, "database error")
		return
	}

	nextPub := rec.PublicEnabled
	if body.PublicEnabled != nil {
		nextPub = *body.PublicEnabled
	}
	nextInt := rec.InternalEnabled
	if body.InternalEnabled != nil {
		nextInt = *body.InternalEnabled
	}
	if !nextPub && !nextInt {
		jsonError(w, http.StatusBadRequest, "at least one link type must remain active")
		return
	}

	if body.LongURL != nil && strings.TrimSpace(*body.LongURL) == "" {
		jsonError(w, http.StatusBadRequest, "long_url cannot be empty")
		return
	}

	// Rename: INSERT with new code (preserving created_at) then DELETE old (code is PK)
	if body.NewCode != nil {
		newCode := strings.TrimSpace(*body.NewCode)
		if !validCode.MatchString(newCode) {
			jsonError(w, http.StatusBadRequest, "code must be 1–32 chars: letters, numbers, hyphens, underscores")
			return
		}
		lu := rec.LongURL
		if body.LongURL != nil {
			lu = *body.LongURL
		}
		tx, err := db.Begin()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "database error")
			return
		}
		defer tx.Rollback()
		if _, err := tx.Exec(
			"INSERT INTO urls (code, long_url, public_enabled, internal_enabled, created_at) SELECT ?, ?, ?, ?, created_at FROM urls WHERE code = ?",
			newCode, lu, boolToInt(nextPub), boolToInt(nextInt), code,
		); err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				jsonError(w, http.StatusConflict, fmt.Sprintf("code '%s' is already taken", newCode))
			} else {
				jsonError(w, http.StatusInternalServerError, "database error")
			}
			return
		}
		if _, err := tx.Exec("DELETE FROM urls WHERE code = ?", code); err != nil {
			jsonError(w, http.StatusInternalServerError, "database error")
			return
		}
		if err := tx.Commit(); err != nil {
			jsonError(w, http.StatusInternalServerError, "database error")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if err := updateURL(code, body.LongURL, body.PublicEnabled, body.InternalEnabled); err != nil {
		jsonError(w, http.StatusInternalServerError, "database error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func settingsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		pb, ph, uh, ih, ah := cfg.snapshot()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"public_base":   pb,
			"public_host":   ph,
			"ui_host":       uh,
			"internal_host": ih,
			"alias_host":    ah,
		})

	case http.MethodPatch:
		var body struct {
			PublicBase   *string `json:"public_base"`
			UIHost       *string `json:"ui_host"`
			InternalHost *string `json:"internal_host"`
			AliasHost    *string `json:"alias_host"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		pb, _, uh, ih, ah := cfg.snapshot()
		if body.PublicBase != nil {
			pb = *body.PublicBase
		}
		if body.UIHost != nil {
			uh = *body.UIHost
		}
		if body.InternalHost != nil {
			ih = *body.InternalHost
		}
		if body.AliasHost != nil {
			ah = *body.AliasHost
		}
		cfg.apply(pb, uh, ih, ah)
		for k, v := range map[string]string{
			"public_base":   pb,
			"ui_host":       uh,
			"internal_host": ih,
			"alias_host":    ah,
		} {
			if err := saveSetting(k, v); err != nil {
				jsonError(w, http.StatusInternalServerError, "failed to save setting")
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func doRedirect(w http.ResponseWriter, r *http.Request, code string, internal bool) {
	rec, err := getRecord(code)
	if err == sql.ErrNoRows {
		http.Error(w, "short URL not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if internal && !rec.InternalEnabled {
		http.Error(w, "internal link disabled", http.StatusNotFound)
		return
	}
	if !internal && !rec.PublicEnabled {
		http.Error(w, "public link disabled", http.StatusNotFound)
		return
	}
	http.Redirect(w, r, rec.LongURL, http.StatusFound)
}

var staticFS = func() http.Handler {
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(sub))
}()

// apiRouter serves the management API — used by both the UI host and internal host.
// Returns true if the request was handled.
func apiRouter(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.URL.Path == "/shorten":
		shortenHandler(w, r)
	case strings.HasPrefix(r.URL.Path, "/urls/"):
		urlsHandler(w, r)
	case r.URL.Path == "/settings":
		settingsHandler(w, r)
	default:
		return false
	}
	return true
}

// uiRouter: web UI host — serves the UI and API, no redirects.
func uiRouter(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		renderIndex(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/static/") {
		http.StripPrefix("/static/", staticFS).ServeHTTP(w, r)
		return
	}
	if !apiRouter(w, r) {
		http.NotFound(w, r)
	}
}

// publicRouter: public redirect host — redirects only, no UI.
func publicRouter(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimPrefix(r.URL.Path, "/")
	if code == "" {
		http.NotFound(w, r)
		return
	}
	doRedirect(w, r, code, false)
}

// internalRouter: internal host (e.g. "go") — UI at root, redirects elsewhere.
func internalRouter(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		renderIndex(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/static/") {
		http.StripPrefix("/static/", staticFS).ServeHTTP(w, r)
		return
	}
	if apiRouter(w, r) {
		return
	}
	code := strings.TrimPrefix(r.URL.Path, "/")
	doRedirect(w, r, code, true)
}

func mainHandler(w http.ResponseWriter, r *http.Request) {
	host := effectiveHost(r)
	_, ph, uh, ih, ah := cfg.snapshot()

	switch host {
	case uh:
		uiRouter(w, r)
	case ph, ah:
		publicRouter(w, r)
	case ih:
		internalRouter(w, r)
	default:
		// Fallback: serve UI (e.g. during local dev with no matching host)
		uiRouter(w, r)
	}
}
