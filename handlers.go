package main

import (
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strings"

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

	data := struct {
		URLs         []URLRow
		Base         string
		AliasBase    string
		UIHost       string
		InternalHost string
		AliasHost    string
		BuildVersion string
	}{URLs: urls, Base: pb, AliasBase: cfg.aliasBase(), UIHost: uh, InternalHost: ih, AliasHost: ah, BuildVersion: buildVersion}

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
		RedirectType    string `json:"redirect_type"`
		OGTitle         string `json:"og_title"`
		OGDescription   string `json:"og_description"`
		OGImage         string `json:"og_image"`
		Password        string `json:"password"`
		Description     string `json:"description"`
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

	redirectType := body.RedirectType
	if redirectType != "meta" && redirectType != "js" {
		redirectType = "redirect"
	}
	ogTitle, ogDescription, ogImage := body.OGTitle, body.OGDescription, body.OGImage
	description := body.Description
	passwordHash := ""
	if body.Password != "" {
		passwordHash = hashPassword(body.Password)
	}

	var code string
	if customCode != "" {
		if !validCode.MatchString(customCode) {
			jsonError(w, http.StatusBadRequest, "custom alias must be 1–32 chars: letters, numbers, hyphens, underscores")
			return
		}
		if err := saveURL(customCode, longURL, publicEnabled, internalEnabled, redirectType, ogTitle, ogDescription, ogImage, passwordHash, description); err != nil {
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
			err = saveURL(code, longURL, publicEnabled, internalEnabled, redirectType, ogTitle, ogDescription, ogImage, passwordHash, description)
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
		"redirect_type":    redirectType,
		"og_title":         ogTitle,
		"og_description":   ogDescription,
		"og_image":         ogImage,
		"has_password":     passwordHash != "",
		"description":      description,
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
		RedirectType    *string `json:"redirect_type"`
		OGTitle         *string `json:"og_title"`
		OGDescription   *string `json:"og_description"`
		OGImage         *string `json:"og_image"`
		Password        *string `json:"password"`
		Description     *string `json:"description"`
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

	if body.LongURL != nil && strings.TrimSpace(*body.LongURL) == "" {
		jsonError(w, http.StatusBadRequest, "long_url cannot be empty")
		return
	}

	// Sanitize redirect_type
	if body.RedirectType != nil && *body.RedirectType != "meta" && *body.RedirectType != "js" {
		rt := "redirect"
		body.RedirectType = &rt
	}

	// Compute password hash if provided
	var passwordHash *string
	if body.Password != nil {
		h := ""
		if *body.Password != "" {
			h = hashPassword(*body.Password)
		}
		passwordHash = &h
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
		rt := rec.RedirectType
		if body.RedirectType != nil {
			rt = *body.RedirectType
		}
		ogt := rec.OGTitle
		if body.OGTitle != nil {
			ogt = *body.OGTitle
		}
		ogd := rec.OGDescription
		if body.OGDescription != nil {
			ogd = *body.OGDescription
		}
		ogi := rec.OGImage
		if body.OGImage != nil {
			ogi = *body.OGImage
		}
		opw := rec.PasswordHash
		if passwordHash != nil {
			opw = *passwordHash
		}
		odesc := rec.Description
		if body.Description != nil {
			odesc = *body.Description
		}
		tx, err := db.Begin()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "database error")
			return
		}
		defer tx.Rollback()
		if _, err := tx.Exec(
			"INSERT INTO urls (code, long_url, public_enabled, internal_enabled, redirect_type, og_title, og_description, og_image, password_hash, description, created_at) SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, created_at FROM urls WHERE code = ?",
			newCode, lu, boolToInt(nextPub), boolToInt(nextInt), rt, ogt, ogd, ogi, opw, odesc, code,
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

	if err := updateURL(code, body.LongURL, body.PublicEnabled, body.InternalEnabled, body.RedirectType, body.OGTitle, body.OGDescription, body.OGImage, passwordHash, body.Description); err != nil {
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
	if rec.RedirectType == "meta" || rec.RedirectType == "js" {
		pb, _, uh, _, _ := cfg.snapshot()
		ab := cfg.aliasBase()
		shortURL := fmt.Sprintf("%s/%s", pb, code)
		if ab != "" {
			shortURL = fmt.Sprintf("%s/%s", ab, code)
		}
		// passURL: internal redirects share the same router so a relative path works;
		// public/alias redirects must use the absolute webui URL because /pass/ is
		// only registered on the UI and internal routers.
		passURL := "/pass/" + code
		if !internal {
			uiHost := uh
			if uiHost == "" {
				uiHost = effectiveHost(r)
			}
			passURL = requestScheme(r) + "://" + uiHost + "/pass/" + code
		}
		tmpl := metaRedirectTmpl
		if rec.RedirectType == "js" {
			tmpl = jsRedirectTmpl
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		tmpl.Execute(w, struct {
			LongURL, ShortURL, OGTitle, OGDescription, OGImage, Code, PassURL string
			HasPassword                                                        bool
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
