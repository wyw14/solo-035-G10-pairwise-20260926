const eventSelect = document.getElementById("event-select");
const operatorInput = document.getElementById("operator");
const chainPanel = document.getElementById("chain-panel");
const emergencyPanel = document.getElementById("emergency-panel");
const materialsPanel = document.getElementById("materials");
const emergenciesPanel = document.getElementById("emergencies");
const evidencePanel = document.getElementById("evidence");
const stamp = document.getElementById("stamp");
const materialForm = document.getElementById("material-form");

let materialCache = new Map();
let lastStatus = null;

function escapeHTML(value) {
  return String(value ?? "").replace(/[&<>"']/g, (ch) => (
    { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[ch]
  ));
}

function formatTime(value) {
  if (!value) return "-";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

function label(key) {
  return String(key).replaceAll("_", " ");
}

function detailRows(obj, exclude = new Set()) {
  return Object.entries(obj)
    .filter(([key, value]) => !exclude.has(key) && value !== null && value !== "" && !(Array.isArray(value) && value.length === 0))
    .map(([key, value]) => `<dt>${escapeHTML(label(key))}</dt><dd>${escapeHTML(typeof value === "object" ? JSON.stringify(value) : value)}</dd>`)
    .join("");
}

async function api(path, options) {
  const response = await fetch(path, Object.assign({
    headers: { "Accept": "application/json", "Content-Type": "application/json" },
  }, options));
  let payload = null;
  try { payload = await response.json(); } catch (_) { /* empty error body */ }
  if (!response.ok) {
    throw new Error(payload && payload.error ? payload.error : `HTTP ${response.status}`);
  }
  return payload;
}

function requireIdentity(role) {
  const identity = operatorInput.value.trim();
  if (!identity) {
    alert(`请先在顶部填写${role}姓名，应急操作必须留下责任人。`);
    operatorInput.focus();
    return null;
  }
  return identity;
}

async function refreshOperations() {
  const payload = await api("/api/operations");
  const previous = eventSelect.value;
  eventSelect.innerHTML = payload.operations.length
    ? payload.operations.map((op) => `<option value="${escapeHTML(op.event_id)}">${escapeHTML(op.name)}（${escapeHTML(op.phase)}）</option>`).join("")
    : `<option value="">当前没有在播节目</option>`;
  if (previous && payload.operations.some((op) => op.event_id === previous)) {
    eventSelect.value = previous;
  }
}

function renderMaterials(materials) {
  materialCache = new Map(materials.map((m) => [m.id, m]));
  materialsPanel.innerHTML = materials.length ? materials.map((m) => `
    <article class="item">
      <h2>${escapeHTML(m.name)}</h2>
      <dl>
        <dt>素材地址</dt><dd>${escapeHTML(m.content_uri)}</dd>
        <dt>校验和</dt><dd>${escapeHTML(m.checksum)}</dd>
        <dt>录入人</dt><dd>${escapeHTML(m.controlled_by)}</dd>
        <dt>适用开始</dt><dd>${escapeHTML(formatTime(m.available_from))}</dd>
        <dt>适用结束</dt><dd>${escapeHTML(formatTime(m.available_to))}</dd>
        <dt>录入时间</dt><dd>${escapeHTML(formatTime(m.created_at))}</dd>
      </dl>
    </article>`).join("") : `<p class="empty">尚未录入受控静态片素材</p>`;
}

function renderEmergencies(emergencies) {
  emergenciesPanel.innerHTML = emergencies.length ? emergencies.map((e) => `
    <article class="item slate-${escapeHTML(e.status)}">
      <h2>${escapeHTML(e.slate_name || "待选静态片")} <span class="badge">${escapeHTML(e.status)}</span></h2>
      <dl>${detailRows(e, new Set(["candidates"]))}</dl>
    </article>`).join("") : `<p class="empty">当前没有应急静态片记录</p>`;
}

function renderEvidence(records) {
  evidencePanel.innerHTML = records.length ? `<ol class="timeline">${records.map((r) => `
    <li class="evidence-${escapeHTML(r.kind)}">
      <strong>${escapeHTML({ "slate-cut-in": "静态片切入", "slate-handover": "交接接管输出", "slate-withdrawn": "静态片撤下" }[r.kind] || r.kind)}</strong>
      <span>${escapeHTML(formatTime(r.created_at))}</span>
      <code>${escapeHTML(r.kind)}</code>
    </li>`).join("")}</ol>` : `<p class="empty">该节目暂无静态片切入 / 交接 / 撤下证据</p>`;
}

function candidateCards(candidateIDs) {
  return candidateIDs.map((id) => materialCache.get(id)).filter(Boolean).map((m) => `
    <article class="item candidate">
      <h2>${escapeHTML(m.name)}</h2>
      <dl>
        <dt>素材地址</dt><dd>${escapeHTML(m.content_uri)}</dd>
        <dt>适用时段</dt><dd>${escapeHTML(formatTime(m.available_from))} 至 ${escapeHTML(formatTime(m.available_to))}</dd>
      </dl>
      <button class="btn btn-danger" data-action="cut-in" data-slate="${escapeHTML(m.id)}>确认切入该静态片</button>
    </article>`).join("");
}

function renderStatus(view) {
  lastStatus = view;
  const chain = view.chain;
  chainPanel.innerHTML = `
    <article class="item ${chain.available ? "" : "slate-on-air"}">
      <h2>${chain.available ? "播出链路可用" : "播出链路不可用"}</h2>
      <dl>${detailRows(chain)}</dl>
    </article>`;

  const emergency = view.emergency;
  if (!emergency) {
    if (chain.available) {
      emergencyPanel.innerHTML = `<p class="empty">主路或音画备路正在提供有效节目，紧急静态片通路保持热备，无需切入。</p>`;
    } else {
      emergencyPanel.innerHTML = `
        <div class="action-bar">
          <input id="reason" type="text" placeholder="失锁原因（可选，随证据留存）">
          <button class="btn btn-danger" id="propose-btn" data-action="propose">链路不可用：生成切入候选</button>
        </div>
        <p class="hint">系统将按当前时间从适用时段内的受控素材中给出候选；候选生成后仍需明确确认才会切入。</p>`;
    }
    return;
  }

  if (emergency.status === "proposed") {
    emergencyPanel.innerHTML = `
      <div class="banner banner-warn">候选已给出，等待明确确认。提案令牌：<code>${escapeHTML(emergency.command)}</code>，提案人：${escapeHTML(emergency.proposed_by)}</div>
      <div class="grid">${candidateCards(emergency.candidates) || `<p class="empty">提案候选素材已不在素材库中</p>`}</div>`;
    return;
  }

  if (emergency.status === "on-air") {
    const options = view.takeovers.map((option) => `
      <article class="item ${option.ready ? "" : "disabled"}">
        <h2>${escapeHTML(option.label)} <span class="badge">${option.ready ? "已准备好" : "未就绪"}</span></h2>
        <dl><dt>接管目标</dt><dd>${escapeHTML(option.id)}</dd><dt>状态</dt><dd>${escapeHTML(option.detail)}</dd></dl>
        ${option.ready ? `<button class="btn" data-action="handover" data-takeover="${escapeHTML(option.id)}">导播选择该输出并撤下静态片</button>` : ""}
      </article>`).join("");
    emergencyPanel.innerHTML = `
      <div class="banner banner-danger">静态片正在播出：${escapeHTML(emergency.slate_name)}（切入人 ${escapeHTML(emergency.confirmed_by)}，${escapeHTML(formatTime(emergency.cut_in_at))}）</div>
      <p class="hint">应急结束时，请由导播选择一个已准备好的接管输出，按先交接后撤下完成恢复。</p>
      <div class="grid">${options}</div>`;
    return;
  }

  if (emergency.status === "handed-over") {
    emergencyPanel.innerHTML = `
      <div class="banner banner-ok">应急结束：已交接至 ${escapeHTML(emergency.takeover_name)}，静态片已撤下。</div>
      <article class="item"><h2>${escapeHTML(emergency.slate_name)}</h2><dl>${detailRows(emergency, new Set(["candidates"]))}</dl></article>`;
  }
}

async function refreshSelected() {
  const eventID = eventSelect.value;
  if (!eventID) {
    chainPanel.innerHTML = `<p class="empty">请选择在播节目</p>`;
    emergencyPanel.innerHTML = `<p class="empty">请选择在播节目</p>`;
    evidencePanel.innerHTML = `<p class="empty">请选择在播节目</p>`;
    return;
  }
  renderStatus(await api(`/api/slates/status?event_id=${encodeURIComponent(eventID)}`));
  const evidence = await api(`/api/slates/evidence?event_id=${encodeURIComponent(eventID)}`);
  renderEvidence(evidence.evidence);
}

async function refreshAll() {
  try {
    await refreshOperations();
    const [materials, emergencies] = await Promise.all([
      api("/api/slates/materials"),
      api("/api/slates/emergencies"),
    ]);
    renderMaterials(materials.materials);
    renderEmergencies(emergencies.emergencies);
    await refreshSelected();
    stamp.textContent = `状态 200 · ${new Date().toLocaleTimeString()}`;
  } catch (error) {
    stamp.textContent = `连接已中断：${error.message}`;
  }
}

eventSelect.addEventListener("change", refreshSelected);

emergencyPanel.addEventListener("click", async (event) => {
  const button = event.target.closest("button[data-action]");
  if (!button || button.disabled) return;
  const eventID = eventSelect.value;
  try {
    if (button.dataset.action === "propose") {
      const identity = requireIdentity("提案操作人");
      if (!identity) return;
      const reason = document.getElementById("reason").value.trim();
      if (!window.confirm("确认播出链路已不可用？系统将给出适用的静态片候选，切入前还需再次明确确认。")) return;
      await api("/api/slates/propose", { method: "POST", body: JSON.stringify({ event_id: eventID, reason, operator: identity }) });
    } else if (button.dataset.action === "cut-in") {
      const identity = requireIdentity("切入确认操作人");
      if (!identity || !lastStatus || !lastStatus.emergency) return;
      const material = materialCache.get(button.dataset.slate);
      if (!window.confirm(`明确确认：立即将静态片「${material.name}」切入播出？\n提案令牌：${lastStatus.emergency.command}\n确认操作人：${identity}\n此操作会留下切入播出证据。`)) return;
      button.disabled = true;
      await api("/api/slates/cut-in", {
        method: "POST",
        body: JSON.stringify({ event_id: eventID, slate_id: button.dataset.slate, command: lastStatus.emergency.command, operator: identity }),
      });
    } else if (button.dataset.action === "handover") {
      const director = requireIdentity("导播");
      if (!director) return;
      if (!window.confirm(`导播 ${director} 确认选择「${button.closest(".item").querySelector("h2").textContent.trim()}」接管播出？\n系统将先交接后撤下静态片，并留下交接与撤下证据。`)) return;
      button.disabled = true;
      await api("/api/slates/handover", {
        method: "POST",
        body: JSON.stringify({ event_id: eventID, takeover_id: button.dataset.takeover, director }),
      });
    }
    await refreshAll();
  } catch (error) {
    alert(`操作被拒绝：${error.message}`);
    button.disabled = false;
  }
});

// The propose button is rendered dynamically; the delegated listener above
// reaches it through its data-action attribute.
materialForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  const data = new FormData(materialForm);
  const payload = {
    name: String(data.get("name") || "").trim(),
    content_uri: String(data.get("content_uri") || "").trim(),
    checksum: String(data.get("checksum") || "").trim(),
    controlled_by: String(data.get("controlled_by") || "").trim(),
  };
  for (const field of ["available_from", "available_to"]) {
    const raw = String(data.get(field) || "").trim();
    if (raw) payload[field] = new Date(raw).toISOString();
  }
  try {
    await api("/api/slates/materials", { method: "POST", body: JSON.stringify(payload) });
    materialForm.reset();
    await refreshAll();
  } catch (error) {
    alert(`素材录入被拒绝：${error.message}`);
  }
});

refreshAll();
setInterval(refreshAll, 5000);
