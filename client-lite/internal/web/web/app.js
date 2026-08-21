"use strict";

const appState = {
  profiles: [],
  subscriptions: [],
  launches: [],
	takeover: { enabled: false, state: "stopped", message: "" },
  runningProcesses: [],
  commonPaths: new Map(),
  features: { appLaunches: false },
  tab: location.hash === "#apps" ? "apps" : "proxies",
  proxySource: "manual",
  nodeFilter: "",
  token: "",
  busy: new Set(),
  selectedProfiles: new Set(),
  editingId: "",
  editingLaunchId: "",
  kind: "websocket",
  poll: null,
  warningsShown: false,
  initialized: false,
  notifiedFailures: new Map(),
	notifiedTakeoverError: "",
  nodeMetrics: {},
};
const $ = (selector) => document.querySelector(selector);
const profilesElement = $("#profiles");
const launchesElement = $("#launches");
const dialogElement = $("#profile-dialog");
const formElement = $("#profile-form");
const launchDialogElement = $("#launch-dialog");
const launchFormElement = $("#launch-form");
const actionDialogElement = $("#action-dialog");
const shareDialogElement = $("#share-dialog");
const importDialogElement = $("#import-dialog");
const importFormElement = $("#import-form");
const clashImportDialogElement = $("#clash-import-dialog");
const clashImportFormElement = $("#clash-import-form");
const processDialogElement = $("#process-dialog");
const processFormElement = $("#process-form");
const commonDialogElement = $("#common-dialog");
const commonFormElement = $("#common-form");
let actionDialogResolver = null;
let applicationPickerBusy = false;

const commonApplications = [
  { name: "Cursor.exe", label: "Cursor", mode: "cursor", processes: "Cursor.exe" },
  { name: "ChatGPT.exe", label: "ChatGPT", mode: "chatgpt", processes: "ChatGPT.exe;codex-code-mode-host.exe" },
  { name: "Antigravity IDE.exe", label: "Antigravity IDE", mode: "antigravity", processes: "Antigravity IDE.exe;language_server_windows_x64.exe" },
  { name: "claude.exe", label: "Claude Code", mode: "claude", processes: "claude.exe;claude-code.exe" },
  { name: "chrome.exe", label: "Google Chrome", mode: "chrome", processes: "chrome.exe" },
  { name: "msedge.exe", label: "Microsoft Edge", mode: "edge", processes: "msedge.exe" }
];

function icon(name) {
  return `<svg class="icon" aria-hidden="true"><use href="#icon-${name}"></use></svg>`;
}

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

function showAlertModal({ kind = "启动失败", title = "无法启动应用", message, details = "", confirmText = "知道了" }) {
  if (actionDialogResolver) finishActionDialog(false);
  $("#action-dialog-kind").textContent = kind;
  $("#action-dialog-title").textContent = title;
  $("#action-dialog-message").textContent = message;
  const detailsElement = $("#action-dialog-details");
  detailsElement.textContent = details;
  detailsElement.hidden = !details;
  $("#cancel-action-dialog").hidden = true;
  const confirmButton = $("#confirm-action-dialog");
  confirmButton.textContent = confirmText;
  confirmButton.classList.remove("danger-solid");
  confirmButton.classList.add("primary");
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
	const previousTakeover = appState.takeover;
    appState.profiles = data.profiles || [];
    appState.subscriptions = data.subscriptions || [];
    appState.launches = data.launches || [];
	appState.takeover = data.takeover || { enabled: false, state: "stopped", message: "" };
    appState.features = data.features || { appLaunches: false };
    appState.token = data.token;
	$("#app-version").textContent = `v${data.version || "dev"}`;
	$("#config-path").textContent = `配置文件：${data.configPath}`;
	const validIDs = new Set(appState.profiles.map((item) => item.profile.id));
	for (const id of appState.selectedProfiles) if (!validIDs.has(id)) appState.selectedProfiles.delete(id);
    syncTabs();
    renderSourceTabs();
    renderProfiles();
    renderLaunches();
	renderTakeover();
	if (appState.initialized) notifyConnectionFailures(previousProfiles, appState.profiles);
	if (appState.initialized && appState.takeover.state === "error") {
		const message = appState.takeover.message || "应用网络接管服务异常，正在自动恢复";
		if (previousTakeover?.state !== "error" || appState.notifiedTakeoverError !== message) {
			appState.notifiedTakeoverError = message;
			showToast(message, true);
		}
	} else if (appState.takeover.state !== "error") {
		appState.notifiedTakeoverError = "";
	}
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

function manualProfiles() {
  return appState.profiles.filter((item) => item.profile.type !== "clash");
}

function currentSubscription() {
  return (appState.subscriptions || []).find((item) => item.id === appState.proxySource) || null;
}

function proxyDisplayName(profile) {
  if (profile.type === "clash" && profile.clash?.nodeName) {
    return `${profile.name} · ${profile.clash.nodeName}`;
  }
  return profile.name;
}

function renderSourceTabs() {
  const sticky = $("#clash-sticky");
  const tabs = $("#source-tabs");
  const toolbar = $("#subscription-toolbar");
  const isProxies = appState.tab === "proxies";
  if (sticky) sticky.hidden = !isProxies;
  if (!isProxies) {
    if (toolbar) toolbar.hidden = true;
    return;
  }
  const subs = appState.subscriptions || [];
  if (appState.proxySource !== "manual" && !subs.some((item) => item.id === appState.proxySource)) {
    appState.proxySource = "manual";
  }
  const buttons = [`<button type="button" class="source-tab${appState.proxySource === "manual" ? " active" : ""}" data-proxy-source="manual">手动添加</button>`];
  for (const sub of subs) {
    const running = sub.running ? " running" : "";
    buttons.push(`<button type="button" class="source-tab${appState.proxySource === sub.id ? " active" : ""}${running}" data-proxy-source="${escapeHTML(sub.id)}">${escapeHTML(sub.name)}</button>`);
  }
  tabs.innerHTML = buttons.join("");
  const isManual = appState.proxySource === "manual";
  toolbar.hidden = isManual;
  document.querySelectorAll("[data-actions='proxies'] [data-import], [data-actions='proxies'] [data-command='start-all'], [data-actions='proxies'] [data-menu]").forEach((element) => {
    element.hidden = !isManual;
  });
  if (!isManual) {
    const sub = currentSubscription();
    const filter = $("#node-filter");
    if (filter && filter.value !== appState.nodeFilter) filter.value = appState.nodeFilter;
    const meta = $("#subscription-meta");
    if (!sub) {
      meta.textContent = "";
    } else {
      const current = sub.selectedNode ? ` · <span class="current-node">当前 ${escapeHTML(sub.selectedNode)}</span>` : "";
      meta.innerHTML = `${sub.nodes?.length || 0} 个节点 · ${escapeHTML(sub.listenAddress)}${current}`;
    }
    const testing = Boolean(sub && appState.busy.has(`clash-test:${sub.id}`));
    document.querySelectorAll("[data-clash-action='delay'], [data-clash-action='speed']").forEach((button) => {
      button.disabled = testing;
    });
  }
}

function renderProfiles() {
  if (appState.tab !== "proxies") return;
  renderSourceTabs();
  if (appState.proxySource !== "manual") {
    renderClashNodes();
    return;
  }
  profilesElement.classList.remove("node-grid");
  const profiles = manualProfiles();
  const running = profiles.filter((item) => item.running).length;
  const external = profiles.filter((item) => item.profile.type === "external").length;
  $("#summary").textContent = `${profiles.length} 个代理 · ${running} 个本地监听${external ? ` · ${external} 个外部代理` : ""}`;
  $("#page-eyebrow").textContent = "LOCAL PROXY";
  $("#page-title").textContent = "网络代理管理";
  $("#notice-title").textContent = "使用说明";
  $("#overview-note").textContent = "配置并启动代理后，本机流量会通过加密通道传输。关闭此窗口不会停止代理运行，你可以在系统托盘中继续管理。Clash 订阅请用上方 Tab 切换。";
  if (!profiles.length) {
    profilesElement.innerHTML = `<div class="empty-state"><h2>还没有代理配置</h2><p>使用右上角按钮添加 WebSocket、SSH、外部 SOCKS5，或导入 Clash 订阅。</p></div>`;
    updateSelectionToolbar();
    return;
  }
  profilesElement.innerHTML = profiles.map((item) => {
    const profile = item.profile;
    const busy = appState.busy.has(profile.id);
    const isExternal = profile.type === "external";
    const type = profile.type === "ssh" ? "SSH" : isExternal ? "外部 SOCKS5" : "WebSocket";
    const localCapabilities = isExternal ? "SOCKS5 / 混合端口" : profile.type === "ssh" ? "SOCKS5 TCP / HTTP" : "SOCKS5 TCP+UDP / HTTP";
    const statusClass = busy || item.starting ? "busy" : item.running || isExternal ? "running" : "";
    const statusText = item.starting ? "正在启动" : busy ? "正在处理" : isExternal ? "外部提供" : item.running ? "本地监听中" : "已停止";
    const endpoint = isExternal ? "由其他代理软件管理" : profile.type === "ssh" ? `${profile.ssh.host}:${profile.ssh.port}` : profile.websocket.url;
    const primaryAction = item.running ? "stop" : "start";
    const primaryText = item.running ? "停止" : "启动";
	const connectionTime = formatConnectionTime(item.connectionAt);
	const connectionStatus = item.connectionStatus === "success"
		? `<span class="connection-health success">远端连接正常${connectionTime ? ` · ${escapeHTML(connectionTime)}` : ""}</span>`
		: item.connectionStatus === "error"
			? `<span class="connection-health error-health">远端连接失败${connectionTime ? ` · ${escapeHTML(connectionTime)}` : ""}</span>`
			: `<span class="connection-health untested">远端尚未验证</span>`;
    const selected = appState.selectedProfiles.has(profile.id);
    return `<article class="profile-card${selected ? " selected" : ""}">
      <div class="card-main">
        <div class="card-content">
          <div class="card-title-row">
            ${isExternal ? "" : `<label class="profile-selector" title="选择 ${escapeHTML(profile.name)}"><input class="profile-select" type="checkbox" data-profile-select="${escapeHTML(profile.id)}" aria-label="选择 ${escapeHTML(profile.name)}" ${selected ? "checked" : ""}></label>`}
            <h2 class="card-title">${escapeHTML(profile.name)}</h2>
            <span class="badge">${type}</span>
            ${connectionStatus}
            <span class="status ${statusClass}">${statusText}</span>
            ${profile.autoStart ? `<span class="badge neutral">自动启动</span>` : ""}
            ${profile.bypassPrivate ? `<span class="badge neutral">局域网直连</span>` : ""}
            ${profile.bypassChina ? `<span class="badge neutral">国内直连</span>` : ""}
          </div>
          <p class="endpoint">${localCapabilities} ${escapeHTML(profile.listenHost)}:${profile.listenPort} · ${escapeHTML(endpoint)}</p>
		  ${item.error ? `<p class="error">启动错误：${escapeHTML(item.error)}</p>` : ""}
		  ${item.connectionError ? `<p class="error connection-error">连接失败：${escapeHTML(item.connectionError)}</p>` : ""}
        </div>
      </div>
      <div class="card-top-actions">
        <div class="card-actions">
          ${isExternal ? "" : `<button class="button compact ${item.running ? "stop" : "start"}" data-profile-action="${primaryAction}" data-id="${escapeHTML(profile.id)}" ${busy ? "disabled" : ""}>${icon(item.running ? "stop" : "play")}${primaryText}</button><button class="button compact secondary icon-only" aria-label="分享 ${escapeHTML(profile.name)}" data-tooltip="分享配置" data-profile-action="share" data-id="${escapeHTML(profile.id)}" ${busy ? "disabled" : ""}>${icon("share")}</button>`}
          <button class="button compact secondary icon-only" aria-label="编辑 ${escapeHTML(profile.name)}" data-tooltip="编辑配置" data-profile-action="edit" data-id="${escapeHTML(profile.id)}" ${busy ? "disabled" : ""}>${icon("edit")}</button>
        </div>
        <label class="default-proxy-switch"><input type="checkbox" role="switch" data-profile-default="${escapeHTML(profile.id)}" ${profile.default ? "checked" : ""} ${busy ? "disabled" : ""}><span class="switch-track" aria-hidden="true"></span><span>默认</span></label>
        <button class="card-delete" aria-label="删除 ${escapeHTML(profile.name)}" data-tooltip="删除配置" data-profile-action="delete" data-id="${escapeHTML(profile.id)}" ${busy ? "disabled" : ""}>${icon("close")}</button>
      </div>
    </article>`;
  }).join("");
  updateSelectionToolbar();
}

function renderClashNodes() {
  const sub = currentSubscription();
  $("#page-eyebrow").textContent = "CLASH SUBSCRIPTION";
  $("#page-title").textContent = sub ? sub.name : "Clash 订阅";
  $("#notice-title").textContent = "订阅节点";
  $("#overview-note").textContent = "选择一个节点启动本地 SOCKS5。设为默认后，未单独指定代理的应用会使用该节点。同一订阅同时只运行一个节点。";
  if (!sub) {
    $("#summary").textContent = "订阅不存在";
    profilesElement.innerHTML = `<div class="empty-state"><h2>找不到这个订阅</h2><p>请切换回「手动添加」，或重新导入 Clash 订阅。</p></div>`;
    updateSelectionToolbar();
    return;
  }
  profilesElement.classList.add("node-grid");
  const busy = appState.busy.has(`clash:${sub.id}`);
  const testing = appState.busy.has(`clash-test:${sub.id}`);
  const metrics = appState.nodeMetrics[sub.id] || {};
  const nodes = (sub.nodes || []).filter((node) => {
    const keyword = appState.nodeFilter.trim().toLowerCase();
    if (!keyword) return true;
    return [node.name, node.type].join(" ").toLowerCase().includes(keyword);
  });
  $("#summary").textContent = `${sub.nodes?.length || 0} 个节点 · ${sub.running ? "本地监听中" : "未启动"} · ${sub.listenAddress}`;
  if (sub.error) {
    $("#overview-note").textContent = `启动错误：${sub.error}`;
  }
  if (!sub.nodes?.length) {
    profilesElement.innerHTML = `<div class="empty-state"><h2>订阅里还没有节点</h2><p>点击“更新订阅”重新拉取，或删除后重新导入。</p></div>`;
    updateSelectionToolbar();
    return;
  }
  if (!nodes.length) {
    profilesElement.innerHTML = `<div class="empty-state"><h2>没有匹配的节点</h2><p>请调整筛选关键词。</p></div>`;
    updateSelectionToolbar();
    return;
  }
  profilesElement.innerHTML = nodes.map((node) => {
    const active = sub.selectedNode === node.name;
    const running = Boolean(sub.running && active);
    const metric = metrics[node.name] || {};
    const delayText = metric.delayError ? "延迟 超时" : metric.delayMs ? `延迟 ${metric.delayMs} ms` : "";
    const speedText = metric.speedError ? "速度 失败" : metric.speedMbps ? `速度 ${Number(metric.speedMbps).toFixed(1)} Mbps` : "";
    return `<article class="profile-card node-card${active ? " selected" : ""}">
      <div class="card-main">
        <div class="card-content">
          <div class="card-title-row">
            <h2 class="card-title">${escapeHTML(node.name)}</h2>
            <span class="badge">${escapeHTML(node.type || "proxy")}</span>
            <span class="status ${running ? "running" : busy ? "busy" : ""}">${running ? "本地监听中" : active ? "已选择" : "未启动"}</span>
            ${sub.profileDefault && active ? `<span class="badge default-badge">默认代理</span>` : ""}
          </div>
          <div class="node-metrics">
            ${delayText ? `<span class="node-metric ${delayClassFor(metric.delayMs, metric.delayError)}">${delayText}</span>` : ""}
            ${speedText ? `<span class="node-metric ${metric.speedError ? "bad" : "good"}">${speedText}</span>` : ""}
          </div>
        </div>
      </div>
      <div class="node-card-footer">
        <div class="node-card-tools">
          <button class="button compact secondary icon-only" aria-label="测试 ${escapeHTML(node.name)} 延迟" data-tooltip="延迟测试" data-clash-test="delay" data-id="${escapeHTML(sub.id)}" data-node="${escapeHTML(node.name)}" ${testing ? "disabled" : ""}>${icon("delay")}</button>
          <button class="button compact secondary icon-only" aria-label="测试 ${escapeHTML(node.name)} 速度" data-tooltip="速度测试" data-clash-test="speed" data-id="${escapeHTML(sub.id)}" data-node="${escapeHTML(node.name)}" ${testing ? "disabled" : ""}>${icon("speed")}</button>
        </div>
        <div class="node-card-primary">
          <button class="button compact ${running ? "stop" : "start"}" data-clash-node="${running ? "stop" : "start"}" data-id="${escapeHTML(sub.id)}" data-node="${escapeHTML(node.name)}" ${busy ? "disabled" : ""}>${icon(running ? "stop" : "play")}${running ? "停止" : "启动"}</button>
          <label class="default-proxy-switch"><input type="checkbox" role="switch" data-clash-default="${escapeHTML(sub.id)}" data-node="${escapeHTML(node.name)}" ${sub.profileDefault && active ? "checked" : ""} ${busy ? "disabled" : ""}><span class="switch-track" aria-hidden="true"></span><span>默认</span></label>
        </div>
      </div>
    </article>`;
  }).join("");
  updateSelectionToolbar();
}

function updateSelectionToolbar() {
  const count = appState.selectedProfiles.size;
  const selectable = manualProfiles().filter((item) => item.profile.type !== "external").length;
  $("#selection-toolbar").hidden = appState.tab !== "proxies" || appState.proxySource !== "manual" || selectable === 0;
  $("#selection-count").textContent = `已选择 ${count} 项`;
  $("#batch-export").disabled = count === 0;
  const allSelected = selectable > 0 && count === selectable;
  $("#select-all-profiles").checked = allSelected;
  $("#select-all-profiles").indeterminate = count > 0 && !allSelected;
}

function syncTabs() {
  const canApps = Boolean(appState.features.appLaunches);
  $("#tab-apps").hidden = !canApps;
  if (!canApps && appState.tab === "apps") appState.tab = "proxies";
  document.querySelectorAll("[data-tab]").forEach((tab) => {
    const active = tab.dataset.tab === appState.tab;
    tab.classList.toggle("active", active);
    tab.setAttribute("aria-selected", active ? "true" : "false");
  });
  document.querySelectorAll("[data-actions]").forEach((group) => {
    group.hidden = group.dataset.actions !== appState.tab;
  });
  profilesElement.hidden = appState.tab !== "proxies";
  launchesElement.hidden = appState.tab !== "apps";
	$("#takeover-panel").hidden = appState.tab !== "apps";
  renderSourceTabs();
  updateSelectionToolbar();
  if (appState.tab === "apps") {
    const hash = "#apps";
    if (location.hash !== hash) history.replaceState(null, "", hash);
  } else if (location.hash === "#apps") {
    history.replaceState(null, "", location.pathname + location.search);
  }
}

function renderTakeover() {
	const panel = $("#takeover-panel");
	if (!panel) return;
	panel.hidden = appState.tab !== "apps";
	const status = appState.takeover || {};
	const checkbox = $("#takeover-enabled");
	checkbox.checked = Boolean(status.enabled);
	checkbox.disabled = appState.busy.has("takeover");
	const labels = { healthy: "运行正常", starting: "正在启动", restarting: "正在恢复", error: "运行异常", idle: "等待应用", stopped: status.enabled ? "等待应用" : "未开启" };
	const badge = $("#takeover-status");
	badge.textContent = labels[status.state] || "状态未知";
	badge.className = `status ${status.state === "healthy" ? "running" : status.state === "starting" || status.state === "restarting" ? "busy" : ""}`;
	$("#takeover-switch-label").textContent = status.enabled ? "已启用" : "启用接管";
	const error = $("#takeover-error");
	error.hidden = status.state !== "error";
	error.textContent = status.state === "error" ? `错误：${status.message || "后台接管服务意外退出，正在等待自动恢复"}${status.restartCount ? `（已尝试 ${status.restartCount} 次）` : ""}` : "";
}

async function toggleTakeover(enabled) {
	if (appState.busy.has("takeover")) return;
	appState.busy.add("takeover");
	renderTakeover();
	try {
		const data = await api("/api/app-takeover", { method: "POST", body: JSON.stringify({ enabled }) });
		appState.takeover = data.takeover || { enabled, state: enabled ? "starting" : "stopped", message: "" };
		showToast(enabled ? "应用网络接管已开启；管理员授权完成后将自动代理列表中的应用" : "应用网络接管已关闭；桌面快捷方式仍可独立使用");
	} catch (error) {
		showToast(error.message, true);
		await loadState(true);
	} finally {
		appState.busy.delete("takeover");
		renderTakeover();
	}
}

function setTab(tab) {
  appState.tab = tab === "apps" && appState.features.appLaunches ? "apps" : "proxies";
  setNavigationOpen(false);
  syncTabs();
  renderProfiles();
  renderLaunches();
}

function setNavigationOpen(open) {
  document.body.classList.toggle("nav-open", open);
  $("#mobile-menu").setAttribute("aria-expanded", open ? "true" : "false");
  $("#sidebar-scrim").hidden = !open;
}

function launchModeLabel(mode) {
  return ({
    chatgpt: "ChatGPT",
    antigravity: "Antigravity IDE",
    cursor: "Cursor",
    chrome: "Google Chrome",
    edge: "Microsoft Edge",
    claude: "Claude Code",
    wechat: "微信 TUN",
    "wechat-windivert": "微信 WinDivert",
    hook: "通用 Hook",
    windivert: "通用 WinDivert"
  })[mode] || mode;
}

function renderLaunches() {
  if (appState.tab !== "apps") return;
  $("#page-eyebrow").textContent = "APPLICATION PROXY";
  $("#page-title").textContent = "应用代理管理";
  $("#summary").textContent = `${appState.launches.length} 个被代理应用`;
  $("#notice-title").textContent = "应用代理说明";
  $("#overview-note").textContent = "这里维护持续生效的进程接管规则。启动新应用请创建桌面快捷方式；快捷方式会读取最新的默认或单独指定代理。";
  if (!appState.launches.length) {
    launchesElement.innerHTML = `<div class="empty-state"><h2>还没有接管应用</h2><p>点击“添加应用”，可从运行进程、桌面快捷方式或 EXE 程序批量添加。</p></div>`;
    return;
  }
  launchesElement.innerHTML = appState.launches.map((entry) => {
    const busy = appState.busy.has(`launch:${entry.id}`);
    const profileText = entry.profileName
      ? `${entry.profileName} · ${entry.listenAddress || "未监听"}`
      : "尚未选择代理配置";
    const statusClass = busy ? "busy" : entry.profileRunning || entry.externalProxy ? "running" : "";
    const statusText = busy ? "正在处理" : entry.usesDefault ? "继承默认代理" : entry.profileName ? "单独指定代理" : "等待设置默认代理";
    return `<article class="profile-card app-card">
      <div class="card-main">
        <div class="card-content">
          <div class="card-title-row">
            <span class="app-avatar" aria-hidden="true">${icon(entry.mode === "wechat" || entry.mode === "wechat-windivert" ? "network" : "apps")}</span>
            <h2 class="card-title">${escapeHTML(entry.name)}</h2>
            <span class="badge">${escapeHTML(entry.modeLabel || launchModeLabel(entry.mode))}</span>
            <span class="status ${statusClass}">${statusText}</span>
          </div>
          <p class="endpoint">${escapeHTML(profileText)}${entry.path ? ` · ${escapeHTML(entry.path)}` : ""}</p>
        </div>
      </div>
      <div class="card-top-actions app-top-actions"><button class="button compact secondary" data-launch-action="shortcut" data-id="${escapeHTML(entry.id)}" ${busy ? "disabled" : ""}>${icon("shortcut")}桌面快捷方式</button><button class="button compact secondary icon-only" aria-label="编辑 ${escapeHTML(entry.name)}" data-tooltip="编辑应用" data-launch-action="edit" data-id="${escapeHTML(entry.id)}" ${busy ? "disabled" : ""}>${icon("edit")}</button><button class="card-delete" aria-label="删除 ${escapeHTML(entry.name)}" data-tooltip="删除应用" data-launch-action="delete" data-id="${escapeHTML(entry.id)}" ${busy ? "disabled" : ""}>${icon("close")}</button></div>
    </article>`;
  }).join("");
}

function fillLaunchProfiles(selectedId) {
  return fillProxySelect($("#launch-profile"), selectedId);
}

function fillProxySelect(select, selectedId = "") {
  const options = appState.profiles.map((item) => {
    const profile = item.profile;
    const selected = profile.id === selectedId ? "selected" : "";
    return `<option value="${escapeHTML(profile.id)}" ${selected}>${escapeHTML(proxyDisplayName(profile))}（${escapeHTML(profile.listenHost)}:${profile.listenPort}）</option>`;
  });
  options.unshift(`<option value="" ${!selectedId ? "selected" : ""}>使用网络代理页的默认代理</option>`);
  select.innerHTML = options.join("");
}

function syncLaunchFields() {
  const mode = $("#launch-mode").value;
  const browser = mode === "chrome" || mode === "edge";
  $("#launch-path-row").hidden = mode === "chatgpt";
  $("#launch-args-row").hidden = mode === "chatgpt";
  $("#launch-isolated-row").hidden = mode !== "antigravity" && mode !== "cursor" && !browser;
  $("#launch-isolated-row span").textContent = browser ? "使用独立代理配置目录（推荐，不影响日常浏览器）" : "使用隔离配置（不影响日常登录状态）";
  $("#launch-dns-row").hidden = mode === "chatgpt" || mode === "chrome" || mode === "edge";
  $("#launch-path-label").textContent = mode === "hook" || mode === "claude" || mode === "windivert" ? "程序路径（创建快捷方式必填）" : "程序路径（可留空自动查找）";
  $("#launch-path").required = false;
  const notes = {
    hook: "接管由共享 WinDivert 完成；桌面快捷方式使用 Hook 启动 TCP 流量。",
    windivert: "接管由共享 WinDivert 完成；填写程序路径后，桌面快捷方式将优先使用通用 Hook 以减少提权。",
    claude: "接管 claude.exe；桌面快捷方式使用 Hook，因此需要填写 Claude 可执行文件路径。",
    chrome: "接管 chrome.exe；桌面快捷方式使用 Chromium 原生 SOCKS5 参数。",
    edge: "接管 msedge.exe；桌面快捷方式使用 Chromium 原生 SOCKS5 参数。"
  };
  $("#launch-mode-note").textContent = notes[mode] || "接管规则对已运行和以后启动的同名进程生效；桌面快捷方式使用轻量启动方式。";
}

async function openLaunchDialog(id = "") {
  const entry = id ? appState.launches.find((item) => item.id === id) : null;
  if (!entry) return;
  appState.editingLaunchId = id;
  launchFormElement.reset();
  $("#launch-error").hidden = true;
  $("#launch-dialog-title").textContent = `编辑 ${entry.name}`;
  $("#launch-app-name").textContent = entry.name;
  $("#launch-name").value = entry.name;
  $("#launch-mode").value = entry.mode;
  $("#launch-path").value = entry?.path || "";
  $("#launch-args").value = entry?.arguments || "";
  $("#launch-udp").value = entry?.udpMode || "auto";
  $("#launch-dns").value = entry?.dns || "";
  $("#launch-processes").value = entry?.processNames || "";
  $("#launch-isolated").checked = Boolean(entry?.isolated);
  $("#launch-attach-existing").value = "true";
  fillLaunchProfiles(entry.profileId || "");
  $("#launch-proxy").value = entry?.proxy || "";
  syncLaunchFields();
  launchDialogElement.showModal();
  $("#launch-path").focus();
}

function closeLaunchDialog() {
  if (launchDialogElement.open) launchDialogElement.close();
  appState.editingLaunchId = "";
}

async function saveLaunch(event) {
  event.preventDefault();
  const saveButton = $("#save-launch");
  const errorElement = $("#launch-error");
  errorElement.hidden = true;
  syncLaunchFields();
  if (!launchFormElement.reportValidity()) return;
  saveButton.disabled = true;
  saveButton.textContent = "保存中…";
  try {
    const data = await api("/api/launches", {
      method: "POST",
      body: JSON.stringify({
        id: appState.editingLaunchId || "",
        name: $("#launch-name").value.trim(),
        mode: $("#launch-mode").value,
        profileId: $("#launch-profile").value,
        proxy: "",
        path: $("#launch-path-row").hidden ? "" : $("#launch-path").value.trim(),
        arguments: $("#launch-args-row").hidden ? "" : $("#launch-args").value.trim(),
        isolated: !$("#launch-isolated-row").hidden && $("#launch-isolated").checked,
        wechatExisting: false,
        udpMode: $("#launch-udp").value,
        dns: $("#launch-dns-row").hidden ? "" : $("#launch-dns").value.trim(),
        processNames: $("#launch-processes").value.trim(),
        attachExisting: true
      })
    });
    closeLaunchDialog();
    showToast(data.applyError ? `应用已保存，但接管未刷新：${data.applyError}` : "应用已保存，接管规则已刷新", Boolean(data.applyError));
    await loadState();
  } catch (error) {
    errorElement.textContent = error.message;
    errorElement.hidden = false;
  } finally {
    saveButton.disabled = false;
    saveButton.textContent = "保存应用";
  }
}

function processApplication(process) {
  const lower = process.name.toLowerCase();
  const known = commonApplications.find((item) => item.processes.toLowerCase().split(";").includes(lower));
  return {
    name: process.name,
    mode: known?.mode || "hook",
    profileId: $("#process-profile").value,
    proxy: "",
    path: process.path,
    arguments: "",
    isolated: false,
    udpMode: "auto",
    dns: "",
    processNames: known?.processes || process.name,
    attachExisting: true
  };
}

function pickedApplication(application) {
  const lowerName = (application.name || "").toLowerCase();
  const lowerPath = (application.path || "").toLowerCase();
  const known = commonApplications.find((item) => {
    const names = item.processes.toLowerCase().split(";");
    return names.includes(lowerName) || names.some((name) => lowerPath.endsWith(`\\${name}`)) ||
      lowerName.includes(item.label.toLowerCase());
  });
  const name = known?.name || application.name;
  return {
    name,
    mode: known?.mode || "hook",
    profileId: "",
    proxy: "",
    path: application.path || "",
    arguments: application.arguments || "",
    isolated: false,
    udpMode: "auto",
    dns: "",
    processNames: known?.processes || name,
    attachExisting: true
  };
}

async function pickApplicationFiles(kind) {
  if (applicationPickerBusy) {
    showToast("文件选择窗口已经打开，请先完成或取消当前选择", true);
    return;
  }
  applicationPickerBusy = true;
  const pickButtons = [...document.querySelectorAll("[data-pick-app]")];
  pickButtons.forEach((button) => { button.disabled = true; });
  showToast(kind === "shortcut" ? "请选择一个或多个 Windows 快捷方式" : "请选择一个或多个 EXE 程序");
  try {
    const picked = await api("/api/application-files/pick", { method: "POST", body: JSON.stringify({ kind }) });
    const applications = (picked.applications || []).map(pickedApplication)
      .filter((item) => item.path || item.mode !== "hook");
    if (!applications.length) {
      if ((picked.applications || []).length) throw new Error("所选快捷方式没有指向可识别的应用程序");
      showToast("已取消选择");
      return;
    }
    const data = await api("/api/launches/bulk", { method: "POST", body: JSON.stringify({ entries: applications }) });
    showToast(data.applyError ? `已添加 ${data.saved} 个应用，但接管未刷新：${data.applyError}` : `已添加 ${data.saved} 个应用，接管规则已刷新`, Boolean(data.applyError));
    await loadState();
  } catch (error) {
    showToast(error.message, true);
  } finally {
    applicationPickerBusy = false;
    pickButtons.forEach((button) => { button.disabled = false; });
  }
}

function closeActionMenus(except = null) {
  document.querySelectorAll("[data-menu]").forEach((menu) => {
    if (menu === except) return;
    const panel = menu.querySelector(".action-menu-panel");
    const trigger = menu.querySelector("[data-menu-trigger]");
    panel.hidden = true;
    trigger.setAttribute("aria-expanded", "false");
  });
}

function toggleActionMenu(trigger) {
  const menu = trigger.closest("[data-menu]");
  const panel = menu.querySelector(".action-menu-panel");
  const opening = panel.hidden;
  closeActionMenus(menu);
  panel.hidden = !opening;
  trigger.setAttribute("aria-expanded", opening ? "true" : "false");
}

async function loadProcessChoices() {
  const list = $("#process-choice-list");
  const refresh = $("#refresh-process-list");
  refresh.disabled = true;
  list.innerHTML = `<div class="empty-state compact-empty"><div class="loading" aria-hidden="true"></div><p>正在读取运行进程</p></div>`;
  try {
    const data = await api("/api/processes");
    appState.runningProcesses = data.processes || [];
    list.innerHTML = appState.runningProcesses.length ? appState.runningProcesses.map((process, index) => `
      <label class="choice-item"><input type="checkbox" name="running-process" value="${index}"><span class="choice-icon">${icon("apps")}</span><span class="choice-copy"><strong>${escapeHTML(process.name)}</strong><small>${escapeHTML(process.path)} · PID ${process.pid}</small></span></label>`).join("") : `<div class="empty-state compact-empty"><p>没有读取到可添加的桌面进程</p></div>`;
    filterProcessChoices();
  } catch (error) {
    list.innerHTML = `<p class="form-error" role="alert">${escapeHTML(error.message)}</p>`;
    $("#process-filter-empty").hidden = true;
  } finally {
    refresh.disabled = false;
  }
}

function filterProcessChoices() {
  const query = $("#process-filter").value.trim().toLocaleLowerCase();
  let visible = 0;
  document.querySelectorAll('#process-choice-list input[name="running-process"]').forEach((input) => {
    const process = appState.runningProcesses[Number(input.value)];
    const matches = !query || (process?.name || "").toLocaleLowerCase().includes(query);
    input.closest(".choice-item").hidden = !matches;
    if (matches) visible += 1;
  });
  $("#process-filter-empty").hidden = !appState.runningProcesses.length || visible > 0;
}

async function openProcessDialog() {
  processFormElement.reset();
  $("#process-error").hidden = true;
  fillProxySelect($("#process-profile"));
  processDialogElement.showModal();
  await loadProcessChoices();
}

function closeProcessDialog() {
  if (processDialogElement.open) processDialogElement.close();
}

function detectedCommonApplicationPath(app) {
  for (const processName of app.processes.split(";")) {
    const path = appState.commonPaths.get(processName.toLowerCase());
    if (path) return path;
  }
  return "";
}

async function saveProcesses(event) {
  event.preventDefault();
  const selected = [...document.querySelectorAll('input[name="running-process"]:checked')]
    .map((input) => appState.runningProcesses[Number(input.value)]).filter(Boolean);
  const errorElement = $("#process-error");
  if (!selected.length) {
    errorElement.textContent = "请至少选择一个运行进程";
    errorElement.hidden = false;
    return;
  }
  const button = $("#save-processes");
  button.disabled = true;
  button.textContent = "正在添加…";
  try {
    const data = await api("/api/launches/bulk", { method: "POST", body: JSON.stringify({ entries: selected.map(processApplication) }) });
    closeProcessDialog();
    showToast(data.applyError ? `已添加 ${data.saved} 个应用，但接管未刷新：${data.applyError}` : `已添加 ${data.saved} 个应用，接管规则已刷新`, Boolean(data.applyError));
    await loadState();
  } catch (error) {
    errorElement.textContent = error.message;
    errorElement.hidden = false;
  } finally {
    button.disabled = false;
    button.textContent = "添加所选应用";
  }
}

async function openCommonDialog() {
  commonFormElement.reset();
  $("#common-error").hidden = true;
  fillProxySelect($("#common-profile"));
  appState.commonPaths.clear();
  try {
    const data = await api("/api/processes");
    for (const process of data.processes || []) appState.commonPaths.set(process.name.toLowerCase(), process.path);
  } catch (_) {}
  $("#common-choice-list").innerHTML = commonApplications.map((app, index) => `
    <label class="choice-item common-choice"><input type="checkbox" name="common-app" value="${index}"><span class="choice-icon">${icon(app.mode === "chrome" || app.mode === "edge" ? "network" : "apps")}</span><span class="choice-copy"><strong>${escapeHTML(app.label)}</strong><small>${escapeHTML(app.processes.replaceAll(";", " · "))}${detectedCommonApplicationPath(app) ? " · 已检测路径" : ""}</small></span></label>`).join("");
  commonDialogElement.showModal();
}

function closeCommonDialog() {
  if (commonDialogElement.open) commonDialogElement.close();
}

async function saveCommonApps(event) {
  event.preventDefault();
  const selected = [...document.querySelectorAll('input[name="common-app"]:checked')]
    .map((input) => commonApplications[Number(input.value)]).filter(Boolean);
  const errorElement = $("#common-error");
  if (!selected.length) {
    errorElement.textContent = "请至少选择一个常见应用";
    errorElement.hidden = false;
    return;
  }
  const profileId = $("#common-profile").value;
  const entries = selected.map((app) => ({
    name: app.name, mode: app.mode, profileId, proxy: "", path: detectedCommonApplicationPath(app), arguments: "",
    isolated: false, udpMode: "auto", dns: "", processNames: app.processes, attachExisting: true
  }));
  const button = $("#save-common-apps");
  button.disabled = true;
  button.textContent = "正在导入…";
  try {
    const data = await api("/api/launches/bulk", { method: "POST", body: JSON.stringify({ entries }) });
    closeCommonDialog();
    showToast(data.applyError ? `已导入 ${data.saved} 个应用，但接管未刷新：${data.applyError}` : `已导入 ${data.saved} 个常见应用，接管规则已刷新`, Boolean(data.applyError));
    await loadState();
  } catch (error) {
    errorElement.textContent = error.message;
    errorElement.hidden = false;
  } finally {
    button.disabled = false;
    button.textContent = "导入所选应用";
  }
}

async function launchAction(action, id) {
  const key = `launch:${id}`;
  if (appState.busy.has(key)) return;
  const entry = appState.launches.find((item) => item.id === id);
  if (!entry) return;
  if (action === "edit") { openLaunchDialog(id); return; }
  if (action === "delete") {
    const confirmed = await showConfirmModal({
      kind: "删除应用",
      title: "删除这个被代理应用？",
      message: `“${entry.name}”将从应用代理列表中移除。`,
      details: "已创建的桌面快捷方式不会自动删除；以后双击它会从快照恢复这个应用配置。如需彻底删除，请同时删除桌面快捷方式。",
      confirmText: "删除应用",
      danger: true
    });
    if (!confirmed) return;
    try {
      const data = await api(`/api/launches/${encodeURIComponent(id)}`, { method: "DELETE" });
      showToast(data.applyError ? `应用已删除，但接管规则刷新失败：${data.applyError}` : "被代理应用已删除，接管规则已刷新", Boolean(data.applyError));
      await loadState();
    }
    catch (error) { showToast(error.message, true); }
    return;
  }
  appState.busy.add(key);
  renderLaunches();
  try {
    let data;
    try {
      data = await api(`/api/launches/${encodeURIComponent(id)}/${action}`, {
        method: "POST",
        body: action === "start" ? JSON.stringify({ confirmRunning: false }) : undefined
      });
    } catch (error) {
      if (action !== "start" || error.data?.code !== "application_already_running") throw error;
      const confirmed = await showConfirmModal({
        kind: "应用正在运行",
        title: `再次启动 ${entry.name}？`,
        message: `检测到“${entry.name}”已经在运行。再次启动可能打开新窗口，也可能由现有进程接管。`,
        details: "只有确认后才会继续；当前正在运行的应用不会被关闭。",
        confirmText: "仍然启动"
      });
      if (!confirmed) return;
      data = await api(`/api/launches/${encodeURIComponent(id)}/${action}`, {
        method: "POST",
        body: JSON.stringify({ confirmRunning: true })
      });
    }
    if (action === "start") {
      showToast(entry.attachExisting
        ? `已接管 ${entry.name}；本次 Lite 运行期间以后启动的同名程序也会生效`
        : `正在启动 ${entry.name}（${data.listenAddress || entry.listenAddress || "本地代理"}）`);
    }
    if (action === "shortcut") showToast(`已创建桌面快捷方式：${data.path || ""}`);
  } catch (error) {
    if (action === "start" && error.data?.code === "proxy_unavailable") {
      await showAlertModal({
        kind: "代理检查失败",
        title: "代理不可用，已停止启动",
        message: error.message,
        details: "请先在“网络代理”中检查或启动该代理，确认连接正常后再试。"
      });
    } else if (action === "start" && error.data?.code === "application_not_running") {
      await showAlertModal({
        kind: "接管失败",
        title: "没有找到正在运行的程序",
        message: error.message,
        details: "请先启动目标程序，或编辑入口后重新选择当前运行的进程。"
      });
    } else if (action === "start" && error.data?.code === "windivert_start_failed") {
      await showAlertModal({
        kind: "WinDivert 权限",
        title: "需要管理员授权",
        message: error.message,
        details: "首次启用接管时会弹出 Windows UAC。请点击“是”允许启动共享 WinDivert 引擎；不需要把 Easy-Net Lite 整体改成管理员运行。如果已经允许仍然失败，请确认使用的是 x64-TUN 完整包。"
      });
    } else if (action === "start") {
      await showAlertModal({
        kind: "启动失败",
        title: `未能启动 ${entry.name}`,
        message: error.message,
        details: "应用没有被静默标记为成功。请根据上面的错误检查程序路径、代理配置和运行权限。"
      });
    } else {
      showToast(error.message, true);
    }
  } finally {
    appState.busy.delete(key);
    await loadState(true);
  }
}

function openProfileDialog(kind, id = "") {
  const item = id ? appState.profiles.find((entry) => entry.profile.id === id) : null;
  const profile = item ? structuredClone(item.profile) : null;
  appState.editingId = id;
  appState.kind = kind;
  formElement.reset();
  $("#form-error").hidden = true;
  $("#form-error").classList.remove("success-result");
  $("#dialog-kind").textContent = kind === "ssh" ? "SSH 代理" : kind === "external" ? "外部 SOCKS5" : "WebSocket 代理";
  $("#dialog-title").textContent = id ? "编辑配置" : "添加配置";
  $("#test-profile").hidden = !id;
  $("#test-profile").disabled = false;
  $("#websocket-fields").hidden = kind !== "websocket";
  $("#ssh-fields").hidden = kind !== "ssh";
  $("#external-fields").hidden = kind !== "external";
  $("#managed-listen-fields").hidden = kind === "external";
  $("#field-bypass-private-row").hidden = kind === "external";
  $("#field-bypass-china-row").hidden = kind === "external";
  document.querySelectorAll(".edit-secret-hint").forEach((element) => { element.hidden = !id; });
  $("#field-name").value = profile?.name || "";
  $("#field-local-port").value = profile?.listenPort || nextPort();
  $("#field-local-port").required = kind !== "external";
  $("#field-auto-start").checked = profile ? profile.autoStart : true;
  $("#field-bypass-private").checked = profile ? Boolean(profile.bypassPrivate) : true;
  $("#field-bypass-china").checked = profile ? Boolean(profile.bypassChina) : true;
  $("#field-external-host").required = kind === "external";
  $("#field-external-port").required = kind === "external";
  $("#field-external-host").value = kind === "external" ? profile?.listenHost || "127.0.0.1" : "";
  $("#field-external-port").value = kind === "external" ? profile?.listenPort || 7890 : "";
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
	} else if (kind === "ssh") {
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
  } else {
	$("#field-ws-url").required = false;
	$("#field-ws-secret").required = false;
	$("#field-ssh-host").required = false;
	$("#field-ssh-user").required = false;
	$("#field-ssh-port").required = false;
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
  errorElement.classList.remove("success-result");
  if (!formElement.reportValidity()) return;
  saveButton.disabled = true;
  saveButton.textContent = "保存中…";
  try {
    const request = await profileRequestFromForm();
    await api("/api/profiles", { method: "POST", body: JSON.stringify(request) });
    dialogElement.close();
    showToast("代理配置已保存");
    await loadState();
  } catch (error) {
    errorElement.textContent = error.message;
    errorElement.hidden = false;
  } finally {
    saveButton.disabled = false;
    saveButton.textContent = "保存配置";
  }
}

async function profileRequestFromForm() {
  const existing = appState.editingId ? appState.profiles.find((entry) => entry.profile.id === appState.editingId)?.profile : null;
  const profile = {
    id: existing?.id || "",
    name: $("#field-name").value.trim(),
    type: appState.kind,
    listenHost: appState.kind === "external" ? $("#field-external-host").value.trim() : "127.0.0.1",
    listenPort: Number(appState.kind === "external" ? $("#field-external-port").value : $("#field-local-port").value),
    autoStart: appState.kind !== "external" && $("#field-auto-start").checked,
    default: Boolean(existing?.default),
    bypassPrivate: $("#field-bypass-private").checked,
    bypassChina: $("#field-bypass-china").checked,
    websocket: null,
    ssh: null
  };
  const request = { profile, websocketSecret: "", sshPassword: "", sshPassphrase: "", sshPrivateKey: "" };
  if (appState.kind === "external") return request;
  if (appState.kind === "websocket") {
    profile.websocket = {
      url: $("#field-ws-url").value.trim(),
      allowInsecure: $("#field-ws-insecure").checked,
      legacyQueryAuth: $("#field-ws-legacy-query").checked
    };
    request.websocketSecret = $("#field-ws-secret").value;
    return request;
  }
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
  return request;
}

async function testEditedProfile() {
  const id = appState.editingId;
  if (!id || appState.busy.has(id)) return;
  const button = $("#test-profile");
  const errorElement = $("#form-error");
  errorElement.hidden = true;
  errorElement.classList.remove("success-result");
  if (!formElement.reportValidity()) return;
  appState.busy.add(id);
  button.disabled = true;
  button.innerHTML = `${icon("bolt")}测试中…`;
  try {
    const request = await profileRequestFromForm();
    await api("/api/profiles", { method: "POST", body: JSON.stringify(request) });
    await api(`/api/profiles/${encodeURIComponent(id)}/test`, { method: "POST" });
    errorElement.classList.add("success-result");
    errorElement.textContent = appState.kind === "ssh" ? "连接成功：SSH 地址和认证信息验证通过。" : appState.kind === "external" ? "连接成功：外部 SOCKS5 握手及测试目标连接正常。" : "连接成功：WebSocket 地址、密钥和隧道握手验证通过。";
    errorElement.hidden = false;
    await loadState(true);
  } catch (error) {
    errorElement.classList.remove("success-result");
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
          errorElement.classList.add("success-result");
          errorElement.textContent = "连接成功：SSH 指纹和认证信息验证通过。";
        } catch (trustError) {
          errorElement.textContent = `连接失败：${trustError.message}`;
        }
      } else {
        errorElement.textContent = "未信任 SSH 服务器指纹，连接测试已取消。";
      }
    } else {
      errorElement.textContent = `连接失败：${error.message}`;
    }
    errorElement.hidden = false;
    await loadState(true);
  } finally {
    appState.busy.delete(id);
    button.disabled = false;
    button.innerHTML = `${icon("bolt")}测试连接`;
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
      $("#share-dialog-title").textContent = `分享“${item.profile.name}”`;
      $("#share-code-label").textContent = "1 个加密分享码";
      shareDialogElement.showModal();
      $("#copy-share-code").focus();
    } catch (error) { showToast(error.message, true); }
    finally { appState.busy.delete(id); await loadState(true); }
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

async function setDefaultProfile(id, enabled) {
  if (appState.busy.has(id)) return;
  appState.busy.add(id);
  renderProfiles();
  try {
    const data = await api(`/api/profiles/${encodeURIComponent(id)}/default`, { method: "POST", body: JSON.stringify({ enabled }) });
    const message = enabled ? "默认代理已切换；未单独指定代理的应用会自动使用它" : "已取消默认代理";
    showToast(data.applyError ? `${message}，但接管规则刷新失败：${data.applyError}` : message, Boolean(data.applyError));
  } catch (error) {
    showToast(error.message, true);
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

async function exportSelectedProfiles() {
  const ids = [...appState.selectedProfiles];
  if (!ids.length) return;
  const button = $("#batch-export");
  button.disabled = true;
  try {
    const data = await api("/api/export", { method: "POST", body: JSON.stringify({ ids }) });
    $("#share-code").value = data.shareCode || (data.shareCodes || []).join("\n");
    $("#share-dialog-title").textContent = "批量导出分享码";
    $("#share-code-label").textContent = `${data.exported || ids.length} 个加密分享码（每行一个）`;
    shareDialogElement.showModal();
    $("#copy-share-code").focus();
  } catch (error) {
    showToast(error.message, true);
  } finally {
    button.disabled = false;
    updateSelectionToolbar();
  }
}

function setAllProfilesSelected(selected) {
  appState.selectedProfiles.clear();
  if (selected) for (const item of manualProfiles()) if (item.profile.type !== "external") appState.selectedProfiles.add(item.profile.id);
  renderProfiles();
}

function openClashImportDialog() {
  clashImportFormElement.reset();
  $("#clash-import-error").hidden = true;
  clashImportDialogElement.showModal();
  $("#clash-import-name").focus();
}

function closeClashImportDialog() {
  if (clashImportDialogElement.open) clashImportDialogElement.close();
  clashImportFormElement.reset();
  $("#clash-import-error").hidden = true;
}

async function importClashSubscription(event) {
  event.preventDefault();
  const button = $("#import-clash-subscription");
  const errorElement = $("#clash-import-error");
  errorElement.hidden = true;
  if (!clashImportFormElement.reportValidity()) return;
  button.disabled = true;
  button.textContent = "导入中…";
  try {
    const data = await api("/api/subscriptions", {
      method: "POST",
      body: JSON.stringify({ name: $("#clash-import-name").value.trim(), url: $("#clash-import-url").value.trim() })
    });
    closeClashImportDialog();
    appState.proxySource = data.id;
    appState.nodeFilter = "";
    showToast(`已导入「${data.name}」，共 ${data.nodes || 0} 个节点`);
    await loadState();
  } catch (error) {
    errorElement.textContent = error.message;
    errorElement.hidden = false;
  } finally {
    button.disabled = false;
    button.textContent = "导入订阅";
  }
}

async function clashSubscriptionAction(action, id) {
  const key = `clash:${id}`;
  if (appState.busy.has(key)) return;
  const sub = (appState.subscriptions || []).find((item) => item.id === id);
  if (action === "delete") {
    const confirmed = await showConfirmModal({
      kind: "删除订阅",
      title: "删除这个 Clash 订阅？",
      message: `「${sub?.name || "订阅"}」及其节点列表会从 Lite 中移除。`,
      details: "正在运行的 mihomo 节点也会停止。此操作无法撤销。",
      confirmText: "删除订阅",
      danger: true
    });
    if (!confirmed) return;
  }
  appState.busy.add(key);
  renderProfiles();
  try {
    if (action === "delete") {
      await api(`/api/subscriptions/${encodeURIComponent(id)}`, { method: "DELETE" });
      appState.proxySource = "manual";
      showToast("Clash 订阅已删除");
    } else if (action === "refresh") {
      const data = await api(`/api/subscriptions/${encodeURIComponent(id)}/refresh`, { method: "POST" });
      showToast(`订阅已更新，共 ${data.nodes || 0} 个节点`);
    } else if (action === "stop") {
      await api(`/api/subscriptions/${encodeURIComponent(id)}/stop`, { method: "POST" });
      showToast("订阅节点已停止");
    }
  } catch (error) {
    showToast(error.message, true);
  } finally {
    appState.busy.delete(key);
    await loadState(true);
  }
}

async function clashNodeAction(action, id, nodeName) {
  const key = `clash:${id}`;
  if (appState.busy.has(key) || !nodeName) return;
  appState.busy.add(key);
  renderProfiles();
  try {
    if (action === "stop") {
      await api(`/api/subscriptions/${encodeURIComponent(id)}/stop`, { method: "POST" });
      showToast("订阅节点已停止");
    } else {
      await api(`/api/subscriptions/${encodeURIComponent(id)}/start`, { method: "POST", body: JSON.stringify({ node: nodeName }) });
      showToast(`已启动节点「${nodeName}」`);
    }
  } catch (error) {
    showToast(error.message, true);
  } finally {
    appState.busy.delete(key);
    await loadState(true);
  }
}

async function setClashNodeDefault(id, nodeName, enabled) {
  const key = `clash:${id}`;
  if (appState.busy.has(key) || !nodeName) return;
  appState.busy.add(key);
  renderProfiles();
  try {
    const data = await api(`/api/subscriptions/${encodeURIComponent(id)}/default`, { method: "POST", body: JSON.stringify({ node: nodeName, enabled }) });
    const message = enabled ? `已将「${nodeName}」设为默认代理` : "已取消默认代理";
    showToast(data.applyError ? `${message}，但接管规则刷新失败：${data.applyError}` : message, Boolean(data.applyError));
  } catch (error) {
    showToast(error.message, true);
  } finally {
    appState.busy.delete(key);
    await loadState(true);
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
    const data = await api("/api/import", { method: "POST", body: JSON.stringify({ shareCode: $("#import-share-code").value.trim() }) });
    closeImportDialog();
    showToast(`已导入 ${data.imported || 1} 个配置，请检查后启动`);
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

function delayClassFor(ms, error) {
  if (error) return "bad";
  if (!ms) return "";
  if (ms < 150) return "good";
  if (ms < 400) return "mid";
  return "bad";
}

function applyNodeMetrics(id, results, kind) {
  const bag = appState.nodeMetrics[id] || {};
  for (const item of results || []) {
    const prev = bag[item.name] || {};
    if (kind === "delay") {
      prev.delayMs = item.delayMs || 0;
      prev.delayError = item.error || "";
    } else {
      prev.speedMbps = item.speedMbps || 0;
      prev.speedError = item.error || "";
    }
    bag[item.name] = prev;
  }
  appState.nodeMetrics[id] = bag;
}

async function clashMetricTest(kind, id, nodeName) {
  const key = `clash-test:${id}`;
  if (appState.busy.has(key) || !id) return;
  appState.busy.add(key);
  renderProfiles();
  const label = kind === "delay" ? "延迟" : "速度";
  showToast(nodeName ? `正在测试「${nodeName}」${label}` : `正在测试全部节点${label}`);
  try {
    const data = await api(`/api/subscriptions/${encodeURIComponent(id)}/${kind}`, {
      method: "POST",
      body: JSON.stringify(nodeName ? { node: nodeName } : {})
    });
    applyNodeMetrics(id, data.results || [], kind);
    const results = data.results || [];
    const failed = results.filter((item) => item.error).length;
    showToast(failed ? `${label}测试完成：${results.length - failed}/${results.length} 可用` : `${label}测试完成`);
  } catch (error) {
    showToast(error.message, true);
  } finally {
    appState.busy.delete(key);
    renderProfiles();
  }
}

function escapeHTML(value) {
  return String(value ?? "").replace(/[&<>'"]/g, (character) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "'": "&#39;", '"': "&quot;" })[character]);
}

document.addEventListener("click", (event) => {
  const menuTrigger = event.target.closest("[data-menu-trigger]");
  if (menuTrigger) { toggleActionMenu(menuTrigger); return; }
  if (!event.target.closest("[data-menu]")) closeActionMenus();
  const pickButton = event.target.closest("[data-pick-app]");
  if (pickButton) { closeActionMenus(); pickApplicationFiles(pickButton.dataset.pickApp); return; }
  const tabButton = event.target.closest("[data-tab]");
  if (tabButton) { setTab(tabButton.dataset.tab); return; }
  const sourceButton = event.target.closest("[data-proxy-source]");
  if (sourceButton) {
    appState.proxySource = sourceButton.dataset.proxySource || "manual";
    appState.nodeFilter = "";
    renderProfiles();
    return;
  }
  const clashAction = event.target.closest("[data-clash-action]");
  if (clashAction) {
    const sub = currentSubscription();
    if (!sub) return;
    const action = clashAction.dataset.clashAction;
    if (action === "delay" || action === "speed") {
      clashMetricTest(action, sub.id, "");
      return;
    }
    clashSubscriptionAction(action, sub.id);
    return;
  }
  const clashTest = event.target.closest("[data-clash-test]");
  if (clashTest) {
    clashMetricTest(clashTest.dataset.clashTest, clashTest.dataset.id, clashTest.dataset.node);
    return;
  }
  const clashNode = event.target.closest("[data-clash-node]");
  if (clashNode) { clashNodeAction(clashNode.dataset.clashNode, clashNode.dataset.id, clashNode.dataset.node); return; }
  const processLaunchButton = event.target.closest("[data-process-launch]");
  if (processLaunchButton) { closeActionMenus(); openProcessDialog(); return; }
  const commonLaunchButton = event.target.closest("[data-common-launch]");
  if (commonLaunchButton) { openCommonDialog(); return; }
  const presetButton = event.target.closest("[data-external-preset]");
  if (presetButton) {
    const presets = { clash: ["Clash", 7890], v2rayn: ["v2rayN", 10808], "clash-meta": ["Clash Meta", 7890] };
    const [name, port] = presets[presetButton.dataset.externalPreset] || ["外部代理", 7890];
    if (!$("#field-name").value.trim()) $("#field-name").value = name;
    $("#field-external-host").value = "127.0.0.1";
    $("#field-external-port").value = port;
    return;
  }
  const launchButton = event.target.closest("[data-launch-action]");
  if (launchButton) { launchAction(launchButton.dataset.launchAction, launchButton.dataset.id); return; }
  const importClashButton = event.target.closest("[data-import-clash]");
  if (importClashButton) { openClashImportDialog(); return; }
  const importButton = event.target.closest("[data-import]");
  if (importButton) { openImportDialog(); return; }
  const addButton = event.target.closest("[data-add]");
  if (addButton) { closeActionMenus(); openProfileDialog(addButton.dataset.add); return; }
  const actionButton = event.target.closest("[data-profile-action]");
  if (actionButton) { profileAction(actionButton.dataset.profileAction, actionButton.dataset.id); return; }
  const commandButton = event.target.closest("[data-command]");
  if (commandButton) globalCommand(commandButton.dataset.command);
});

document.addEventListener("keydown", (event) => {
  if (event.key === "Escape") closeActionMenus();
});

document.addEventListener("change", (event) => {
	if (event.target.id === "takeover-enabled") { toggleTakeover(event.target.checked); return; }
  const clashDefault = event.target.closest("[data-clash-default]");
  if (clashDefault) { setClashNodeDefault(clashDefault.dataset.clashDefault, clashDefault.dataset.node, clashDefault.checked); return; }
  const defaultSwitch = event.target.closest("[data-profile-default]");
  if (defaultSwitch) { setDefaultProfile(defaultSwitch.dataset.profileDefault, defaultSwitch.checked); return; }
  const checkbox = event.target.closest("[data-profile-select]");
  if (!checkbox) return;
  if (checkbox.checked) appState.selectedProfiles.add(checkbox.dataset.profileSelect);
  else appState.selectedProfiles.delete(checkbox.dataset.profileSelect);
  renderProfiles();
});

$("#field-ssh-auth").addEventListener("change", toggleSSHAuth);
$("#test-profile").addEventListener("click", testEditedProfile);
$("#batch-export").addEventListener("click", exportSelectedProfiles);
$("#select-all-profiles").addEventListener("change", (event) => setAllProfilesSelected(event.target.checked));
$("#mobile-menu").addEventListener("click", () => setNavigationOpen(!document.body.classList.contains("nav-open")));
$("#sidebar-scrim").addEventListener("click", () => setNavigationOpen(false));
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
$("#close-clash-import-dialog").addEventListener("click", closeClashImportDialog);
$("#cancel-clash-import-dialog").addEventListener("click", closeClashImportDialog);
clashImportFormElement.addEventListener("submit", importClashSubscription);
$("#node-filter").addEventListener("input", (event) => {
  appState.nodeFilter = event.target.value;
  if (appState.proxySource !== "manual") renderClashNodes();
});
formElement.addEventListener("submit", saveProfile);
launchFormElement.addEventListener("submit", saveLaunch);
$("#launch-mode").addEventListener("change", () => {
  if (!appState.editingLaunchId && ["chrome", "edge"].includes($("#launch-mode").value)) {
    $("#launch-isolated").checked = true;
  }
  syncLaunchFields();
});
$("#launch-profile").addEventListener("change", syncLaunchFields);
$("#refresh-process-list").addEventListener("click", loadProcessChoices);
$("#process-filter").addEventListener("input", filterProcessChoices);
processFormElement.addEventListener("submit", saveProcesses);
commonFormElement.addEventListener("submit", saveCommonApps);
document.querySelectorAll("[data-close-process]").forEach((button) => button.addEventListener("click", closeProcessDialog));
document.querySelectorAll("[data-close-common]").forEach((button) => button.addEventListener("click", closeCommonDialog));
$("#close-launch-dialog").addEventListener("click", closeLaunchDialog);
$("#cancel-launch-dialog").addEventListener("click", closeLaunchDialog);
launchDialogElement.addEventListener("click", (event) => { if (event.target === launchDialogElement) closeLaunchDialog(); });
launchDialogElement.addEventListener("cancel", (event) => { event.preventDefault(); closeLaunchDialog(); });
window.addEventListener("hashchange", () => {
  if (location.hash === "#apps") setTab("apps");
  else if (appState.tab === "apps") setTab("proxies");
});
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
clashImportDialogElement.addEventListener("click", (event) => { if (event.target === clashImportDialogElement) closeClashImportDialog(); });
processDialogElement.addEventListener("click", (event) => { if (event.target === processDialogElement) closeProcessDialog(); });
commonDialogElement.addEventListener("click", (event) => { if (event.target === commonDialogElement) closeCommonDialog(); });
shareDialogElement.addEventListener("cancel", (event) => { event.preventDefault(); closeShareDialog(); });
importDialogElement.addEventListener("cancel", (event) => { event.preventDefault(); closeImportDialog(); });
clashImportDialogElement.addEventListener("cancel", (event) => { event.preventDefault(); closeClashImportDialog(); });
processDialogElement.addEventListener("cancel", (event) => { event.preventDefault(); closeProcessDialog(); });
commonDialogElement.addEventListener("cancel", (event) => { event.preventDefault(); closeCommonDialog(); });
document.addEventListener("visibilitychange", () => { if (!document.hidden) loadState(true); });

loadState();
appState.poll = setInterval(() => { if (!document.hidden && !dialogElement.open && !shareDialogElement.open && !importDialogElement.open && !clashImportDialogElement.open && !launchDialogElement.open && !processDialogElement.open && !commonDialogElement.open) loadState(true); }, 2500);
