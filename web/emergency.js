const eventSelect = document.getElementById("eventSelect");
const chainBadge = document.getElementById("chainBadge");
const chainReasons = document.getElementById("chainReasons");
const actions = document.getElementById("actions");
const candidateRows = document.getElementById("candidateRows");
const evidenceRows = document.getElementById("evidenceRows");
const flash = document.getElementById("flash");
let currentEvent = "";
let pendingToken = "";

function showFlash(message, ok) {
  flash.textContent = message;
  flash.className = ok ? "ok" : "err";
  if (ok) setTimeout(() => { flash.textContent = ""; flash.className = ""; }, 4000);
}

async function postControl(payload) {
  const response = await fetch("/api/control", {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify(payload),
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data || `请求失败 ${response.status}`);
  return data.value;
}

function esc(value) {
  return String(value ?? "").replace(/[&<>"']/g, (ch) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[ch]));
}

function kindLabel(kind) {
  return { "slate-take": "切入", "slate-withdraw": "撤下", "slate-handover": "交接" }[kind] || kind;
}

function windowsText(asset) {
  if (asset.applicable_windows.length === 7 && asset.applicable_windows.every((w) => w.start_min === 0 && w.end_min === 1440)) {
    return "每天 00:00-24:00（全天候兜底）";
  }
  const names = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];
  const hm = (m) => `${String(Math.floor(m / 60)).padStart(2, "0")}:${String(m % 60).padStart(2, "0")}`;
  return asset.applicable_windows.map((w) => `${names[w.weekday]} ${hm(w.start_min)}-${hm(w.end_min)}`).join("；");
}

async function loadEvents() {
  const response = await fetch("/api/operations");
  const payload = await response.json();
  const operations = payload.operations || [];
  eventSelect.innerHTML = operations.length
    ? operations.map((op) => `<option value="${op.event_id}">${esc(op.name)} · ${op.event_id.slice(0, 8)}</option>`).join("")
    : `<option value="">无在播事件</option>`;
  currentEvent = eventSelect.value;
}

function renderChain(chain) {
  if (chain.available) {
    chainBadge.innerHTML = `<span class="badge ok">播出链路可用</span>`;
    chainReasons.textContent = "信号或备路仍在提供有效节目，无需紧急静态片";
  } else {
    chainBadge.innerHTML = `<span class="badge bad">播出链路不可用</span>`;
    chainReasons.textContent = (chain.reasons || []).join("；");
  }
}

function renderActions(view) {
  const status = view.session ? view.session.status : "";
  if (pendingToken) {
    actions.innerHTML = `
      <p class="muted">已生成切入提案，请核对素材后由导播明确确认（令牌 60 秒内有效）：</p>
      <p class="muted">确认令牌：<span class="token">${pendingToken}</span></p>
      <div class="row">
        <button id="confirmTakeBtn">确认切入静态片</button>
        <button class="button ghost" id="cancelProposalBtn">放弃提案</button>
      </div>`;
    document.getElementById("confirmTakeBtn").onclick = async () => {
      try {
        const evidence = await postControl({ action: "confirm-slate", event_id: currentEvent, token: pendingToken });
        pendingToken = "";
        showFlash(`已切入静态片，证据 ${evidence.id}`, true);
        refresh();
      } catch (error) { showFlash(error.message, false); }
    };
    document.getElementById("cancelProposalBtn").onclick = () => { pendingToken = ""; refresh(); };
    return;
  }
  if (status === "holding-slate") {
    actions.innerHTML = `
      <p><span class="badge warn">静态片播出中：${esc(view.session.active_asset.name)}</span></p>
      <div class="row">
        <button class="danger" id="withdrawBtn">撤下静态片</button>
      </div>`;
    document.getElementById("withdrawBtn").onclick = async () => {
      if (!confirm("确认由导播撤下当前紧急静态片？")) return;
      try {
        const evidence = await postControl({ action: "withdraw-slate", event_id: currentEvent, message: "导播手动撤下" });
        showFlash(`静态片已撤下，证据 ${evidence.id}`, true);
        refresh();
      } catch (error) { showFlash(error.message, false); }
    };
    return;
  }
  if (view.takeover && view.takeover.status === "prepared") {
    actions.innerHTML = `
      <p class="muted">接管输出已准备好：${esc(view.takeover.label)}（fence ${view.takeover.fence}），等待导播确认交接。</p>
      <div class="row">
        <button id="handoverBtn">确认交接，结束应急</button>
      </div>`;
    document.getElementById("handoverBtn").onclick = async () => {
      try {
        const evidence = await postControl({ action: "confirm-handover", event_id: currentEvent, takeover_id: view.takeover.id });
        showFlash(`已完成交接，证据 ${evidence.id}`, true);
        refresh();
      } catch (error) { showFlash(error.message, false); }
    };
    return;
  }
  if (status && status.startsWith("withdrawn")) {
    actions.innerHTML = `
      <p><span class="badge warn">静态片已撤下，等待接管输出</span></p>
      <div class="row">
        <button id="prepareBtn">准备接管输出（恢复在播节目）</button>
      </div>`;
    document.getElementById("prepareBtn").onclick = async () => {
      try {
        const takeover = await postControl({ action: "prepare-takeover", event_id: currentEvent, label: "恢复的在播节目" });
        showFlash(`接管输出 ${takeover.id} 已准备好，请确认交接`, true);
        refresh();
      } catch (error) { showFlash(error.message, false); }
    };
    return;
  }
  if (status === "handed-over") {
    actions.innerHTML = `<p><span class="badge ok">应急已结束，节目已由接管输出恢复在播</span></p>`;
    return;
  }
  actions.innerHTML = `<p class="muted">当前没有进行中的应急。${view.chain.available ? "链路可用，不建议切入。" : "请从下方适用候选中选择素材发起切入提案。"}</p>`;
}

function renderCandidates(view) {
  if (!view.candidates.length) {
    candidateRows.innerHTML = `<tr><td colspan="5" class="muted">当前时段没有适用的受控素材</td></tr>`;
    return;
  }
  candidateRows.innerHTML = view.candidates.map((candidate) => `
    <tr>
      <td>${esc(candidate.asset.name)}<br><span class="muted">${esc(candidate.asset.id.slice(0, 8))}</span></td>
      <td>${esc(candidate.asset.content_uri)}</td>
      <td>${esc(candidate.due_reason)}</td>
      <td>${esc(candidate.asset.control)}</td>
      <td>${view.chain.available ? "" : `<button data-asset="${candidate.asset.id}">发起切入提案</button>`}</td>
    </tr>`).join("");
  candidateRows.querySelectorAll("button[data-asset]").forEach((button) => {
    button.onclick = async () => {
      try {
        const proposal = await postControl({ action: "propose-slate", event_id: currentEvent, slate_id: button.dataset.asset, message: "播出链路不可用" });
        pendingToken = proposal.token;
        showFlash("切入提案已生成，请确认", true);
        refresh();
      } catch (error) { showFlash(error.message, false); }
    };
  });
}

function renderEvidence(view) {
  if (!view.evidence.length) {
    evidenceRows.innerHTML = `<tr><td colspan="5" class="muted">暂无静态片播出证据</td></tr>`;
    return;
  }
  evidenceRows.innerHTML = view.evidence.map((evidence) => `
    <tr>
      <td>${new Date(evidence.recorded_at).toLocaleString()}</td>
      <td><span class="badge ${evidence.kind === "slate-take" ? "warn" : "ok"}">${kindLabel(evidence.kind)}</span></td>
      <td>${esc(evidence.asset_name)}</td>
      <td>${esc(evidence.detail)}</td>
      <td class="muted">${evidence.takeover_id ? "接管 " + esc(evidence.takeover_id.slice(0, 8)) : evidence.token ? "令牌 " + esc(evidence.token.slice(0, 8)) : "—"}</td>
    </tr>`).join("");
}

async function refresh() {
  if (!currentEvent) return;
  try {
    const response = await fetch(`/api/emergency?event_id=${encodeURIComponent(currentEvent)}`);
    const view = await response.json();
    document.getElementById("assetCount").textContent = (view.assets || []).length;
    renderChain(view.chain);
    renderActions(view);
    renderCandidates(view);
    renderEvidence(view);
  } catch (error) {
    showFlash(`服务连接失败 ${error.message}`, false);
  }
}

function toMinutes(value) {
  const parts = value.split(":").map(Number);
  return parts[0] * 60 + parts[1];
}

document.getElementById("registerBtn").onclick = async () => {
  const weekday = Number(document.getElementById("assetWeekday").value);
  const windows = weekday === -1
    ? [0, 1, 2, 3, 4, 5, 6].map((day) => ({ weekday: day, start_min: toMinutes(document.getElementById("assetStart").value), end_min: toMinutes(document.getElementById("assetEnd").value) }))
    : [{ weekday, start_min: toMinutes(document.getElementById("assetStart").value), end_min: toMinutes(document.getElementById("assetEnd").value) }];
  try {
    await postControl({
      action: "register-slate",
      slate_name: document.getElementById("assetName").value,
      content_uri: document.getElementById("assetURI").value,
      control: document.getElementById("assetControl").value,
      message: document.getElementById("assetReason").value,
      windows,
    });
    showFlash("受控素材已录入", true);
    refresh();
  } catch (error) { showFlash(error.message, false); }
};

eventSelect.onchange = () => { currentEvent = eventSelect.value; pendingToken = ""; refresh(); };

(async function init() {
  await loadEvents();
  refresh();
  setInterval(refresh, 5000);
})();
