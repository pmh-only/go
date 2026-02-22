/* ── modals ── */
function openModal(id) {
  document.getElementById(id).classList.add("open");
}

function closeModal(id) {
  document.getElementById(id).classList.remove("open");
}

document.addEventListener("DOMContentLoaded", () => {
  // Close on backdrop click
  document.querySelectorAll(".modal-overlay").forEach((el) => {
    el.addEventListener("click", (e) => {
      if (e.target === el) closeModal(el.id);
    });
  });
  // Close on Escape
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      document
        .querySelectorAll(".modal-overlay.open")
        .forEach((el) => closeModal(el.id));
    }
  });

  // Auto-fill URL input from clipboard if it looks like a URL
  const urlInput = document.getElementById("urlInput");
  if (navigator.clipboard?.readText) {
    navigator.clipboard
      .readText()
      .then((text) => {
        text = text.trim();
        if (text.startsWith("http://") || text.startsWith("https://")) {
          urlInput.value = text;
          urlInput.select();
        }
      })
      .catch(() => {});
  }
});

/* ── helpers ── */
function stripScheme(url) {
  return url.replace(/^https?:\/\//, "");
}

function formatExpiryDisplay(iso) {
  if (!iso) return "";
  const d = new Date(iso);
  const now = new Date();
  const expired = d <= now;
  const label = expired ? "Expired" : "Expires";
  return `<span class="${expired ? "expired" : ""}">${label}: ${d.toLocaleString()}</span>`;
}

/* ── click-to-copy link ── */
function copyLink(e, el) {
  e.preventDefault();
  if (el.classList.contains("disabled")) return;
  const url = el.dataset.url;
  if (!url || !navigator.clipboard?.writeText) return;
  navigator.clipboard
    .writeText(url)
    .then(() => {
      el.classList.add("copied");
      setTimeout(() => el.classList.remove("copied"), 1500);
    })
    .catch(() => {});
}

/* ── redirect type ── */
function onRedirectType(radio) {
  const isJs = radio.value === "js";
  const isMeta = radio.value === "meta";
  document.getElementById("ogSection").style.display =
    isJs || isMeta ? "" : "none";
  document.getElementById("passwordSection").style.display =
    isJs ? "" : "none";
}

function onEditRedirectType(radio) {
  const isJs = radio.value === "js";
  const isMeta = radio.value === "meta";
  document.getElementById("editOgSection").style.display =
    isJs || isMeta ? "" : "none";
  document.getElementById("editPasswordSection").style.display =
    isJs ? "" : "none";
}

let editPasswordCleared = false;

function clearEditPassword() {
  editPasswordCleared = true;
  document.getElementById("editPassword").value = "";
  document.getElementById("editPassword").placeholder = "No password (cleared)";
  document.getElementById("editClearPwBtn").style.display = "none";
}

function clearEditExpires() {
  document.getElementById("editExpiresInput").value = "";
  document.getElementById("editClearExpiresBtn").style.display = "none";
}

/* ── form toggles ── */
function onToggle() {
  const pub = document.getElementById("chkPublic");
  const int_ = document.getElementById("chkInternal");
  document.getElementById("togglePublic").classList.toggle("on", pub.checked);
  document.getElementById("togglePublic").classList.toggle("off", !pub.checked);
  document
    .getElementById("toggleInternal")
    .classList.toggle("on", int_.checked);
  document
    .getElementById("toggleInternal")
    .classList.toggle("off", !int_.checked);
  document.getElementById("toggleErr").style.display =
    !pub.checked && !int_.checked ? "" : "none";
}

/* ── shorten ── */
async function shorten(e) {
  e.preventDefault();
  const pub = document.getElementById("chkPublic").checked;
  const int_ = document.getElementById("chkInternal").checked;
  if (!pub && !int_) {
    document.getElementById("toggleErr").style.display = "";
    return;
  }

  const url = document.getElementById("urlInput").value.trim();
  const alias = document.getElementById("aliasInput").value.trim();
  const redirectType =
    document.querySelector('input[name="redirectType"]:checked')?.value ||
    "redirect";
  const resultEl = document.getElementById("result");
  resultEl.innerHTML = "";

  const expiresLocal = document.getElementById("expiresInput").value;
  const payload = {
    url,
    public_enabled: pub,
    internal_enabled: int_,
    redirect_type: redirectType,
    og_title: document.getElementById("ogTitle").value.trim(),
    og_description: document.getElementById("ogDescription").value.trim(),
    og_image: document.getElementById("ogImage").value.trim(),
    password: document.getElementById("passwordInput").value,
    description: document.getElementById("descInput").value.trim(),
    expires_at: expiresLocal ? new Date(expiresLocal).toISOString() : "",
  };
  if (alias) payload.custom_code = alias;

  try {
    const res = await fetch("/shorten", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    const data = await res.json();
    if (!res.ok) {
      resultEl.innerHTML =
        '<div class="result error"><div class="rlabel">Error</div>' +
        (data.error || "Something went wrong") +
        "</div>";
      return;
    }
    resultEl.innerHTML = "";

    // Copy the primary URL to clipboard automatically (prefer alias)
    const toCopy = data.alias_url || data.short_url || data.internal_url;
    if (toCopy && navigator.clipboard?.writeText)
      navigator.clipboard.writeText(toCopy).catch(() => {});

    // Reset form
    document.getElementById("urlInput").value = "";
    document.getElementById("aliasInput").value = "";
    document.getElementById("ogTitle").value = "";
    document.getElementById("ogDescription").value = "";
    document.getElementById("ogImage").value = "";
    document.getElementById("rtypeRedirect").checked = true;
    document.getElementById("ogSection").style.display = "none";
    document.getElementById("passwordInput").value = "";
    document.getElementById("passwordSection").style.display = "none";
    document.getElementById("descInput").value = "";
    document.getElementById("expiresInput").value = "";

    // Insert new row at top of table
    insertNewRow(data);
  } catch {
    resultEl.innerHTML =
      '<div class="result error"><div class="rlabel">Error</div>Network error.</div>';
  }
}

function insertNewRow(data) {
  const code = data.code;
  const longURL = data.long_url;
  const pubUrl = data.alias_url || data.short_url || "";
  const intUrl = data.internal_url || "";
  const pubEnabled = !!pubUrl;
  const intEnabled = !!intUrl;
  const redirectType = data.redirect_type || "redirect";
  const desc = data.description || "";
  const expiresAt = data.expires_at || "";

  const shortLong = longURL.length > 55 ? longURL.slice(0, 55) + "…" : longURL;
  const pubDisplay = stripScheme(pubUrl);

  const pubLink = pubEnabled
    ? `<a href="${pubUrl}" target="_blank" data-url="${pubUrl}" onclick="copyLink(event,this)" id="pub-link-${code}">${pubDisplay}</a>`
    : `<a class="disabled" data-url="${pubUrl}" onclick="copyLink(event,this)" id="pub-link-${code}">${pubDisplay}</a>`;
  const intDisplay = stripScheme(intUrl);
  const intLink = intEnabled
    ? `<a data-url="${intDisplay}" onclick="copyLink(event,this)" id="int-link-${code}">${intDisplay}</a>`
    : `<a class="disabled" data-url="${intDisplay}" onclick="copyLink(event,this)" id="int-link-${code}">${intDisplay}</a>`;
  const pubToggle = `<button class="row-toggle tag-public ${pubEnabled ? "on" : "off"}" onclick="rowToggle('${code}','public',this)" title="Toggle public link">P</button>`;
  const intToggle = `<button class="row-toggle tag-internal ${intEnabled ? "on" : "off"}" onclick="rowToggle('${code}','internal',this)" title="Toggle internal link">I</button>`;
  const metaBadge =
    redirectType === "meta"
      ? `<span class="rtype-badge">META</span>`
      : redirectType === "js"
        ? `<span class="rtype-badge rtype-badge--js">JS</span>`
        : "";

  const longURLEscaped = longURL.replace(/'/g, "\\'");
  const tr = document.createElement("tr");
  tr.id = "row-" + code;
  tr.className = "row-new";
  tr.dataset.rtype = redirectType;
  tr.dataset.ogTitle = data.og_title || "";
  tr.dataset.ogDesc = data.og_description || "";
  tr.dataset.ogImage = data.og_image || "";
  tr.dataset.hasPassword = data.has_password ? "true" : "false";
  tr.dataset.desc = desc;
  tr.dataset.expiresAt = expiresAt;
  tr.innerHTML = `
    <td class="td-links">
      <div class="link-line">${pubToggle}${pubLink}${metaBadge}</div>
      <div class="link-line">${intToggle}${intLink}</div>
    </td>
    <td class="td-original" id="orig-${code}">
      <a href="${longURL}" target="_blank" style="color:#2b6cb0">${shortLong}</a>
      ${desc ? `<div class="desc-text">${desc.replace(/&/g,"&amp;").replace(/</g,"&lt;")}</div>` : ""}
    </td>
    <td class="td-date">just now${expiresAt ? `<div class="expires-text">${formatExpiryDisplay(expiresAt)}</div>` : ""}</td>
    <td class="td-actions">
        <div class="act-row">
          <button class="action-btn btn-qr"    onclick="showQR('${code}')"                    title="QR code">
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="3" height="3"/><rect x="19" y="14" width="2" height="2"/><rect x="14" y="19" width="2" height="2"/><rect x="19" y="19" width="2" height="2"/></svg>
          </button>
          <button class="action-btn btn-edit"  onclick="startEdit('${code}','${longURLEscaped}')" title="Edit">
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>
          </button>
          <button class="action-btn btn-delete" onclick="deleteRow('${code}')"                 title="Delete">
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/></svg>
          </button>
        </div>
    </td>`;

  let tbody = document.getElementById("linksBody");
  if (!tbody) {
    // First ever link — replace empty-state with a full table
    const emptyState = document.getElementById("emptyState");
    const tableWrap = emptyState.parentNode;
    emptyState.remove();
    tableWrap.innerHTML =
      "<table><thead><tr><th>Links</th><th>Original</th><th>Created</th><th>Actions</th></tr></thead>" +
      '<tbody id="linksBody"></tbody></table>';
    tbody = document.getElementById("linksBody");
  }

  tbody.insertBefore(tr, tbody.firstChild);

  // Update count label
  const label = document.getElementById("countLabel");
  if (label) {
    const total = tbody.querySelectorAll("tr").length;
    label.textContent = total + " entries";
  }
}

/* ── search / filter ── */
function filterRows(q) {
  const term = q.trim().toLowerCase();
  const rows = document.querySelectorAll("tbody tr");
  let visible = 0;
  rows.forEach((row) => {
    const show = !term || row.textContent.toLowerCase().includes(term);
    row.style.display = show ? "" : "none";
    if (show) visible++;
  });
  const label = document.getElementById("countLabel");
  if (label)
    label.textContent =
      (term ? visible + " of " + rows.length : rows.length) + " entries";
}

/* ── settings modal ── */
async function saveSettings() {
  const payload = {
    public_base: document.getElementById("cfgPublicBase").value.trim(),
    ui_host: document.getElementById("cfgUIHost").value.trim(),
    internal_host: document.getElementById("cfgInternalHost").value.trim(),
    alias_host: document.getElementById("cfgAliasHost").value.trim(),
    public_api_host: document.getElementById("cfgPublicAPIHost").value.trim(),
  };
  const res = await fetch("/settings", {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  const fb = document.getElementById("settingsFeedback");
  fb.style.display = "";
  if (res.ok) {
    fb.textContent = "Saved!";
    fb.style.color = "#276749";
    setTimeout(() => closeModal("modalSettings"), 800);
  } else {
    fb.textContent = "Error saving.";
    fb.style.color = "#c53030";
  }
  setTimeout(() => {
    fb.style.display = "none";
  }, 2500);
}

/* ── QR code modal ── */
let currentQRCode = null;

function showQR(code) {
  currentQRCode = code;
  const img = document.getElementById("qrImage");
  img.src = "/qr/" + code;
  document.getElementById("qrViewLink").href = "/qr/" + code;
  document.getElementById("qrFeedback").style.display = "none";
  openModal("modalQR");
}

async function copyQR() {
  try {
    const res = await fetch("/qr/" + currentQRCode);
    const blob = await res.blob();
    await navigator.clipboard.write([new ClipboardItem({ "image/png": blob })]);
    const fb = document.getElementById("qrFeedback");
    fb.textContent = "Copied!";
    fb.style.color = "#276749";
    fb.style.display = "";
    setTimeout(() => {
      fb.style.display = "none";
    }, 2000);
  } catch {
    const fb = document.getElementById("qrFeedback");
    fb.textContent = "Copy failed — try Download instead.";
    fb.style.color = "#c53030";
    fb.style.display = "";
  }
}

function downloadQR() {
  const a = document.createElement("a");
  a.href = "/qr/" + currentQRCode;
  a.download = currentQRCode + "-qr.png";
  a.click();
}

/* ── row visibility toggle ── */
async function rowToggle(code, type, btn) {
  const isOn = btn.classList.contains("on");
  const newVal = !isOn;
  const row = document.getElementById("row-" + code);
  const payload = {};
  payload[type === "public" ? "public_enabled" : "internal_enabled"] = newVal;
  const res = await fetch("/urls/" + code, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  if (!res.ok) return;
  btn.classList.toggle("on", newVal);
  btn.classList.toggle("off", !newVal);
  const links = row.querySelectorAll(".td-links a");
  const idx = type === "public" ? 0 : 1;
  links[idx].classList.toggle("disabled", !newVal);
  if (type === "public") {
    if (newVal) {
      links[idx].setAttribute("href", links[idx].dataset.url);
      links[idx].target = "_blank";
    } else {
      links[idx].removeAttribute("href");
      links[idx].removeAttribute("target");
    }
  }
}

/* ── edit destination URL — modal ── */
let currentEditCode = null;

function startEdit(code, currentURL) {
  currentEditCode = code;
  const codeInp = document.getElementById("editCodeInput");
  codeInp.value = code;
  codeInp.style.borderColor = "";
  const urlInp = document.getElementById("editUrlInput");
  urlInp.value = currentURL;
  urlInp.style.borderColor = "";
  document.getElementById("editFeedback").style.display = "none";

  // Restore redirect type + OG + password from row data attributes
  editPasswordCleared = false;
  const row = document.getElementById("row-" + code);
  const rtype = row?.dataset.rtype || "redirect";
  const hasPassword = row?.dataset.hasPassword === "true";
  document.getElementById("editRtypeRedirect").checked = rtype === "redirect";
  document.getElementById("editRtypeMeta").checked = rtype === "meta";
  document.getElementById("editRtypeJs").checked = rtype === "js";
  document.getElementById("editOgSection").style.display =
    rtype === "meta" || rtype === "js" ? "" : "none";
  document.getElementById("editDescInput").value = row?.dataset.desc || "";
  document.getElementById("editOgTitle").value = row?.dataset.ogTitle || "";
  document.getElementById("editOgDescription").value =
    row?.dataset.ogDesc || "";
  document.getElementById("editOgImage").value = row?.dataset.ogImage || "";
  document.getElementById("editPasswordSection").style.display =
    rtype === "js" ? "" : "none";
  const pwInput = document.getElementById("editPassword");
  const clearBtn = document.getElementById("editClearPwBtn");
  pwInput.value = "";
  pwInput.placeholder = hasPassword
    ? "New password (leave blank to keep)"
    : "Set password (optional)";
  clearBtn.style.display = hasPassword ? "" : "none";

  const expiresAt = row?.dataset.expiresAt || "";
  const editExpires = document.getElementById("editExpiresInput");
  const clearExpiresBtn = document.getElementById("editClearExpiresBtn");
  if (expiresAt) {
    // Convert UTC ISO to local datetime-local value
    const d = new Date(expiresAt);
    editExpires.value = new Date(d.getTime() - d.getTimezoneOffset() * 60000)
      .toISOString()
      .slice(0, 16);
    clearExpiresBtn.style.display = "";
  } else {
    editExpires.value = "";
    clearExpiresBtn.style.display = "none";
  }

  openModal("modalEdit");
  setTimeout(() => codeInp.focus(), 50);
}

async function confirmEdit() {
  const newCode = document.getElementById("editCodeInput").value.trim();
  const newURL = document.getElementById("editUrlInput").value.trim();
  if (!newURL) return;

  const rtype =
    document.querySelector('input[name="editRedirectType"]:checked')?.value ||
    "redirect";
  const expiresLocal = document.getElementById("editExpiresInput").value;
  const body = {
    long_url: newURL,
    description: document.getElementById("editDescInput").value.trim(),
    redirect_type: rtype,
    og_title: document.getElementById("editOgTitle").value.trim(),
    og_description: document.getElementById("editOgDescription").value.trim(),
    og_image: document.getElementById("editOgImage").value.trim(),
    expires_at: expiresLocal ? new Date(expiresLocal).toISOString() : "",
  };
  if (rtype === "js") {
    if (editPasswordCleared) {
      body.password = "";
    } else {
      const pw = document.getElementById("editPassword").value;
      if (pw) body.password = pw;
    }
  }
  if (newCode && newCode !== currentEditCode) body.code = newCode;

  const res = await fetch("/urls/" + currentEditCode, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    const fb = document.getElementById("editFeedback");
    fb.textContent = data.error || "Failed to save.";
    fb.style.color = "#c53030";
    fb.style.display = "";
    if (body.code)
      document.getElementById("editCodeInput").style.borderColor = "#fc8181";
    else document.getElementById("editUrlInput").style.borderColor = "#fc8181";
    return;
  }

  const effectiveCode = body.code || currentEditCode;

  // Update destination URL cell
  const cell = document.getElementById("orig-" + currentEditCode);
  const short = newURL.length > 55 ? newURL.slice(0, 55) + "…" : newURL;
  const descSafe = body.description
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;");
  cell.innerHTML =
    '<a href="' +
    newURL +
    '" target="_blank" style="color:#2b6cb0">' +
    short +
    "</a>" +
    (body.description ? `<div class="desc-text">${descSafe}</div>` : "");

  // If the code changed, rename all code-keyed DOM elements
  if (body.code) {
    const oldCode = currentEditCode;
    const row = document.getElementById("row-" + oldCode);
    row.id = "row-" + effectiveCode;
    cell.id = "orig-" + effectiveCode;
    const pb = document.getElementById("pub-link-" + oldCode);
    const ib = document.getElementById("int-link-" + oldCode);
    if (pb) {
      pb.id = "pub-link-" + effectiveCode;
      pb.textContent = pb.textContent.replace(oldCode, effectiveCode);
      pb.dataset.url = pb.dataset.url.replace(oldCode, effectiveCode);
      if (pb.getAttribute("href")) pb.setAttribute("href", pb.dataset.url);
    }
    if (ib) {
      ib.id = "int-link-" + effectiveCode;
      ib.textContent = ib.textContent.replace(oldCode, effectiveCode);
      ib.dataset.url = ib.dataset.url.replace(oldCode, effectiveCode);
    }
    row.querySelectorAll("[onclick]").forEach((el) => {
      el.setAttribute(
        "onclick",
        el
          .getAttribute("onclick")
          .replaceAll("'" + oldCode + "'", "'" + effectiveCode + "'"),
      );
    });
  }

  // Update row data attributes + redirect badge
  const rowEl = document.getElementById("row-" + effectiveCode);
  if (rowEl) {
    rowEl.dataset.rtype = rtype;
    rowEl.dataset.desc = body.description;
    rowEl.dataset.ogTitle = body.og_title;
    rowEl.dataset.ogDesc = body.og_description;
    rowEl.dataset.ogImage = body.og_image;
    rowEl.dataset.expiresAt = body.expires_at;
    if (body.password !== undefined) {
      rowEl.dataset.hasPassword = body.password ? "true" : "false";
    }
    // Update expiry display in td-date
    const dateCell = rowEl.querySelector(".td-date");
    if (dateCell) {
      let expiryDiv = dateCell.querySelector(".expires-text");
      if (body.expires_at) {
        if (!expiryDiv) {
          expiryDiv = document.createElement("div");
          expiryDiv.className = "expires-text";
          dateCell.appendChild(expiryDiv);
        }
        expiryDiv.innerHTML = formatExpiryDisplay(body.expires_at);
      } else if (expiryDiv) {
        expiryDiv.remove();
      }
    }
  }
  const pubLinkEl = document.getElementById("pub-link-" + effectiveCode);
  if (pubLinkEl) {
    const linkLine = pubLinkEl.closest(".link-line");
    let badge = linkLine.querySelector(".rtype-badge");
    if (rtype === "meta") {
      if (!badge) {
        badge = document.createElement("span");
        linkLine.appendChild(badge);
      }
      badge.className = "rtype-badge";
      badge.textContent = "META";
    } else if (rtype === "js") {
      if (!badge) {
        badge = document.createElement("span");
        linkLine.appendChild(badge);
      }
      badge.className = "rtype-badge rtype-badge--js";
      badge.textContent = "JS";
    } else if (badge) {
      badge.remove();
    }
  }

  closeModal("modalEdit");
}

/* ── delete — modal ── */
let currentDeleteCode = null;

function deleteRow(code) {
  currentDeleteCode = code;
  document.getElementById("deleteModalCode").textContent = code;
  openModal("modalDelete");
}

async function confirmDelete() {
  const res = await fetch("/urls/" + currentDeleteCode, { method: "DELETE" });
  if (res.ok) document.getElementById("row-" + currentDeleteCode).remove();
  closeModal("modalDelete");
}
