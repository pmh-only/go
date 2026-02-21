/* ── modals ── */
function openModal(id) {
  document.getElementById(id).classList.add('open');
}

function closeModal(id) {
  document.getElementById(id).classList.remove('open');
}

document.addEventListener('DOMContentLoaded', () => {
  // Close on backdrop click
  document.querySelectorAll('.modal-overlay').forEach(el => {
    el.addEventListener('click', e => { if (e.target === el) closeModal(el.id); });
  });
  // Close on Escape
  document.addEventListener('keydown', e => {
    if (e.key === 'Escape') {
      document.querySelectorAll('.modal-overlay.open').forEach(el => closeModal(el.id));
    }
  });

  // Auto-fill URL input from clipboard if it looks like a URL
  const urlInput = document.getElementById('urlInput');
  if (navigator.clipboard?.readText) {
    navigator.clipboard.readText().then(text => {
      text = text.trim();
      if (text.startsWith('http://') || text.startsWith('https://')) {
        urlInput.value = text;
        urlInput.select();
      }
    }).catch(() => {});
  }
});

/* ── helpers ── */
function stripScheme(url) {
  return url.replace(/^https?:\/\//, '');
}

/* ── click-to-copy link ── */
function copyLink(e, el) {
  e.preventDefault();
  if (el.classList.contains('disabled')) return;
  const url = el.dataset.url;
  if (!url || !navigator.clipboard?.writeText) return;
  navigator.clipboard.writeText(url).then(() => {
    el.classList.add('copied');
    setTimeout(() => el.classList.remove('copied'), 1500);
  }).catch(() => {});
}

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

  const url      = document.getElementById('urlInput').value.trim();
  const alias    = document.getElementById('aliasInput').value.trim();
  const resultEl = document.getElementById('result');
  resultEl.innerHTML = '';

  const payload = { url, public_enabled: pub, internal_enabled: int_ };
  if (alias) payload.custom_code = alias;

  try {
    const res  = await fetch('/shorten', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) });
    const data = await res.json();
    if (!res.ok) {
      resultEl.innerHTML = '<div class="result error"><div class="rlabel">Error</div>' + (data.error || 'Something went wrong') + '</div>';
      return;
    }
    const pubUrl = data.alias_url || data.short_url;
    let rows = '';
    if (pubUrl)            rows += urlRow('pub-' + data.code, 'public',   pubUrl,            true);
    if (data.internal_url) rows += urlRow('int-' + data.code, 'internal', data.internal_url, false);
    resultEl.innerHTML = '<div class="result success"><div class="rlabel">Your links</div>' + rows + '</div>';

    // Copy the primary URL to clipboard automatically (prefer alias)
    const toCopy = data.alias_url || data.short_url || data.internal_url;
    if (toCopy && navigator.clipboard?.writeText) navigator.clipboard.writeText(toCopy).catch(() => {});

    setTimeout(() => location.reload(), 2000);
  } catch {
    resultEl.innerHTML = '<div class="result error"><div class="rlabel">Error</div>Network error.</div>';
  }
}

function urlRow(id, type, text, isHref) {
  const tag     = '<span class="url-tag tag-' + type + ' on">' + type + '</span>';
  const display = stripScheme(text);
  const a = isHref
    ? '<a id="' + id + '" href="' + text + '" target="_blank" data-url="' + text + '" onclick="copyLink(event,this)">' + display + '</a>'
    : '<a id="' + id + '" data-url="' + text + '" onclick="copyLink(event,this)">' + display + '</a>';
  return '<div class="url-row">' + tag + a + '</div>';
}

/* ── edit code (ID) — inline ── */
function startEditCode(code) {
  const disp = document.getElementById('code-display-' + code);
  disp.style.display = 'none';
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
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ code: newCode }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    document.getElementById('code-input-' + oldCode).style.borderColor = '#fc8181';
    document.getElementById('code-input-' + oldCode).title = data.error || 'Error';
    return;
  }
  const row = document.getElementById('row-' + oldCode);
  row.id = 'row-' + newCode;
  document.getElementById('code-edit-' + oldCode).remove();
  const disp = document.getElementById('code-display-' + oldCode);
  disp.id = 'code-display-' + newCode;
  disp.querySelector('.code-label').textContent = newCode;
  disp.style.display = '';
  const pb = document.getElementById('pub-link-' + oldCode);
  const ib = document.getElementById('int-link-' + oldCode);
  if (pb) {
    pb.id = 'pub-link-' + newCode;
    pb.textContent = pb.textContent.replace(oldCode, newCode);
    pb.dataset.url  = pb.dataset.url.replace(oldCode, newCode);
    if (pb.getAttribute('href')) pb.setAttribute('href', pb.dataset.url);
  }
  if (ib) {
    ib.id = 'int-link-' + newCode;
    ib.textContent  = ib.textContent.replace(oldCode, newCode);
    ib.dataset.url  = ib.dataset.url.replace(oldCode, newCode);
  }
  row.querySelectorAll('[onclick]').forEach(el => {
    el.setAttribute('onclick', el.getAttribute('onclick').replaceAll("'" + oldCode + "'", "'" + newCode + "'"));
  });
}

function cancelEditCode(code) {
  const editDiv = document.getElementById('code-edit-' + code);
  if (editDiv) editDiv.remove();
  document.getElementById('code-display-' + code).style.display = '';
}

/* ── search / filter ── */
function filterRows(q) {
  const term = q.trim().toLowerCase();
  const rows = document.querySelectorAll('tbody tr');
  let visible = 0;
  rows.forEach(row => {
    const show = !term || row.textContent.toLowerCase().includes(term);
    row.style.display = show ? '' : 'none';
    if (show) visible++;
  });
  const label = document.getElementById('countLabel');
  if (label) label.textContent = (term ? visible + ' of ' + rows.length : rows.length) + ' entries';
}

/* ── settings modal ── */
async function saveSettings() {
  const payload = {
    public_base:   document.getElementById('cfgPublicBase').value.trim(),
    ui_host:       document.getElementById('cfgUIHost').value.trim(),
    internal_host: document.getElementById('cfgInternalHost').value.trim(),
    alias_host:    document.getElementById('cfgAliasHost').value.trim(),
  };
  const res = await fetch('/settings', { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) });
  const fb  = document.getElementById('settingsFeedback');
  fb.style.display = '';
  if (res.ok) {
    fb.textContent = 'Saved!'; fb.style.color = '#276749';
    setTimeout(() => closeModal('modalSettings'), 800);
  } else {
    fb.textContent = 'Error saving.'; fb.style.color = '#c53030';
  }
  setTimeout(() => { fb.style.display = 'none'; }, 2500);
}

/* ── row visibility toggle ── */
async function rowToggle(code, type, btn) {
  const isOn    = btn.classList.contains('on');
  const newVal  = !isOn;
  const row     = document.getElementById('row-' + code);
  const btns    = row.querySelectorAll('.row-toggle');
  const otherOn = [...btns].some(b => b !== btn && b.classList.contains('on'));
  if (!newVal && !otherOn) {
    btn.style.outline = '2px solid #fc8181';
    setTimeout(() => btn.style.outline = '', 1200);
    return;
  }
  const payload = {};
  payload[type === 'public' ? 'public_enabled' : 'internal_enabled'] = newVal;
  const res = await fetch('/urls/' + code, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) });
  if (!res.ok) return;
  btn.classList.toggle('on', newVal);
  btn.classList.toggle('off', !newVal);
  const links = row.querySelectorAll('.td-links a');
  const idx   = type === 'public' ? 0 : 1;
  links[idx].classList.toggle('disabled', !newVal);
  if (type === 'public') {
    if (newVal) { links[idx].setAttribute('href', links[idx].dataset.url); links[idx].target = '_blank'; }
    else        { links[idx].removeAttribute('href'); links[idx].removeAttribute('target'); }
  }
}

/* ── edit destination URL — modal ── */
let currentEditCode = null;

function startEdit(code, currentURL) {
  currentEditCode = code;
  const inp = document.getElementById('editUrlInput');
  inp.value = currentURL;
  inp.style.borderColor = '';
  document.getElementById('editFeedback').style.display = 'none';
  openModal('modalEdit');
  setTimeout(() => inp.focus(), 50);
}

async function confirmEdit() {
  const newURL = document.getElementById('editUrlInput').value.trim();
  if (!newURL) return;
  const res = await fetch('/urls/' + currentEditCode, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ long_url: newURL }),
  });
  if (!res.ok) {
    document.getElementById('editUrlInput').style.borderColor = '#fc8181';
    const fb = document.getElementById('editFeedback');
    fb.textContent = 'Failed to save.'; fb.style.color = '#c53030'; fb.style.display = '';
    return;
  }
  const cell  = document.getElementById('orig-' + currentEditCode);
  const short = newURL.length > 55 ? newURL.slice(0, 55) + '…' : newURL;
  cell.innerHTML = '<a href="' + newURL + '" target="_blank" style="color:#2b6cb0">' + short + '</a>';
  closeModal('modalEdit');
}

/* ── delete — modal ── */
let currentDeleteCode = null;

function deleteRow(code) {
  currentDeleteCode = code;
  document.getElementById('deleteModalCode').textContent = code;
  openModal('modalDelete');
}

async function confirmDelete() {
  const res = await fetch('/urls/' + currentDeleteCode, { method: 'DELETE' });
  if (res.ok) document.getElementById('row-' + currentDeleteCode).remove();
  closeModal('modalDelete');
}
