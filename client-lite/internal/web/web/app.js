"use strict";

const appState = { profiles: [], token: "", busy: new Set(), editingId: "", kind: "websocket", poll: null };
const $ = (selector) => document.querySelector(selector);
const profilesElement = $("#profiles");
const dialogElement = $("#profile-dialog");
const formElement = $("#profile-form");
const actionDialogElement = $("#action-dialog");
let actionDialogResolver = null;

class APIError extends Error {
  constructor(message, data, status) { super(message); this.data = data; this.status = status; }
}

function finishActionDialog(result) {
  const resolve = actionDialogResolver;
  actionDialogResolver = null;
  if (actionDialogElement.open) actionDialogElement.close();
  if (resolve) resolve(result);
}

function showConfirmModal({ kind = "确认操作", title = "请确认", message, details = "", confirmText = "确认", danger = false }) {
  if (actionDialogResolver) finishActionDialog(false);
  $("#action-dialog-kind").textContent = kind;
  $("#action-dialog-title").textContent = title;
  $("#action-dialog-message").textContent = message;
  const detailsElement = $("#action-dialog-details");
  detailsElement.textContent = details;
  detailsElement.hidden = !details;
  const confirmButton = $("#confirm-action-dialog");
  confirmButton.textContent = confirmText;
  confirmButton.classList.toggle("primary", !danger);
  confirmButton.classList.toggle("danger-solid", danger);
  actionDialogElement.showModal();
  $("#cancel-action-dialog").focus();
  return new Promise((resolve) => { actionDialogResolver = resolve; });
}

async function api(path, options = {}) {
  const headers = new Headers(options.headers || {});
  if (options.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  if ((options.method || "GET") !== "GET") headers.set("X-Easy-Net-Token", appState.token);
  const response = await fetch(path, { ...options, headers, cache: "no-store" });
  let data = {};
  try { data = await response.json(); } catch (_) { data = {}; }
  if (!response.ok) throw new APIError(data.error || `请求失败（${response.status}）`, data, response.status);
  return data;
}

async function loadState(silent = false) {
  try {
    const data = await api("/api/state");
    appState.profiles = data.profiles || [];
    appState.token = data.token;
    $("#config-path").textContent = `配置文件：${data.configPath}`;
    renderProfiles();
  } catch (error) {
    if (!silent) showToast(error.message, true);
  }
}

function renderProfiles() {
  const running = appState.profiles.filter((item) => item.running).length;
  $("#summary").textContent = `${appState.profiles.length} 个配置 · ${running} 个运行中`;
  if (!appState.profiles.length) {
    profilesElement.innerHTML = `<div class="empty-state"><h2>还没有代理配置</h2><p>使用右上角按钮添加 WebSocket 或 SSH 代理。</p></div>`;
    return;
  }
  profilesElement.innerHTML = appState.profiles.map((item) => {
    const profile = item.profile;
    const busy = appState.busy.has(profile.id);
    const type = profile.type === "ssh" ? "SSH" : "WebSocket";
    const statusClass = busy ? "busy" : item.running ? "running" : "";
    const statusText = busy ? "正在处理" : item.running ? "运行中" : "已停止";
    const endpoint = profile.type === "ssh" ? `${profile.ssh.host}:${profile.ssh.port}` : profile.websocket.url;
    const primaryAction = item.running ? "stop" : "start";
    const primaryText = item.running ? "停止" : "启动";
    return `<article class="profile-card">
      <div class="card-main">
        <div>
          <div class="card-title-row">
            <h2 class="card-title">${escapeHTML(profile.name)}</h2>
            <span class="badge">${type}</span>
            <span class="status ${statusClass}">${statusText}</span>
            ${profile.autoStart ? `<span class="badge">自动启动</span>` : ""}
          </div>
          <p class="endpoint">SOCKS5 ${escapeHTML(profile.listenHost)}:${profile.listenPort} · ${escapeHTML(endpoint)}</p>
          ${item.error ? `<p class="error">最近错误：${escapeHTML(item.error)}</p>` : ""}
        </div>
        <div class="card-actions">
          <button class="button ${item.running ? "secondary" : "start"}" data-profile-action="${primaryAction}" data-id="${escapeHTML(profile.id)}" ${busy ? "disabled" : ""}>${primaryText}</button>
          <button class="button secondary" data-profile-action="edit" data-id="${escapeHTML(profile.id)}" ${busy ? "disabled" : ""}>编辑</button>
          <button class="button danger" data-profile-action="delete" data-id="${escapeHTML(profile.id)}" ${busy ? "disabled" : ""}>删除</button>
        </div>
      </div>
    </article>`;
  }).join("");
}

function openProfileDialog(kind, id = "") {
  const item = id ? appState.profiles.find((entry) => entry.profile.id === id) : null;
  const profile = item ? structuredClone(item.profile) : null;
  appState.editingId = id;
  appState.kind = kind;
  formElement.reset();
  $("#form-error").hidden = true;
  $("#dialog-kind").textContent = kind === "ssh" ? "SSH 代理" : "WebSocket 代理";
  $("#dialog-title").textContent = id ? "编辑配置" : "添加配置";
  $("#websocket-fields").hidden = kind !== "websocket";
  $("#ssh-fields").hidden = kind !== "ssh";
  document.querySelectorAll(".edit-secret-hint").forEach((element) => { element.hidden = !id; });
  $("#field-name").value = profile?.name || "";
  $("#field-local-port").value = profile?.listenPort || nextPort();
  $("#field-auto-start").checked = profile ? profile.autoStart : true;
  if (kind === "websocket") {
    $("#field-ws-url").value = profile?.websocket?.url || "";
	$("#field-ws-url").required = true;
    $("#field-ws-secret").required = !id;
	$("#field-ssh-host").required = false;
	$("#field-ssh-user").required = false;
	$("#field-ssh-port").required = false;
  } else {
	$("#field-ws-url").required = false;
	$("#field-ws-secret").required = false;
    $("#field-ssh-host").value = profile?.ssh?.host || "";
    $("#field-ssh-port").value = profile?.ssh?.port || 22;
    $("#field-ssh-user").value = profile?.ssh?.username || "";
    $("#field-ssh-auth").value = profile?.ssh?.authType || "password";
	$("#field-ssh-host").required = true;
	$("#field-ssh-user").required = true;
	$("#field-ssh-port").required = true;
    $("#field-ssh-password").required = !id && $("#field-ssh-auth").value === "password";
    const existingKey = profile?.ssh?.privateKeyPath || "";
    $("#ssh-key-hint").textContent = existingKey ? "已保存私钥；重新选择文件将替换它" : "支持 OpenSSH 私钥，最大 64 KiB";
    toggleSSHAuth();
  }
  dialogElement.showModal();
  $("#field-name").focus();
}

function toggleSSHAuth() {
  const privateKey = $("#field-ssh-auth").value === "private_key";
	const existing = appState.editingId ? appState.profiles.find((entry) => entry.profile.id === appState.editingId)?.profile : null;
	const hasSavedPassword = Boolean(existing?.ssh?.passwordRef);
	const passwordRow = $("#ssh-password-row");
	const keyRow = $("#ssh-key-row");
	const passphraseRow = $("#ssh-passphrase-row");
	const passwordInput = $("#field-ssh-password");
	const keyInput = $("#field-ssh-key");
	const passphraseInput = $("#field-ssh-passphrase");
	passwordRow.hidden = privateKey;
	keyRow.hidden = !privateKey;
	passphraseRow.hidden = !privateKey;
	passwordInput.disabled = privateKey;
	keyInput.disabled = !privateKey;
	passphraseInput.disabled = !privateKey;
	passwordInput.required = !privateKey && !hasSavedPassword;
}

async function saveProfile(event) {
  event.preventDefault();
  const saveButton = $("#save-profile");
  const errorElement = $("#form-error");
  errorElement.hidden = true;
  if (!formElement.reportValidity()) return;
  saveButton.disabled = true;
  saveButton.textContent = "保存中…";
  try {
    const existing = appState.editingId ? appState.profiles.find((entry) => entry.profile.id === appState.editingId)?.profile : null;
    const profile = existing ? structuredClone(existing) : {
      id: "", type: appState.kind, listenHost: "127.0.0.1", websocket: null, ssh: null
    };
    profile.name = $("#field-name").value.trim();
    profile.listenPort = Number($("#field-local-port").value);
    profile.autoStart = $("#field-auto-start").checked;
    const request = { profile, websocketSecret: "", sshPassword: "", sshPassphrase: "", sshPrivateKey: "" };
    if (appState.kind === "websocket") {
      profile.websocket = profile.websocket || { url: "", secretRef: "" };
      profile.ssh = null;
      profile.websocket.url = $("#field-ws-url").value.trim();
      request.websocketSecret = $("#field-ws-secret").value;
    } else {
      profile.ssh = profile.ssh || { host: "", port: 22, username: "", authType: "password" };
      profile.websocket = null;
      profile.ssh.host = $("#field-ssh-host").value.trim();
      profile.ssh.port = Number($("#field-ssh-port").value);
      profile.ssh.username = $("#field-ssh-user").value.trim();
      profile.ssh.authType = $("#field-ssh-auth").value;
      const keyFile = $("#field-ssh-key").files[0];
      if (profile.ssh.authType === "private_key") {
		request.sshPassphrase = $("#field-ssh-passphrase").value;
        if (!keyFile && !profile.ssh.privateKeyPath) throw new Error("请选择 SSH 私钥文件");
        if (keyFile && keyFile.size > 64 * 1024) throw new Error("私钥文件不能超过 64 KiB");
        if (keyFile) request.sshPrivateKey = await keyFile.text();
	  } else {
		request.sshPassword = $("#field-ssh-password").value;
      }
    }
    await api("/api/profiles", { method: "POST", body: JSON.stringify(request) });
    dialogElement.close();
    showToast("配置已保存");
    await loadState();
  } catch (error) {
    errorElement.textContent = error.message;
    errorElement.hidden = false;
  } finally {
    saveButton.disabled = false;
    saveButton.textContent = "保存配置";
  }
}

async function profileAction(action, id) {
  if (appState.busy.has(id)) return;
  const item = appState.profiles.find((entry) => entry.profile.id === id);
  if (!item) return;
  if (action === "edit") { openProfileDialog(item.profile.type, id); return; }
  if (action === "delete") {
    const confirmed = await showConfirmModal({
      kind: "删除配置",
      title: "删除这个代理？",
      message: `“${item.profile.name}”将从代理列表中移除。`,
      details: "相关密码和托管私钥也会一并删除，此操作无法撤销。",
      confirmText: "删除配置",
      danger: true
    });
    if (!confirmed) return;
    try { await api(`/api/profiles/${encodeURIComponent(id)}`, { method: "DELETE" }); showToast("配置已删除"); await loadState(); }
    catch (error) { showToast(error.message, true); }
    return;
  }
  appState.busy.add(id);
  renderProfiles();
  try {
    await api(`/api/profiles/${encodeURIComponent(id)}/${action}`, { method: "POST" });
    showToast(action === "start" ? "代理已启动" : "代理已停止");
  } catch (error) {
    if (error.data?.code === "ssh_host_unknown") {
      const trusted = await showConfirmModal({
        kind: "SSH 安全确认",
        title: "确认服务器指纹",
        message: `这是首次连接 ${error.data.address}。请与服务器管理员核对下面的 SHA-256 指纹。`,
        details: error.data.fingerprint,
        confirmText: "信任并启动"
      });
      if (trusted) {
        try {
          await api(`/api/profiles/${encodeURIComponent(id)}/trust`, { method: "POST", body: JSON.stringify({ fingerprint: error.data.fingerprint }) });
          await api(`/api/profiles/${encodeURIComponent(id)}/start`, { method: "POST" });
          showToast("SSH 指纹已信任，代理已启动");
        } catch (trustError) { showToast(trustError.message, true); }
      }
    } else { showToast(error.message, true); }
  } finally {
    appState.busy.delete(id);
    await loadState(true);
  }
}

async function globalCommand(command) {
  if (command === "quit") {
    const confirmed = await showConfirmModal({
      kind: "退出程序",
      title: "停止全部代理并退出？",
      message: "退出后，所有本地 SOCKS5 端口都会停止监听。",
      details: "需要再次使用时，请重新启动 Easy-Net Lite。",
      confirmText: "退出程序",
      danger: true
    });
    if (!confirmed) return;
    try { await api("/api/app/quit", { method: "POST" }); document.body.innerHTML = `<main class="closed"><h1>Easy-Net Lite 已退出</h1><p>现在可以关闭此页面。</p></main>`; }
    catch (error) { showToast(error.message, true); }
    return;
  }
  try {
    await api(`/api/${command}`, { method: "POST" });
    showToast(command === "start-all" ? "已执行启动全部" : "所有代理已停止");
    await loadState();
  } catch (error) { showToast(error.message, true); }
}

function showToast(message, isError = false) {
  const toast = $("#toast");
  if (!toast) return;
  toast.textContent = message;
  toast.classList.toggle("error-toast", isError);
  toast.hidden = false;
  clearTimeout(showToast.timer);
  showToast.timer = setTimeout(() => { toast.hidden = true; }, 3600);
}

function nextPort() {
  const used = new Set(appState.profiles.map((item) => item.profile.listenPort));
  for (let port = 1080; port < 65536; port += 1) if (!used.has(port)) return port;
  return 1080;
}

function escapeHTML(value) {
  return String(value ?? "").replace(/[&<>'"]/g, (character) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "'": "&#39;", '"': "&quot;" })[character]);
}

document.addEventListener("click", (event) => {
  const addButton = event.target.closest("[data-add]");
  if (addButton) { openProfileDialog(addButton.dataset.add); return; }
  const actionButton = event.target.closest("[data-profile-action]");
  if (actionButton) { profileAction(actionButton.dataset.profileAction, actionButton.dataset.id); return; }
  const commandButton = event.target.closest("[data-command]");
  if (commandButton) globalCommand(commandButton.dataset.command);
});

$("#field-ssh-auth").addEventListener("change", toggleSSHAuth);
$("#close-dialog").addEventListener("click", () => dialogElement.close());
$("#cancel-dialog").addEventListener("click", () => dialogElement.close());
$("#close-action-dialog").addEventListener("click", () => finishActionDialog(false));
$("#cancel-action-dialog").addEventListener("click", () => finishActionDialog(false));
$("#confirm-action-dialog").addEventListener("click", () => finishActionDialog(true));
formElement.addEventListener("submit", saveProfile);
dialogElement.addEventListener("click", (event) => { if (event.target === dialogElement) dialogElement.close(); });
actionDialogElement.addEventListener("cancel", (event) => { event.preventDefault(); finishActionDialog(false); });
actionDialogElement.addEventListener("close", () => {
  if (actionDialogResolver) {
    const resolve = actionDialogResolver;
    actionDialogResolver = null;
    resolve(false);
  }
});
actionDialogElement.addEventListener("click", (event) => { if (event.target === actionDialogElement) finishActionDialog(false); });
document.addEventListener("visibilitychange", () => { if (!document.hidden) loadState(true); });

loadState();
appState.poll = setInterval(() => { if (!document.hidden && !dialogElement.open) loadState(true); }, 2500);
