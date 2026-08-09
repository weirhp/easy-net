"use strict";

const appState = {
  profiles: [],
  token: "",
  busy: new Set(),
  editingId: "",
  kind: "websocket",
  poll: null,
  warningsShown: false,
  initialized: false,
  notifiedFailures: new Map(),
};
const $ = (selector) => document.querySelector(selector);
const profilesElement = $("#profiles");
const dialogElement = $("#profile-dialog");
const formElement = $("#profile-form");
const actionDialogElement = $("#action-dialog");
const shareDialogElement = $("#share-dialog");
const importDialogElement = $("#import-dialog");
const importFormElement = $("#import-form");
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
	$("#cancel-action-dialog").hidden = false;
  confirmButton.textContent = confirmText;
  confirmButton.classList.toggle("primary", !danger);
  confirmButton.classList.toggle("danger-solid", danger);
  actionDialogElement.showModal();
  $("#cancel-action-dialog").focus();
  return new Promise((resolve) => { actionDialogResolver = resolve; });
}

function showMessageModal({ kind = "操作结果", title, message, details = "", isError = false }) {
	if (actionDialogResolver) finishActionDialog(false);
	$("#action-dialog-kind").textContent = kind;
	$("#action-dialog-title").textContent = title;
	$("#action-dialog-message").textContent = message;
	const detailsElement = $("#action-dialog-details");
	detailsElement.textContent = details;
	detailsElement.hidden = !details;
	const confirmButton = $("#confirm-action-dialog");
	confirmButton.textContent = "关闭";
	confirmButton.classList.toggle("primary", !isError);
	confirmButton.classList.toggle("danger-solid", isError);
	$("#cancel-action-dialog").hidden = true;
	actionDialogElement.showModal();
	confirmButton.focus();
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
	const previousProfiles = appState.profiles;
    appState.profiles = data.profiles || [];
    appState.token = data.token;
    $("#config-path").textContent = `Easy-Net Lite ${data.version || "dev"} · 配置文件：${data.configPath}`;
    renderProfiles();
	if (appState.initialized) notifyConnectionFailures(previousProfiles, appState.profiles);
	appState.initialized = true;
    if (!silent && !appState.warningsShown && data.warnings?.length) {
      appState.warningsShown = true;
      showToast(data.warnings.join("；"), true);
    }
  } catch (error) {
    if (!silent) showToast(error.message, true);
  }
}

function notifyConnectionFailures(previousProfiles, currentProfiles) {
	const now = Date.now();
	for (const item of currentProfiles) {
		const profileID = item.profile.id;
		if (item.connectionStatus !== "error" || !item.connectionAt) {
			appState.notifiedFailures.delete(profileID);
			continue;
		}
		const previous = previousProfiles.find((entry) => entry.profile.id === item.profile.id);
		if (previous?.connectionAt === item.connectionAt && previous?.connectionStatus === "error") continue;
		const message = `${item.profile.name}：${item.connectionError || "远端连接失败"}`;
		const notified = appState.notifiedFailures.get(profileID);
		if (notified?.message === message && now - notified.at < 30000) continue;
		appState.notifiedFailures.set(profileID, { message, at: now });
		showToast(message, true);
	}
}

function formatConnectionTime(value) {
	if (!value) return "";
	const date = new Date(value);
	if (Number.isNaN(date.getTime())) return "";
	const now = new Date();
	const sameDay = date.getFullYear() === now.getFullYear() && date.getMonth() === now.getMonth() && date.getDate() === now.getDate();
	return new Intl.DateTimeFormat("zh-CN", sameDay
		? { hour: "2-digit", minute: "2-digit", second: "2-digit" }
		: { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" }).format(date);
}

function renderProfiles() {
  const running = appState.profiles.filter((item) => item.running).length;
  $("#summary").textContent = `${appState.profiles.length} 个配置 · ${running} 个本地监听`;
  if (!appState.profiles.length) {
    profilesElement.innerHTML = `<div class="empty-state"><h2>还没有代理配置</h2><p>使用右上角按钮添加 WebSocket 或 SSH 代理。</p></div>`;
    return;
  }
  profilesElement.innerHTML = appState.profiles.map((item) => {
    const profile = item.profile;
    const busy = appState.busy.has(profile.id);
    const type = profile.type === "ssh" ? "SSH" : "WebSocket";
    const localCapabilities = profile.type === "ssh" ? "SOCKS5 TCP / HTTP" : "SOCKS5 TCP+UDP / HTTP";
    const statusClass = busy || item.starting ? "busy" : item.running ? "running" : "";
    const statusText = item.starting ? "正在启动" : busy ? "正在处理" : item.running ? "本地监听中" : "已停止";
    const endpoint = profile.type === "ssh" ? `${profile.ssh.host}:${profile.ssh.port}` : profile.websocket.url;
    const primaryAction = item.running ? "stop" : "start";
    const primaryText = item.running ? "停止" : "启动";
	const connectionTime = formatConnectionTime(item.connectionAt);
	const connectionStatus = item.connectionStatus === "success"
		? `<span class="connection-health success">远端连接正常${connectionTime ? ` · ${escapeHTML(connectionTime)}` : ""}</span>`
		: item.connectionStatus === "error"
			? `<span class="connection-health error-health">远端连接失败${connectionTime ? ` · ${escapeHTML(connectionTime)}` : ""}</span>`
			: `<span class="connection-health untested">远端尚未验证</span>`;
    return `<article class="profile-card">
      <div class="card-main">
        <div>
          <div class="card-title-row">
            <h2 class="card-title">${escapeHTML(profile.name)}</h2>
            <span class="badge">${type}</span>
            <span class="status ${statusClass}">${statusText}</span>
            ${profile.autoStart ? `<span class="badge">自动启动</span>` : ""}
            ${profile.bypassPrivate ? `<span class="badge">内网直连</span>` : ""}
          </div>
          <p class="endpoint">${localCapabilities} ${escapeHTML(profile.listenHost)}:${profile.listenPort} · ${escapeHTML(endpoint)}</p>
		  <div class="connection-row">${connectionStatus}</div>
		  ${item.error ? `<p class="error">启动错误：${escapeHTML(item.error)}</p>` : ""}
		  ${item.connectionError ? `<p class="error connection-error">连接失败：${escapeHTML(item.connectionError)}</p>` : ""}
        </div>
        <div class="card-actions">
          <button class="button ${item.running ? "secondary" : "start"}" data-profile-action="${primaryAction}" data-id="${escapeHTML(profile.id)}" ${busy ? "disabled" : ""}>${primaryText}</button>
		  <button class="button secondary" data-profile-action="test" data-id="${escapeHTML(profile.id)}" ${busy || item.starting ? "disabled" : ""}>测试连接</button>
          <button class="button secondary" data-profile-action="share" data-id="${escapeHTML(profile.id)}" ${busy ? "disabled" : ""}>分享</button>
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
  $("#field-bypass-private").checked = Boolean(profile?.bypassPrivate);
  if (kind === "websocket") {
    $("#field-ws-url").value = profile?.websocket?.url || "";
	$("#field-ws-secret").placeholder = id ? "已保存；如需更换请重新输入" : "请输入连接密钥";
	$("#ws-secret-hint").textContent = id
		? "当前密钥已保存在系统凭据库。留空会继续使用原密钥；服务端密钥有变化时必须重新填写。"
		: "密钥只保存在系统凭据库，不会写入配置文件。";
	$("#field-ws-insecure").checked = Boolean(profile?.websocket?.allowInsecure);
	$("#field-ws-legacy-query").checked = Boolean(profile?.websocket?.legacyQueryAuth);
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
    const existingKey = Boolean(profile?.ssh?.hasPrivateKey);
    $("#ssh-key-hint").textContent = existingKey ? "已保存私钥；重新选择文件将替换它" : "支持 OpenSSH 私钥，最大 64 KiB";
    toggleSSHAuth();
  }
  dialogElement.showModal();
  $("#field-name").focus();
}

function toggleSSHAuth() {
  const privateKey = $("#field-ssh-auth").value === "private_key";
	const existing = appState.editingId ? appState.profiles.find((entry) => entry.profile.id === appState.editingId)?.profile : null;
	const hasSavedPassword = Boolean(existing?.ssh?.hasPassword);
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
    const profile = {
      id: existing?.id || "",
      name: $("#field-name").value.trim(),
      type: appState.kind,
      listenHost: "127.0.0.1",
      listenPort: Number($("#field-local-port").value),
      autoStart: $("#field-auto-start").checked,
      bypassPrivate: $("#field-bypass-private").checked,
      websocket: null,
      ssh: null
    };
    const request = { profile, websocketSecret: "", sshPassword: "", sshPassphrase: "", sshPrivateKey: "" };
    if (appState.kind === "websocket") {
      profile.websocket = {
        url: $("#field-ws-url").value.trim(),
        allowInsecure: $("#field-ws-insecure").checked,
        legacyQueryAuth: $("#field-ws-legacy-query").checked
      };
      request.websocketSecret = $("#field-ws-secret").value;
    } else {
      profile.ssh = {
        host: $("#field-ssh-host").value.trim(),
        port: Number($("#field-ssh-port").value),
        username: $("#field-ssh-user").value.trim(),
        authType: $("#field-ssh-auth").value
      };
      const keyFile = $("#field-ssh-key").files[0];
      if (profile.ssh.authType === "private_key") {
		request.sshPassphrase = $("#field-ssh-passphrase").value;
        if (!keyFile && !existing?.ssh?.hasPrivateKey) throw new Error("请选择 SSH 私钥文件");
        if (keyFile && keyFile.size > 64 * 1024) throw new Error("私钥文件不能超过 64 KiB");
        if (keyFile) request.sshPrivateKey = await keyFile.text();
	  } else {
		request.sshPassword = $("#field-ssh-password").value;
      }
    }
    await api("/api/profiles", { method: "POST", body: JSON.stringify(request) });
    dialogElement.close();
    showToast(appState.kind === "websocket" ? "配置已保存，请使用“测试连接”确认地址和密钥" : "配置已保存");
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
  if (action === "share") {
    appState.busy.add(id);
    renderProfiles();
    try {
      const data = await api(`/api/profiles/${encodeURIComponent(id)}/share`, { method: "POST" });
      $("#share-code").value = data.shareCode || "";
      shareDialogElement.showModal();
      $("#copy-share-code").focus();
    } catch (error) { showToast(error.message, true); }
    finally { appState.busy.delete(id); await loadState(true); }
    return;
  }
	if (action === "test") {
		appState.busy.add(id);
		renderProfiles();
		try {
			await api(`/api/profiles/${encodeURIComponent(id)}/test`, { method: "POST" });
			await loadState(true);
			await showMessageModal({
				kind: "连接测试",
				title: "连接测试成功",
				message: item.profile.type === "ssh" ? "SSH 地址和认证信息验证通过。" : "WebSocket 地址、密钥和隧道握手验证通过。",
				details: "配置卡片已更新最近连接状态，现在可以通过本地 SOCKS5/HTTP 混合端口使用代理。"
			});
		} catch (error) {
			await loadState(true);
			if (error.data?.code === "ssh_host_unknown") {
				const trusted = await showConfirmModal({
					kind: "SSH 安全确认",
					title: "确认服务器指纹",
					message: `这是首次连接 ${error.data.address}。请与服务器管理员核对下面的 SHA-256 指纹。`,
					details: error.data.fingerprint,
					confirmText: "信任并重新测试"
				});
				if (trusted) {
					try {
						await api(`/api/profiles/${encodeURIComponent(id)}/trust`, { method: "POST", body: JSON.stringify({ fingerprint: error.data.fingerprint }) });
						await api(`/api/profiles/${encodeURIComponent(id)}/test`, { method: "POST" });
						await loadState(true);
						await showMessageModal({ kind: "连接测试", title: "连接测试成功", message: "SSH 指纹和认证信息验证通过。" });
					} catch (trustError) {
						await loadState(true);
						await showMessageModal({ kind: "连接测试", title: "连接测试失败", message: "SSH 连接仍未建立。", details: trustError.message, isError: true });
					}
				}
			} else {
				await showMessageModal({
					kind: "连接测试",
					title: "连接测试失败",
					message: "未能通过该配置建立远端连接，请按下面的提示修正后重试。",
					details: error.message,
					isError: true
				});
			}
		} finally {
			appState.busy.delete(id);
			await loadState(true);
		}
		return;
	}
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
    showToast(action === "start" ? "本地 SOCKS5/HTTP 监听已启动；远端状态会在首次使用或测试后更新" : "代理已停止");
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

function closeShareDialog() {
  if (shareDialogElement.open) shareDialogElement.close();
  $("#share-code").value = "";
}

async function copyShareCode() {
  const code = $("#share-code").value;
  if (!code) return;
  try {
    await navigator.clipboard.writeText(code);
    showToast("分享码已复制");
  } catch (_) {
    const field = $("#share-code");
    field.focus();
    field.select();
    if (document.execCommand("copy")) showToast("分享码已复制");
    else showToast("复制失败，请手动选择分享码", true);
  }
}

function openImportDialog() {
  importFormElement.reset();
  $("#import-error").hidden = true;
  importDialogElement.showModal();
  $("#import-share-code").focus();
}

function closeImportDialog() {
	if (importDialogElement.open) importDialogElement.close();
	importFormElement.reset();
	$("#import-error").hidden = true;
}

async function pasteShareCode() {
  const errorElement = $("#import-error");
  errorElement.hidden = true;
  try {
    const text = (await navigator.clipboard.readText()).trim();
    if (!text) throw new Error("剪贴板中没有分享码");
    $("#import-share-code").value = text;
    $("#import-share-code").focus();
  } catch (error) {
    errorElement.textContent = error.message || "无法读取剪贴板，请手动粘贴分享码";
    errorElement.hidden = false;
  }
}

async function importProfile(event) {
  event.preventDefault();
  const button = $("#import-profile");
  const errorElement = $("#import-error");
  errorElement.hidden = true;
  if (!importFormElement.reportValidity()) return;
  button.disabled = true;
  button.textContent = "导入中…";
  try {
    await api("/api/import", { method: "POST", body: JSON.stringify({ shareCode: $("#import-share-code").value.trim() }) });
    closeImportDialog();
    showToast("配置已导入，请检查后启动");
    await loadState();
  } catch (error) {
    errorElement.textContent = error.message;
    errorElement.hidden = false;
  } finally {
    button.disabled = false;
    button.textContent = "导入配置";
  }
}

async function globalCommand(command) {
  if (command === "quit") {
    const confirmed = await showConfirmModal({
      kind: "退出程序",
      title: "停止全部代理并退出？",
      message: "退出后，所有本地 SOCKS5/HTTP 混合端口都会停止监听。",
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
    showToast(command === "start-all" ? "已启动全部本地监听；请查看各配置的远端状态" : "所有代理已停止");
    await loadState();
  } catch (error) { showToast(error.message, true); }
}

function showToast(message, isError = false) {
  const toast = $("#toast");
  if (!toast) return;
  toast.textContent = message;
  toast.classList.toggle("error-toast", isError);
	toast.setAttribute("role", isError ? "alert" : "status");
	toast.setAttribute("aria-live", isError ? "assertive" : "polite");
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
  const importButton = event.target.closest("[data-import]");
  if (importButton) { openImportDialog(); return; }
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
$("#close-share-dialog").addEventListener("click", closeShareDialog);
$("#dismiss-share-dialog").addEventListener("click", closeShareDialog);
$("#copy-share-code").addEventListener("click", copyShareCode);
$("#close-import-dialog").addEventListener("click", closeImportDialog);
$("#cancel-import-dialog").addEventListener("click", closeImportDialog);
$("#paste-share-code").addEventListener("click", pasteShareCode);
importFormElement.addEventListener("submit", importProfile);
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
shareDialogElement.addEventListener("click", (event) => { if (event.target === shareDialogElement) closeShareDialog(); });
importDialogElement.addEventListener("click", (event) => { if (event.target === importDialogElement) closeImportDialog(); });
shareDialogElement.addEventListener("cancel", (event) => { event.preventDefault(); closeShareDialog(); });
importDialogElement.addEventListener("cancel", (event) => { event.preventDefault(); closeImportDialog(); });
document.addEventListener("visibilitychange", () => { if (!document.hidden) loadState(true); });

loadState();
appState.poll = setInterval(() => { if (!document.hidden && !dialogElement.open && !shareDialogElement.open && !importDialogElement.open) loadState(true); }, 2500);
