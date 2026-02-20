package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	charset = "abcdefghkprstxyz2345678"
	codeLen = 6
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

var (
	port   = envOr("PORT", ":80")
	dbFile = envOr("DB_FILE", "urls.db")
)

// appConfig holds the three configurable hostnames. It is safe for concurrent
// reads and writes (settings can be updated live via the web UI).
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

func hostOnly(h string) string {
	host, _, _ := strings.Cut(h, ":")
	return host
}

var (
	db        *sql.DB
	validCode = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,32}$`)

	indexTmplSrc = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>URL Shortener</title>
  <style>
    *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
    html, body { height: 100%; }
    body {
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
      background: #f0f4f8;
      display: flex;
      height: 100vh;
      overflow: hidden;
    }

    /* ── Left panel ── */
    .panel-left {
      width: 380px;
      flex-shrink: 0;
      background: #fff;
      border-right: 1px solid #e2e8f0;
      display: flex;
      flex-direction: column;
      overflow-y: auto;
      padding: 2rem 1.75rem;
    }
    .panel-left h1 { font-size: 1.4rem; color: #1a202c; margin-bottom: .25rem; }
    .panel-left .subtitle { color: #718096; font-size: .875rem; margin-bottom: 1.75rem; }

    label.field-label { display: block; font-size: .8rem; font-weight: 600; color: #4a5568; margin-bottom: .35rem; }
    .field { margin-bottom: 1rem; }
    .form-row { display: flex; }
    .prefix {
      padding: .65rem .85rem;
      background: #edf2f7;
      border: 1.5px solid #cbd5e0;
      border-right: none;
      border-radius: 7px 0 0 7px;
      color: #718096;
      font-size: .85rem;
      white-space: nowrap;
      align-self: stretch;
      display: flex;
      align-items: center;
    }
    input[type="url"], input[type="text"] {
      width: 100%;
      padding: .65rem .85rem;
      border: 1.5px solid #cbd5e0;
      border-radius: 7px;
      font-size: .95rem;
      outline: none;
      transition: border-color .15s;
      background: #fff;
    }
    input[type="text"].alias { border-radius: 0 7px 7px 0; flex: 1; }
    input:focus { border-color: #667eea; }

    .link-toggles { display: flex; gap: .6rem; }
    .link-toggle {
      flex: 1;
      display: flex;
      align-items: center;
      gap: .45rem;
      padding: .55rem .8rem;
      border: 1.5px solid #cbd5e0;
      border-radius: 7px;
      cursor: pointer;
      transition: border-color .15s, background .15s;
      user-select: none;
    }
    .link-toggle input[type="checkbox"] { display: none; }
    .link-toggle .dot {
      width: 12px; height: 12px;
      border-radius: 50%;
      border: 2px solid #cbd5e0;
      flex-shrink: 0;
      transition: background .15s, border-color .15s;
    }
    .link-toggle .info strong { display: block; font-size: .82rem; color: #4a5568; }
    .link-toggle .info small  { font-size: .72rem; color: #a0aec0; font-family: monospace; }
    .link-toggle.public.on   { border-color: #63b3ed; background: #ebf8ff; }
    .link-toggle.public.on   .dot { background: #3182ce; border-color: #3182ce; }
    .link-toggle.public.on   .info strong { color: #2b6cb0; }
    .link-toggle.internal.on { border-color: #68d391; background: #f0fff4; }
    .link-toggle.internal.on .dot { background: #38a169; border-color: #38a169; }
    .link-toggle.internal.on .info strong { color: #276749; }

    #toggleErr { color: #c53030; font-size: .78rem; margin-top: .35rem; display: none; }

    button.primary {
      width: 100%; padding: .75rem;
      background: #667eea; color: #fff;
      border: none; border-radius: 7px;
      font-size: .95rem; font-weight: 600;
      cursor: pointer; transition: background .15s;
      margin-top: .25rem;
    }
    button.primary:hover { background: #5a67d8; }

    .result {
      margin-top: 1.1rem;
      padding: .9rem 1rem;
      border-radius: 7px;
      font-size: .88rem;
    }
    .result.success { background: #f0fff4; border: 1.5px solid #9ae6b4; color: #276749; }
    .result.error   { background: #fff5f5; border: 1.5px solid #feb2b2; color: #c53030; }
    .result .rlabel { font-weight: 700; margin-bottom: .45rem; color: #1a202c; font-size: .82rem; text-transform: uppercase; letter-spacing: .05em; }
    .url-row { display: flex; align-items: center; gap: .4rem; margin-bottom: .3rem; }
    .url-row a { flex: 1; color: #2b6cb0; word-break: break-all; font-size: .83rem; }

    /* ── Right panel ── */
    .panel-right {
      flex: 1;
      display: flex;
      flex-direction: column;
      overflow: hidden;
    }
    .panel-right-header {
      padding: 1.25rem 1.75rem;
      border-bottom: 1px solid #e2e8f0;
      background: #fff;
      display: flex;
      align-items: center;
      justify-content: space-between;
      flex-shrink: 0;
    }
    .panel-right-header h2 { font-size: 1rem; color: #1a202c; }
    .panel-right-header .count { font-size: .8rem; color: #a0aec0; }
    .table-wrap { flex: 1; overflow-y: auto; }

    table { width: 100%; border-collapse: collapse; font-size: .84rem; }
    thead th {
      position: sticky; top: 0;
      background: #f7fafc;
      text-align: left;
      padding: .6rem 1rem;
      color: #718096;
      font-weight: 600;
      font-size: .75rem;
      text-transform: uppercase;
      letter-spacing: .05em;
      border-bottom: 1px solid #e2e8f0;
      white-space: nowrap;
    }
    tbody tr { border-bottom: 1px solid #f0f4f8; }
    tbody tr:hover { background: #f7fafc; }
    td { padding: .65rem 1rem; vertical-align: middle; }
    td.td-links { min-width: 220px; }
    td.td-original { max-width: 280px; word-break: break-all; }
    td.td-date { white-space: nowrap; color: #a0aec0; font-size: .78rem; }
    td.td-vis { white-space: nowrap; }

    .link-line { display: flex; align-items: center; gap: .3rem; margin-bottom: .18rem; }
    .link-line a { color: #2b6cb0; font-size: .82rem; flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .link-line a.disabled { color: #a0aec0; text-decoration: line-through; pointer-events: none; }
    .link-copy {
      flex-shrink: 0;
      padding: .1rem .4rem; font-size: .7rem;
      background: #edf2f7; color: #718096;
      border-radius: 4px; border: none; cursor: pointer;
      opacity: 0; transition: opacity .15s;
    }
    tr:hover .link-copy { opacity: 1; }
    .link-copy:hover { background: #e2e8f0; }
    .code-row { display: flex; align-items: center; gap: .3rem; margin-bottom: .25rem; }
    .code-label { font-family: monospace; font-size: .82rem; font-weight: 600; color: #4a5568; }
    .code-edit-row { display: flex; align-items: center; gap: .3rem; margin-bottom: .25rem; }
    .code-edit-input {
      font-family: monospace; font-size: .82rem;
      padding: .15rem .4rem; border: 1.5px solid #667eea;
      border-radius: 5px; outline: none; width: 110px;
    }

    /* shared tag + row-toggle styles */
    .url-tag, .row-toggle {
      font-size: .65rem; font-weight: 700;
      padding: .13rem .4rem;
      border-radius: 4px;
      white-space: nowrap;
      text-transform: uppercase;
      letter-spacing: .04em;
      border: none;
      cursor: pointer;
      transition: opacity .15s, filter .15s;
    }
    .url-tag { cursor: default; }
    .row-toggle { cursor: pointer; }
    .row-toggle:hover { filter: brightness(.88); }
    .tag-public.on    { background: #bee3f8; color: #2b6cb0; }
    .tag-public.off   { background: #e2e8f0; color: #a0aec0; }
    .tag-internal.on  { background: #c6f6d5; color: #276749; }
    .tag-internal.off { background: #e2e8f0; color: #a0aec0; }

    .copy-btn {
      padding: .18rem .55rem; font-size: .75rem;
      background: #edf2f7; color: #4a5568;
      border-radius: 5px; border: none; cursor: pointer; white-space: nowrap;
    }
    .copy-btn:hover { background: #e2e8f0; }

    .action-btn {
      padding: .22rem .6rem; font-size: .75rem;
      border-radius: 5px; border: none; cursor: pointer; white-space: nowrap;
      font-weight: 500;
    }
    .btn-edit   { background: #edf2f7; color: #4a5568; }
    .btn-edit:hover { background: #e2e8f0; }
    .btn-delete { background: #fff5f5; color: #c53030; }
    .btn-delete:hover { background: #fed7d7; }
    .btn-save   { background: #c6f6d5; color: #276749; }
    .btn-save:hover { background: #9ae6b4; }
    .btn-cancel { background: #edf2f7; color: #4a5568; }
    .btn-cancel:hover { background: #e2e8f0; }
    .edit-input {
      width: 100%; padding: .35rem .55rem;
      border: 1.5px solid #667eea; border-radius: 6px;
      font-size: .83rem; outline: none;
      margin-bottom: .35rem;
    }
    td.td-actions { white-space: nowrap; vertical-align: top; }
    .actions-row { display: flex; flex-direction: column; gap: .25rem; align-items: flex-start; }
    .vis-row { display: flex; gap: .25rem; flex-wrap: wrap; }
    .act-row { display: flex; gap: .25rem; }

    .empty-state {
      display: flex; flex-direction: column;
      align-items: center; justify-content: center;
      height: 100%; color: #a0aec0; gap: .5rem;
      font-size: .9rem;
    }
    .settings-toggle {
      display: flex; align-items: center; gap: .4rem;
      background: none; border: 1px solid #e2e8f0;
      border-radius: 7px; padding: .45rem .8rem;
      font-size: .82rem; color: #718096; cursor: pointer;
      transition: background .15s;
    }
    .settings-toggle:hover { background: #f7fafc; }
    .panel-left input[type="url"],
    .panel-left input[type="text"] { font-size: .88rem; padding: .55rem .75rem; }
  </style>
</head>
<body>

<!-- ── Left: form ── -->
<aside class="panel-left">
  <h1>URL Shortener</h1>
  <p class="subtitle">Paste a long URL and get short links.</p>

  <form id="shortenForm" onsubmit="shorten(event)">
    <div class="field">
      <label class="field-label" for="urlInput">Long URL</label>
      <input type="url" id="urlInput" placeholder="https://example.com/very/long/url" required>
    </div>
    <div class="field">
      <label class="field-label" for="aliasInput">Custom alias <span style="color:#a0aec0;font-weight:400">(optional)</span></label>
      <div class="form-row">
        <span class="prefix">{{.Base}}/</span>
        <input type="text" id="aliasInput" class="alias" placeholder="my-alias"
               pattern="[a-zA-Z0-9_\-]{1,32}"
               title="Letters, numbers, hyphens, underscores — max 32 chars">
      </div>
    </div>
    <div class="field">
      <label class="field-label">Active link types</label>
      <div class="link-toggles">
        <label class="link-toggle public on" id="togglePublic">
          <input type="checkbox" id="chkPublic" checked onchange="onToggle()">
          <span class="dot"></span>
          <span class="info">
            <strong>Public</strong>
            <small>{{.Base}}/…</small>
          </span>
        </label>
        <label class="link-toggle internal on" id="toggleInternal">
          <input type="checkbox" id="chkInternal" checked onchange="onToggle()">
          <span class="dot"></span>
          <span class="info">
            <strong>Internal</strong>
            <small>go/…</small>
          </span>
        </label>
      </div>
      <p id="toggleErr">At least one link type must be active.</p>
    </div>
    <button type="submit" class="primary">Shorten</button>
  </form>

  <div id="result"></div>

  <!-- ── Settings ── -->
  <div style="margin-top:auto;padding-top:1.5rem">
    <button class="settings-toggle" onclick="toggleSettings()" id="settingsBtn">
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>
      Hostnames
    </button>
    <div id="settingsPanel" style="display:none;margin-top:.75rem">
      <div class="field">
        <label class="field-label">Public base URL</label>
        <input type="url" id="cfgPublicBase" value="{{.Base}}" placeholder="https://pmh.codes">
        <small style="color:#a0aec0;font-size:.74rem">Used for public short links</small>
      </div>
      <div class="field">
        <label class="field-label">UI host</label>
        <input type="text" id="cfgUIHost" value="{{.UIHost}}" placeholder="links.pmh.codes">
        <small style="color:#a0aec0;font-size:.74rem">Host that serves this web UI</small>
      </div>
      <div class="field">
        <label class="field-label">Internal host</label>
        <input type="text" id="cfgInternalHost" value="{{.InternalHost}}" placeholder="go">
        <small style="color:#a0aec0;font-size:.74rem">Host for internal go-links</small>
      </div>
      <div class="field">
        <label class="field-label">Alias host <span style="color:#a0aec0;font-weight:400">(optional)</span></label>
        <input type="text" id="cfgAliasHost" value="{{.AliasHost}}" placeholder="pmh.so">
        <small style="color:#a0aec0;font-size:.74rem">Alternate public redirect host (e.g. a shorter domain)</small>
      </div>
      <div style="display:flex;gap:.5rem;align-items:center">
        <button class="action-btn btn-save" style="padding:.5rem 1rem;font-size:.85rem" onclick="saveSettings()">Save</button>
        <span id="settingsFeedback" style="font-size:.8rem;color:#276749;display:none">Saved!</span>
      </div>
    </div>
  </div>
</aside>

<!-- ── Right: list ── -->
<main class="panel-right">
  <div class="panel-right-header">
    <h2>All URLs</h2>
    <span class="count">{{len .URLs}} entries</span>
  </div>
  <div class="table-wrap">
    {{if .URLs}}
    <table>
      <thead>
        <tr>
          <th>Links</th>
          <th>Original</th>
          <th>Created</th>
          <th>Actions</th>
        </tr>
      </thead>
      <tbody>
        {{range .URLs}}
        <tr id="row-{{.Code}}">
          <td class="td-links">
            <div class="code-row" id="code-display-{{.Code}}">
              <span class="code-label">{{.Code}}</span>
              <button class="link-copy" style="opacity:0" onclick="startEditCode('{{.Code}}')">✎</button>
            </div>
            <div class="link-line">
              <a {{if .PublicEnabled}}href="/{{.Code}}" target="_blank"{{else}}class="disabled"{{end}} id="pub-link-{{.Code}}">{{$.Base}}/{{.Code}}</a>
              <button class="link-copy" onclick="copyRaw('{{$.Base}}/{{.Code}}',this)">Copy</button>
            </div>
            <div class="link-line">
              <a {{if not .InternalEnabled}}class="disabled"{{end}} id="int-link-{{.Code}}">go/{{.Code}}</a>
              <button class="link-copy" onclick="copyRaw('go/{{.Code}}',this)">Copy</button>
            </div>
          </td>
          <td class="td-original" id="orig-{{.Code}}">
            <a href="{{.LongURL}}" target="_blank" style="color:#2b6cb0">{{truncate .LongURL 55}}</a>
          </td>
          <td class="td-date">{{.CreatedAt}}</td>
          <td class="td-actions">
            <div class="actions-row">
              <div class="vis-row">
                <button class="row-toggle tag-public {{if .PublicEnabled}}on{{else}}off{{end}}"
                        onclick="rowToggle('{{.Code}}','public',this)"
                        title="Toggle public link">Public</button>
                <button class="row-toggle tag-internal {{if .InternalEnabled}}on{{else}}off{{end}}"
                        onclick="rowToggle('{{.Code}}','internal',this)"
                        title="Toggle internal link">Internal</button>
              </div>
              <div class="act-row">
                <button class="action-btn btn-edit" onclick="startEdit('{{.Code}}','{{.LongURL}}')">Edit</button>
                <button class="action-btn btn-delete" onclick="deleteRow('{{.Code}}')">Delete</button>
              </div>
            </div>
          </td>
        </tr>
        {{end}}
      </tbody>
    </table>
    {{else}}
    <div class="empty-state">
      <svg width="40" height="40" fill="none" stroke="#cbd5e0" stroke-width="1.5" viewBox="0 0 24 24"><path d="M13.828 10.172a4 4 0 0 0-5.656 0l-4 4a4 4 0 1 0 5.656 5.656l1.102-1.101"/><path d="M10.172 13.828a4 4 0 0 0 5.656 0l4-4a4 4 0 0 0-5.656-5.656l-1.1 1.1"/></svg>
      <span>No URLs yet — shorten one on the left.</span>
    </div>
    {{end}}
  </div>
</main>

<script>
/* ── form toggles ── */
function onToggle() {
  const pub  = document.getElementById('chkPublic');
  const int_ = document.getElementById('chkInternal');
  document.getElementById('togglePublic').classList.toggle('on', pub.checked);
  document.getElementById('togglePublic').classList.toggle('off', !pub.checked);
  document.getElementById('toggleInternal').classList.toggle('on', int_.checked);
  document.getElementById('toggleInternal').classList.toggle('off', !int_.checked);
  document.getElementById('toggleErr').style.display = (!pub.checked && !int_.checked) ? '' : 'none';
}

/* ── shorten ── */
async function shorten(e) {
  e.preventDefault();
  const pub  = document.getElementById('chkPublic').checked;
  const int_ = document.getElementById('chkInternal').checked;
  if (!pub && !int_) { document.getElementById('toggleErr').style.display = ''; return; }

  const url   = document.getElementById('urlInput').value.trim();
  const alias = document.getElementById('aliasInput').value.trim();
  const resultEl = document.getElementById('result');
  resultEl.innerHTML = '';

  const payload = {url, public_enabled: pub, internal_enabled: int_};
  if (alias) payload.custom_code = alias;

  try {
    const res  = await fetch('/shorten', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify(payload)});
    const data = await res.json();
    if (!res.ok) {
      resultEl.innerHTML = '<div class="result error"><div class="rlabel">Error</div>' + (data.error||'Something went wrong') + '</div>';
      return;
    }
    let rows = '';
    if (data.short_url)    rows += urlRow('pub-'+data.code,  'public',   data.short_url,  true);
    if (data.internal_url) rows += urlRow('int-'+data.code,  'internal', data.internal_url, false);
    resultEl.innerHTML = '<div class="result success"><div class="rlabel">Your links</div>' + rows + '</div>';
    setTimeout(() => location.reload(), 2000);
  } catch {
    resultEl.innerHTML = '<div class="result error"><div class="rlabel">Error</div>Network error.</div>';
  }
}

function urlRow(id, type, text, isHref) {
  const tag = '<span class="url-tag tag-' + type + ' on">' + type + '</span>';
  const a   = isHref
    ? '<a id="' + id + '" href="' + text + '" target="_blank">' + text + '</a>'
    : '<a id="' + id + '">' + text + '</a>';
  const btn = '<button class="copy-btn" onclick="copyText(\'' + id + '\',this)">Copy</button>';
  return '<div class="url-row">' + tag + a + btn + '</div>';
}

function copyText(id, btn) {
  navigator.clipboard.writeText(document.getElementById(id).textContent).then(() => {
    btn.textContent = 'Copied!';
    setTimeout(() => btn.textContent = 'Copy', 2000);
  });
}

/* ── edit code (ID) ── */
function startEditCode(code) {
  const disp = document.getElementById('code-display-' + code);
  disp.style.display = 'none';
  const row = document.getElementById('row-' + code);
  const editDiv = document.createElement('div');
  editDiv.className = 'code-edit-row';
  editDiv.id = 'code-edit-' + code;
  editDiv.innerHTML =
    '<input class="code-edit-input" id="code-input-' + code + '" value="' + code + '">' +
    '<button class="link-copy" style="opacity:1;background:#c6f6d5;color:#276749" onclick="saveEditCode(\'' + code + '\')">✓</button>' +
    '<button class="link-copy" style="opacity:1" onclick="cancelEditCode(\'' + code + '\')">✕</button>';
  disp.parentNode.insertBefore(editDiv, disp);
  const inp = document.getElementById('code-input-' + code);
  inp.focus(); inp.select();
  inp.addEventListener('keydown', e => {
    if (e.key === 'Enter')  saveEditCode(code);
    if (e.key === 'Escape') cancelEditCode(code);
  });
}

async function saveEditCode(oldCode) {
  const newCode = document.getElementById('code-input-' + oldCode).value.trim();
  if (!newCode || newCode === oldCode) { cancelEditCode(oldCode); return; }
  const res = await fetch('/urls/' + oldCode, {
    method: 'PATCH',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({code: newCode}),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    document.getElementById('code-input-' + oldCode).style.borderColor = '#fc8181';
    document.getElementById('code-input-' + oldCode).title = data.error || 'Error';
    return;
  }
  // Update DOM in-place
  const row = document.getElementById('row-' + oldCode);
  row.id = 'row-' + newCode;
  document.getElementById('code-edit-' + oldCode).remove();
  const disp = document.getElementById('code-display-' + oldCode);
  disp.id = 'code-display-' + newCode;
  disp.querySelector('.code-label').textContent = newCode;
  disp.style.display = '';
  // Update link text and hrefs
  const pb  = document.getElementById('pub-link-' + oldCode);
  const ib  = document.getElementById('int-link-' + oldCode);
  if (pb) { pb.id = 'pub-link-' + newCode; pb.textContent = pb.textContent.replace(oldCode, newCode); if (pb.href) pb.href = '/' + newCode; }
  if (ib) { ib.id = 'int-link-' + newCode; ib.textContent = ib.textContent.replace(oldCode, newCode); }
  // Update action buttons' onclick references
  row.querySelectorAll('[onclick]').forEach(el => {
    el.setAttribute('onclick', el.getAttribute('onclick').replaceAll("'" + oldCode + "'", "'" + newCode + "'"));
  });
}

function cancelEditCode(code) {
  const editDiv = document.getElementById('code-edit-' + code);
  if (editDiv) editDiv.remove();
  document.getElementById('code-display-' + code).style.display = '';
}

function copyRaw(text, btn) {
  navigator.clipboard.writeText(text).then(() => {
    btn.textContent = 'Copied!';
    setTimeout(() => btn.textContent = 'Copy', 2000);
  });
}

/* ── settings panel ── */
function toggleSettings() {
  const p = document.getElementById('settingsPanel');
  p.style.display = p.style.display === 'none' ? '' : 'none';
}

async function saveSettings() {
  const payload = {
    public_base:   document.getElementById('cfgPublicBase').value.trim(),
    ui_host:       document.getElementById('cfgUIHost').value.trim(),
    internal_host: document.getElementById('cfgInternalHost').value.trim(),
    alias_host:    document.getElementById('cfgAliasHost').value.trim(),
  };
  const res = await fetch('/settings', {method:'PATCH', headers:{'Content-Type':'application/json'}, body:JSON.stringify(payload)});
  const fb = document.getElementById('settingsFeedback');
  if (res.ok) {
    fb.textContent = 'Saved!'; fb.style.color = '#276749';
  } else {
    fb.textContent = 'Error saving.'; fb.style.color = '#c53030';
  }
  fb.style.display = '';
  setTimeout(() => fb.style.display = 'none', 2500);
}

/* ── row visibility toggle ── */
async function rowToggle(code, type, btn) {
  const isOn   = btn.classList.contains('on');
  const newVal = !isOn;

  const row  = document.getElementById('row-' + code);
  const btns = row.querySelectorAll('.row-toggle');
  const otherOn = [...btns].some(b => b !== btn && b.classList.contains('on'));
  if (!newVal && !otherOn) {
    btn.style.outline = '2px solid #fc8181';
    setTimeout(() => btn.style.outline = '', 1200);
    return;
  }

  const payload = {};
  payload[type === 'public' ? 'public_enabled' : 'internal_enabled'] = newVal;
  const res = await fetch('/urls/' + code, {method:'PATCH', headers:{'Content-Type':'application/json'}, body:JSON.stringify(payload)});
  if (!res.ok) return;

  btn.classList.toggle('on', newVal);
  btn.classList.toggle('off', !newVal);

  const links = row.querySelectorAll('.td-links a');
  const idx   = type === 'public' ? 0 : 1;
  links[idx].classList.toggle('disabled', !newVal);
  if (type === 'public') {
    if (newVal) { links[idx].href = links[idx].textContent; links[idx].target = '_blank'; }
    else        { links[idx].removeAttribute('href'); links[idx].removeAttribute('target'); }
  }
}

/* ── edit ── */
function startEdit(code, currentURL) {
  const cell = document.getElementById('orig-' + code);
  cell.innerHTML =
    '<input class="edit-input" id="edit-input-' + code + '" value="' + currentURL.replace(/"/g,'&quot;') + '">' +
    '<div class="act-row">' +
      '<button class="action-btn btn-save"   onclick="saveEdit(\'' + code + '\')">Save</button>' +
      '<button class="action-btn btn-cancel" onclick="cancelEdit(\'' + code + '\',\'' + currentURL.replace(/'/g,"\\'") + '\')">Cancel</button>' +
    '</div>';
  document.getElementById('edit-input-' + code).focus();
}

async function saveEdit(code) {
  const input = document.getElementById('edit-input-' + code);
  const newURL = input.value.trim();
  if (!newURL) return;
  const res = await fetch('/urls/' + code, {method:'PATCH', headers:{'Content-Type':'application/json'}, body:JSON.stringify({long_url: newURL})});
  if (!res.ok) { input.style.borderColor = '#fc8181'; return; }
  const cell = document.getElementById('orig-' + code);
  const short = newURL.length > 55 ? newURL.slice(0,55)+'…' : newURL;
  cell.innerHTML = '<a href="' + newURL + '" target="_blank" style="color:#2b6cb0">' + short + '</a>';
  // also update destination of public link
  const row = document.getElementById('row-' + code);
  const pubLink = row.querySelector('.td-links a');
  if (pubLink && !pubLink.classList.contains('disabled')) pubLink.href = '/' + code;
}

function cancelEdit(code, originalURL) {
  const cell  = document.getElementById('orig-' + code);
  const short = originalURL.length > 55 ? originalURL.slice(0,55)+'…' : originalURL;
  cell.innerHTML = '<a href="' + originalURL + '" target="_blank" style="color:#2b6cb0">' + short + '</a>';
}

/* ── delete ── */
async function deleteRow(code) {
  if (!confirm('Delete this short URL? This cannot be undone.')) return;
  const res = await fetch('/urls/' + code, {method:'DELETE'});
  if (res.ok) document.getElementById('row-' + code).remove();
}
</script>
</body>
</html>
`
)

// migrations is an ordered list of SQL statements, one per schema version.
// Index 0 = migration to version 1, index 1 = migration to version 2, etc.
// Never edit existing entries — only append new ones.
var migrations = []string{
	// v1: initial schema
	`CREATE TABLE IF NOT EXISTS urls (
		code             TEXT PRIMARY KEY,
		long_url         TEXT NOT NULL,
		public_enabled   INTEGER NOT NULL DEFAULT 1,
		internal_enabled INTEGER NOT NULL DEFAULT 1,
		created_at       TEXT NOT NULL
	)`,
	// v2: settings table for configurable hostnames
	`CREATE TABLE IF NOT EXISTS settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`,
}

func loadSettings() error {
	// Start from env-var defaults
	publicBase := envOr("BASE_URL", "http://localhost")
	uiHost := envOr("UI_HOST", "links.localhost")
	internalHost := envOr("INTERNAL_HOST", "go")
	aliasHost := envOr("ALIAS_HOST", "")

	// Override with any values stored in DB
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

func initDB() error {
	var err error
	db, err = sql.Open("sqlite", dbFile)
	if err != nil {
		return err
	}

	// SQLite WAL mode for safer concurrent access
	if _, err = db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return fmt.Errorf("set WAL mode: %w", err)
	}

	// Read current schema version
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}

	pending := migrations[version:]
	if len(pending) == 0 {
		return nil
	}

	// Apply each pending migration inside its own transaction
	for i, stmt := range pending {
		next := version + i + 1
		if err = applyMigration(next, stmt); err != nil {
			return fmt.Errorf("migration to v%d: %w", next, err)
		}
		log.Printf("db: migrated to schema v%d", next)
	}
	return nil
}

func applyMigration(targetVersion int, stmt string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err = tx.Exec(stmt); err != nil {
		return err
	}
	// PRAGMA user_version cannot be set inside a parameterised query
	if _, err = tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", targetVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

func generateCode() (string, error) {
	code := make([]byte, codeLen)
	for i := range code {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		code[i] = charset[n.Int64()]
	}
	return string(code), nil
}

func saveURL(code, longURL string, publicEnabled, internalEnabled bool) error {
	pub, int_ := 0, 0
	if publicEnabled {
		pub = 1
	}
	if internalEnabled {
		int_ = 1
	}
	_, err := db.Exec(
		"INSERT INTO urls (code, long_url, public_enabled, internal_enabled, created_at) VALUES (?, ?, ?, ?, ?)",
		code, longURL, pub, int_, time.Now().UTC().Format("2006-01-02 15:04:05"),
	)
	return err
}

type urlRecord struct {
	LongURL         string
	PublicEnabled   bool
	InternalEnabled bool
}

func getRecord(code string) (urlRecord, error) {
	var r urlRecord
	var pub, int_ int
	err := db.QueryRow(
		"SELECT long_url, public_enabled, internal_enabled FROM urls WHERE code = ?", code,
	).Scan(&r.LongURL, &pub, &int_)
	r.PublicEnabled = pub == 1
	r.InternalEnabled = int_ == 1
	return r, err
}

type URLRow struct {
	Code            string
	LongURL         string
	PublicEnabled   bool
	InternalEnabled bool
	CreatedAt       string
}

func getAllURLs() ([]URLRow, error) {
	rows, err := db.Query(
		`SELECT code, long_url, public_enabled, internal_enabled, created_at
		 FROM urls ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var urls []URLRow
	for rows.Next() {
		var r URLRow
		var pub, int_ int
		if err := rows.Scan(&r.Code, &r.LongURL, &pub, &int_, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.PublicEnabled = pub == 1
		r.InternalEnabled = int_ == 1
		urls = append(urls, r)
	}
	return urls, rows.Err()
}

var indexTmpl = template.Must(
	template.New("index").Funcs(template.FuncMap{
		"truncate": func(s string, n int) string {
			if len(s) <= n {
				return s
			}
			return s[:n] + "..."
		},
	}).Parse(indexTmplSrc),
)

func renderIndex(w http.ResponseWriter, r *http.Request) {
	urls, _ := getAllURLs()
	pb, _, uh, ih, ah := cfg.snapshot()

	data := struct {
		URLs         []URLRow
		Base         string
		UIHost       string
		InternalHost string
		AliasHost    string
	}{URLs: urls, Base: pb, UIHost: uh, InternalHost: ih, AliasHost: ah}

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
	resp := map[string]interface{}{
		"code":             code,
		"long_url":         longURL,
		"public_enabled":   publicEnabled,
		"internal_enabled": internalEnabled,
	}
	if publicEnabled {
		resp["short_url"] = fmt.Sprintf("%s/%s", pb, code)
	}
	if internalEnabled {
		resp["internal_url"] = fmt.Sprintf("%s/%s", ih, code)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

func updateURL(code string, longURL *string, publicEnabled, internalEnabled *bool) error {
	if longURL != nil {
		if _, err := db.Exec("UPDATE urls SET long_url = ? WHERE code = ?", *longURL, code); err != nil {
			return err
		}
	}
	if publicEnabled != nil {
		v := 0
		if *publicEnabled {
			v = 1
		}
		if _, err := db.Exec("UPDATE urls SET public_enabled = ? WHERE code = ?", v, code); err != nil {
			return err
		}
	}
	if internalEnabled != nil {
		v := 0
		if *internalEnabled {
			v = 1
		}
		if _, err := db.Exec("UPDATE urls SET internal_enabled = ? WHERE code = ?", v, code); err != nil {
			return err
		}
	}
	return nil
}

func deleteURL(code string) error {
	res, err := db.Exec("DELETE FROM urls WHERE code = ?", code)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// urlsHandler handles PATCH and DELETE for /urls/{code}
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

		// Fetch current state to validate at-least-one-active rule
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

		// Rename code: insert with new code, delete old (code is PK)
		if body.NewCode != nil {
			newCode := strings.TrimSpace(*body.NewCode)
			if !validCode.MatchString(newCode) {
				jsonError(w, http.StatusBadRequest, "code must be 1–32 chars: letters, numbers, hyphens, underscores")
				return
			}
			tx, err := db.Begin()
			if err != nil {
				jsonError(w, http.StatusInternalServerError, "database error")
				return
			}
			defer tx.Rollback()
			lu := rec.LongURL
			if body.LongURL != nil {
				lu = *body.LongURL
			}
			pub, int_ := 0, 0
			if nextPub {
				pub = 1
			}
			if nextInt {
				int_ = 1
			}
			if _, err := tx.Exec(
				"INSERT INTO urls (code, long_url, public_enabled, internal_enabled, created_at) SELECT ?, ?, ?, ?, created_at FROM urls WHERE code = ?",
				newCode, lu, pub, int_, code,
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

// apiRouter serves the management API — used by both the UI host and internal host.
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

// uiRouter: web UI host — only UI + API, no redirects.
func uiRouter(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		renderIndex(w, r)
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
	if apiRouter(w, r) {
		return
	}
	code := strings.TrimPrefix(r.URL.Path, "/")
	doRedirect(w, r, code, true)
}

func mainHandler(w http.ResponseWriter, r *http.Request) {
	host := hostOnly(r.Host)
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

func main() {
	if err := initDB(); err != nil {
		log.Fatalf("failed to init database: %v", err)
	}
	defer db.Close()

	if err := loadSettings(); err != nil {
		log.Fatalf("failed to load settings: %v", err)
	}

	pb, ph, uh, ih, ah := cfg.snapshot()
	log.Printf("public: %s (%s)  ui: %s  internal: %s  alias: %s", pb, ph, uh, ih, ah)

	http.HandleFunc("/", mainHandler)
	log.Fatal(http.ListenAndServe(port, nil))
}
