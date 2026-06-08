const $ = selector => document.querySelector(selector);

const state = {
  config: null,
  pendingConfirm: null
};

const bytes = value => {
  const number = Number(value || 0);
  if (number >= 1024 ** 3) return `${(number / 1024 ** 3).toFixed(2)} GB`;
  if (number >= 1024 ** 2) return `${(number / 1024 ** 2).toFixed(2)} MB`;
  if (number >= 1024) return `${(number / 1024).toFixed(1)} KB`;
  return `${number} B`;
};

const formData = form => Object.fromEntries(new FormData(form).entries());
const total = usage => Number(usage.uploadBytes || 0) + Number(usage.downloadBytes || 0);
const limitText = value => Number(value || 0) > 0 ? bytes(value) : '不限';
const maskValue = value => value ? '••••••••••••••••' : '-';

const setStatus = (selector, message, type = '') => {
  const element = $(selector);
  element.textContent = message || '';
  element.className = `status ${type}`;
};

const api = async (path, options = {}) => {
  const response = await fetch(path.replace(/^\//, ''), {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...(options.headers || {})
    }
  });
  const contentType = response.headers.get('content-type') || '';
  const data = contentType.includes('application/json') ? await response.json() : await response.text();
  if (!response.ok) throw new Error(data.error || '请求失败');
  return data;
};

const copyText = async (value, label) => {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(value || '');
    } else {
      const input = document.createElement('textarea');
      input.value = value || '';
      input.setAttribute('readonly', '');
      input.style.position = 'fixed';
      input.style.left = '-9999px';
      document.body.appendChild(input);
      input.select();
      document.execCommand('copy');
      document.body.removeChild(input);
    }
    setStatus('#copyStatus', `${label}已复制`, 'ok');
  } catch (err) {
    setStatus('#copyStatus', `复制失败：${err.message}`, 'error');
  }
};

const showApp = loggedIn => {
  $('#login').classList.toggle('hidden', loggedIn);
  $('#app').classList.toggle('hidden', !loggedIn);
  if (!loggedIn) closeAllOverlays();
};

const setSettingsMenu = open => {
  $('#settingsMenu').classList.toggle('hidden', !open);
  $('#settingsBtn').setAttribute('aria-expanded', String(open));
};

const closeSettingsMenu = () => setSettingsMenu(false);

const openPasswordModal = () => {
  closeSettingsMenu();
  $('#passwordForm').reset();
  setStatus('#passwordStatus', '');
  $('#passwordModal').classList.remove('hidden');
};

const closePasswordModal = () => {
  $('#passwordModal').classList.add('hidden');
  setStatus('#passwordStatus', '');
};

const openConfirmModal = ({ title, message, confirmText, confirmClass = 'danger', onConfirm }) => {
  closeSettingsMenu();
  state.pendingConfirm = onConfirm;
  $('#confirmTitle').textContent = title;
  $('#confirmMessage').textContent = message;
  $('#confirmActionBtn').textContent = confirmText;
  $('#confirmActionBtn').className = confirmClass;
  setStatus('#confirmStatus', '');
  $('#confirmModal').classList.remove('hidden');
};

const closeConfirmModal = () => {
  $('#confirmModal').classList.add('hidden');
  state.pendingConfirm = null;
  setStatus('#confirmStatus', '');
};

const closeAllOverlays = () => {
  closeSettingsMenu();
  closePasswordModal();
  closeConfirmModal();
};

const render = data => {
  const { user, config } = data;
  state.config = config;
  $('#userTitle').textContent = `${user.nickname || user.username} 的个人流量和配置`;
  $('#usageMetrics').innerHTML = `
    <div class="metric"><span>今日已用</span><strong>${bytes(total(user.usage.today))}</strong><p>额度 ${limitText(user.dailyLimitBytes)}</p></div>
    <div class="metric"><span>本月已用</span><strong>${bytes(total(user.usage.month))}</strong><p>额度 ${limitText(user.monthlyLimitBytes)}</p></div>
    <div class="metric"><span>今日上行</span><strong>${bytes(user.usage.today.uploadBytes)}</strong></div>
    <div class="metric"><span>今日下行</span><strong>${bytes(user.usage.today.downloadBytes)}</strong></div>
  `;
  $('#configHost').textContent = maskValue(config.serverWsUrl || config.workerHost);
  $('#configSecret').textContent = maskValue(config.secret);
};

const loadMe = async () => {
  try {
    const data = await api('api/user/me');
    showApp(true);
    render(data);
  } catch (err) {
    showApp(false);
  }
};

$('#loginForm').addEventListener('submit', async event => {
  event.preventDefault();
  const form = event.currentTarget;
  setStatus('#loginStatus', '登录中...');
  try {
    await api('api/user/login', {
      method: 'POST',
      body: JSON.stringify(formData(form))
    });
    form.reset();
    setStatus('#loginStatus', '');
    await loadMe();
  } catch (err) {
    setStatus('#loginStatus', err.message, 'error');
  }
});

$('#settingsBtn').addEventListener('click', event => {
  event.stopPropagation();
  setSettingsMenu($('#settingsMenu').classList.contains('hidden'));
});

$('#settingsMenu').addEventListener('click', event => {
  event.stopPropagation();
});

document.addEventListener('click', closeSettingsMenu);

document.addEventListener('keydown', event => {
  if (event.key === 'Escape') closeAllOverlays();
});

$('#openPasswordModalBtn').addEventListener('click', openPasswordModal);
$('#closePasswordModalBtn').addEventListener('click', closePasswordModal);
$('#cancelPasswordBtn').addEventListener('click', closePasswordModal);
$('#passwordModal').addEventListener('click', event => {
  if (event.target === event.currentTarget) closePasswordModal();
});

$('#openResetSecretConfirmBtn').addEventListener('click', () => {
  openConfirmModal({
    title: '重置代理密钥',
    message: '确认后当前代理密钥会立即失效，需要在客户端重新配置新的密钥。',
    confirmText: '重置代理密钥',
    onConfirm: async () => {
      await api('api/user/secret', {
        method: 'POST',
        body: '{}'
      });
      await loadMe();
      setStatus('#copyStatus', '代理密钥已重置，请复制新的连接密钥', 'ok');
    }
  });
});

$('#openLogoutConfirmBtn').addEventListener('click', () => {
  openConfirmModal({
    title: '退出登录',
    message: '确定要退出当前用户端吗？',
    confirmText: '退出',
    onConfirm: async () => {
      await api('api/user/logout', { method: 'POST', body: '{}' });
      showApp(false);
    }
  });
});

$('#closeConfirmBtn').addEventListener('click', closeConfirmModal);
$('#cancelConfirmBtn').addEventListener('click', closeConfirmModal);
$('#confirmModal').addEventListener('click', event => {
  if (event.target === event.currentTarget) closeConfirmModal();
});

$('#copyWsBtn').addEventListener('click', () => {
  copyText(state.config?.serverWsUrl || state.config?.workerHost || '', 'WebSocket 地址');
});

$('#copySecretBtn').addEventListener('click', () => {
  copyText(state.config?.secret || '', '连接密钥');
});

$('#passwordForm').addEventListener('submit', async event => {
  event.preventDefault();
  const form = event.currentTarget;
  try {
    await api('api/user/password', {
      method: 'POST',
      body: JSON.stringify(formData(form))
    });
    form.reset();
    setStatus('#passwordStatus', '密码已修改', 'ok');
  } catch (err) {
    setStatus('#passwordStatus', err.message, 'error');
  }
});

$('#confirmActionBtn').addEventListener('click', async () => {
  if (!state.pendingConfirm) return;
  const button = $('#confirmActionBtn');
  const action = state.pendingConfirm;
  button.disabled = true;
  setStatus('#confirmStatus', '处理中...');
  try {
    await action();
    closeConfirmModal();
  } catch (err) {
    setStatus('#confirmStatus', err.message, 'error');
  } finally {
    button.disabled = false;
  }
});

loadMe();
