package main

import (
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

func hashPassword(pw string) string {
	h := sha256.Sum256([]byte(pw))
	return hex.EncodeToString(h[:])
}

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
		"formatExpiry": func(s string) string {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				return s
			}
			return t.UTC().Format("2006-01-02 15:04 UTC")
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

// buildVersion is injected at build time via -ldflags "-X main.buildVersion=..."
var buildVersion string

// hostOf strips the scheme and trailing slash from a base URL, returning just the host.
func hostOf(u string) string {
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	return strings.TrimRight(u, "/")
}

// isAllowedOrigin reports whether the CORS origin matches the public base or alias base.
func isAllowedOrigin(origin, pb, ab string) bool {
	if origin == "" {
		return false
	}
	originHost := hostOf(origin)
	if h := hostOf(pb); h != "" && originHost == h {
		return true
	}
	if ab != "" {
		if h := hostOf(ab); h != "" && originHost == h {
			return true
		}
	}
	return false
}

// requestScheme returns the scheme of the incoming request, honouring X-Forwarded-Proto.
func requestScheme(r *http.Request) string {
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		return proto
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

var metaRedirectTmpl = template.Must(template.New("meta").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta http-equiv="refresh" content="0; url={{.LongURL}}">
<meta name="robots" content="noindex,nofollow">
{{if .OGTitle}}<title>{{.OGTitle}}</title>
<meta property="og:title" content="{{.OGTitle}}">
<meta name="twitter:title" content="{{.OGTitle}}">{{end}}
{{if .OGDescription}}<meta property="og:description" content="{{.OGDescription}}">
<meta name="twitter:description" content="{{.OGDescription}}">{{end}}
{{if .OGImage}}<meta property="og:image" content="{{.OGImage}}">
<meta name="twitter:image" content="{{.OGImage}}">
<meta name="twitter:card" content="summary_large_image">{{else}}<meta name="twitter:card" content="summary">{{end}}
<meta property="og:type" content="website">
<meta property="og:url" content="{{.ShortURL}}">
<style>:root{color-scheme:light dark}body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background-color:Canvas;color:CanvasText;font-family:system-ui,sans-serif;font-size:.9rem}a{color:LinkText}</style>
</head>
<body><p>Redirecting… <a href="{{.LongURL}}">click here</a></p></body>
</html>`))

var jsRedirectTmpl = template.Must(
	template.New("js").Funcs(template.FuncMap{
		"jsStr": func(s string) template.JS {
			b, _ := json.Marshal(s)
			return template.JS(b)
		},
	}).Parse(`<!DOCTYPE html>
<html lang="en">
<head><meta charset="UTF-8">
<meta name="robots" content="noindex,nofollow">
{{if .OGTitle}}<title>{{.OGTitle}}</title>
<meta property="og:title" content="{{.OGTitle}}">
<meta name="twitter:title" content="{{.OGTitle}}">{{end}}
{{if .OGDescription}}<meta property="og:description" content="{{.OGDescription}}">
<meta name="twitter:description" content="{{.OGDescription}}">{{end}}
{{if .OGImage}}<meta property="og:image" content="{{.OGImage}}">
<meta name="twitter:image" content="{{.OGImage}}">
<meta name="twitter:card" content="summary_large_image">{{else}}<meta name="twitter:card" content="summary">{{end}}
<meta property="og:type" content="website">
<meta property="og:url" content="{{.ShortURL}}">
<style>:root{color-scheme:light dark}body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background-color:Canvas;color:CanvasText;font-family:system-ui,sans-serif;font-size:.9rem}a{color:LinkText}form{display:flex;flex-direction:column;align-items:center;gap:.6rem}input[type=password]{padding:.5rem .75rem;border:1.5px solid #cbd5e0;border-radius:6px;font-size:.9rem;outline:none;width:220px;background:Canvas;color:CanvasText}button{padding:.5rem 1.25rem;background:#667eea;color:#fff;border:none;border-radius:6px;font-size:.9rem;cursor:pointer}#pw-err{color:#c53030;font-size:.8rem}</style>
</head>
<body>{{if .HasPassword}}<div style="text-align:center">
<p style="margin-bottom:.9rem">🔒 This link is password protected.</p>
<form id="pw-form">
<input type="password" id="pw-input" placeholder="Enter password" autofocus>
<button type="submit">Continue →</button>
<p id="pw-err" style="display:none">Incorrect password.</p>
</form>
</div>
<script>
document.getElementById('pw-form').onsubmit=async function(e){
e.preventDefault();
var r=await fetch({{jsStr .PassURL}},{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({password:document.getElementById('pw-input').value})});
if(r.ok){var d=await r.json();window.location.replace(d.url);}
else{document.getElementById('pw-err').style.display='';document.getElementById('pw-input').value='';document.getElementById('pw-input').focus();}
};
</script>{{else}}
<p>Redirecting… <a href="{{.LongURL}}">click here</a></p>
<script>window.location.replace({{jsStr .LongURL}});</script>
{{end}}
</body>
</html>`))

func renderIndex(w http.ResponseWriter, r *http.Request) {
	urls, _ := getAllURLs()
	pb, _, uh, ih, ah := cfg.snapshot()
	papiHost := cfg.publicAPIHostVal()

	data := struct {
		URLs          []URLRow
		Base          string
		AliasBase     string
		UIHost        string
		InternalHost  string
		AliasHost     string
		PublicAPIHost string
		BuildVersion  string
	}{URLs: urls, Base: pb, AliasBase: cfg.aliasBase(), UIHost: uh, InternalHost: ih, AliasHost: ah, PublicAPIHost: papiHost, BuildVersion: buildVersion}

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

func managementErrorResponse(w http.ResponseWriter, err error) {
	var invalid *requestError
	var conflict *conflictError
	switch {
	case errors.As(err, &invalid):
		jsonError(w, http.StatusBadRequest, invalid.Error())
	case errors.As(err, &conflict):
		jsonError(w, http.StatusConflict, conflict.Error())
	case errors.Is(err, sql.ErrNoRows):
		jsonError(w, http.StatusNotFound, "not found")
	default:
		log.Printf("management error: %v", err)
		jsonError(w, http.StatusInternalServerError, "database error")
	}
}

func shortenHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body createURLInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.URL) == "" {
		jsonError(w, http.StatusBadRequest, "invalid JSON or missing url field")
		return
	}
	created, err := createManagedURL(body)
	if err != nil {
		managementErrorResponse(w, err)
		return
	}

	pb, _, _, ih, _ := cfg.snapshot()
	ab := cfg.aliasBase()
	resp := map[string]any{
		"code":             created.Code,
		"long_url":         created.LongURL,
		"public_enabled":   created.PublicEnabled,
		"internal_enabled": created.InternalEnabled,
		"redirect_type":    created.RedirectType,
		"og_title":         created.OGTitle,
		"og_description":   created.OGDescription,
		"og_image":         created.OGImage,
		"has_password":     created.HasPassword,
		"description":      created.Description,
		"expires_at":       created.ExpiresAt,
		"max_uses":         created.MaxUses,
		"use_count":        created.UseCount,
	}
	if created.PublicEnabled {
		resp["short_url"] = fmt.Sprintf("%s/%s", pb, created.Code)
		if ab != "" {
			resp["alias_url"] = fmt.Sprintf("%s/%s", ab, created.Code)
		}
	}
	if created.InternalEnabled {
		// ih is stored as a full URL (e.g. "http://go"); strip the scheme so
		// the internal link reads as "go/code" for display and clipboard.
		resp["internal_url"] = fmt.Sprintf("%s/%s", hostOf(ih), created.Code)
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
		if err := deleteManagedURL(code); err == sql.ErrNoRows {
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
	var body updateURLInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	body.Code = code
	if _, err := updateManagedURL(body); err != nil {
		managementErrorResponse(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func settingsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(currentSettings())

	case http.MethodPatch:
		var body settingsUpdate
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if _, err := updateSettings(body); err != nil {
			log.Printf("settings update error: %v", err)
			jsonError(w, http.StatusInternalServerError, "failed to save setting")
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func passHandler(w http.ResponseWriter, r *http.Request) {
	// CORS: allow the public base URL and alias host to call this endpoint
	// (JS redirect pages served from those domains POST here cross-origin).
	pb, _, _, _, _ := cfg.snapshot()
	ab := cfg.aliasBase()
	if origin := r.Header.Get("Origin"); isAllowedOrigin(origin, pb, ab) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Vary", "Origin")
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	code := strings.TrimPrefix(r.URL.Path, "/pass/")
	if code == "" {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	rec, err := getRecord(code)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rec.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, rec.ExpiresAt); err == nil && time.Now().UTC().After(t) {
			jsonError(w, http.StatusGone, "this link has expired")
			return
		}
	}
	if rec.PasswordHash == "" {
		jsonError(w, http.StatusBadRequest, "no password set")
		return
	}
	if hashPassword(body.Password) != rec.PasswordHash {
		jsonError(w, http.StatusUnauthorized, "incorrect password")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"url": rec.LongURL})
}

func qrHandler(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimPrefix(r.URL.Path, "/qr/")
	if code == "" {
		http.NotFound(w, r)
		return
	}
	rec, err := getRecord(code)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	pb, _, _, _, _ := cfg.snapshot()
	ab := cfg.aliasBase()
	pubURL := fmt.Sprintf("%s/%s", pb, code)
	if ab != "" {
		pubURL = fmt.Sprintf("%s/%s", ab, code)
	}
	_ = rec // record exists; use its public URL
	png, err := qrcode.Encode(pubURL, qrcode.High, 512)
	if err != nil {
		http.Error(w, "qr error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(png)
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
	if rec.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, rec.ExpiresAt); err == nil && time.Now().UTC().After(t) {
			http.Error(w, "this link has expired", http.StatusGone)
			return
		}
	}
	if ok, err := incrementUseCount(code, rec.MaxUses); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	} else if !ok {
		http.Error(w, "this link has reached its use limit", http.StatusGone)
		return
	}
	if rec.RedirectType == "meta" || rec.RedirectType == "js" {
		pb, _, uh, _, _ := cfg.snapshot()
		ab := cfg.aliasBase()
		shortURL := fmt.Sprintf("%s/%s", pb, code)
		if ab != "" {
			shortURL = fmt.Sprintf("%s/%s", ab, code)
		}
		// passURL: internal redirects share the same router so a relative path works;
		// public/alias redirects use the dedicated public API host when configured,
		// otherwise fall back to the UI host (stored as a full URL).
		passURL := "/pass/" + code
		if !internal {
			apiBase := cfg.publicAPIBase()
			if apiBase == "" {
				if uh != "" {
					apiBase = strings.TrimRight(uh, "/")
				} else {
					apiBase = requestScheme(r) + "://" + effectiveHost(r)
				}
			}
			passURL = apiBase + "/pass/" + code
		}
		tmpl := metaRedirectTmpl
		if rec.RedirectType == "js" {
			tmpl = jsRedirectTmpl
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		tmpl.Execute(w, struct {
			LongURL, ShortURL, OGTitle, OGDescription, OGImage, Code, PassURL string
			HasPassword                                                       bool
		}{rec.LongURL, shortURL, rec.OGTitle, rec.OGDescription, rec.OGImage, code, passURL, rec.PasswordHash != ""})
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
	case strings.HasPrefix(r.URL.Path, "/qr/"):
		qrHandler(w, r)
	case strings.HasPrefix(r.URL.Path, "/pass/"):
		passHandler(w, r)
	default:
		return false
	}
	return true
}

// publicAPIRouter: public API host — serves /pass/ and /qr/ endpoints only.
func publicAPIRouter(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasPrefix(r.URL.Path, "/pass/"):
		passHandler(w, r)
	case strings.HasPrefix(r.URL.Path, "/qr/"):
		qrHandler(w, r)
	default:
		http.NotFound(w, r)
	}
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
	papiHost := cfg.publicAPIHostVal()

	// UIHost, InternalHost, AliasHost, PublicAPIHost are stored as full URLs;
	// use hostOf() to extract the bare hostname for comparison.
	uhHost := hostOf(uh)
	ihHost := hostOf(ih)
	ahHost := hostOf(ah)
	papiHostOnly := hostOf(papiHost)

	switch {
	case uhHost != "" && host == uhHost:
		uiRouter(w, r)
	case ph != "" && host == ph:
		publicRouter(w, r)
	case ahHost != "" && host == ahHost:
		publicRouter(w, r)
	case ihHost != "" && host == ihHost:
		internalRouter(w, r)
	case papiHostOnly != "" && host == papiHostOnly:
		publicAPIRouter(w, r)
	default:
		http.NotFound(w, r)
	}
}
