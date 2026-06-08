const $ = selector => document.querySelector(selector);
const $$ = selector => Array.from(document.querySelectorAll(selector));

const state = {
  users: [],
  stats: null,
  userPage: 1,
  userPageSize: 20,
  userSearch: '',
  editingUserId: null
};

const bytes = value => {
  const number = Number(value || 0);
  if (number >= 1024 ** 3) return `${(number / 1024 ** 3).toFixed(2)} GB`;
  if (number >= 1024 ** 2) return `${(number / 1024 ** 2).toFixed(2)} MB`;
  if (number >= 1024) return `${(number / 1024).toFixed(1)} KB`;
  return `${number} B`;
};

const total = usage => Number(usage.uploadBytes || 0) + Number(usage.downloadBytes || 0);
const limitText = value => Number(value || 0) > 0 ? bytes(value) : '不限';
const formData = form => Object.fromEntries(new FormData(form).entries());

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

const copyText = async text => {
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const input = document.createElement('textarea');
  input.value = text;
  input.setAttribute('readonly', '');
  input.style.position = 'fixed';
  input.style.left = '-9999px';
  document.body.appendChild(input);
  input.select();
  document.execCommand('copy');
  document.body.removeChild(input);
};

const showApp = loggedIn => {
  $('#login').classList.toggle('hidden', loggedIn);
  $('#app').classList.toggle('hidden', !loggedIn);
  if (!loggedIn) closeUserModal();
};

const loadMe = async () => {
  try {
    await api('api/admin/me');
    showApp(true);
    await Promise.all([loadUsers(), loadStats(), loadSettings()]);
  } catch (err) {
    showApp(false);
  }
};

const loadUsers = async () => {
  const data = await api('api/admin/users');
  state.users = data.users;
  renderUsers();
};

const filteredUsers = () => {
  const keyword = state.userSearch.trim().toLowerCase();
  if (!keyword) return state.users;
  return state.users.filter(user => [
    user.username,
    user.nickname,
    user.remark
  ].some(value => String(value || '').toLowerCase().includes(keyword)));
};

const pagedUsers = () => {
  const users = filteredUsers();
  const totalPages = Math.max(1, Math.ceil(users.length / state.userPageSize));
  state.userPage = Math.min(Math.max(1, state.userPage), totalPages);
  const start = (state.userPage - 1) * state.userPageSize;
  return {
    rows: users.slice(start, start + state.userPageSize),
    total: users.length,
    totalPages,
    start
  };
};

const renderUsers = () => {
  const page = pagedUsers();
  $('#usersBody').innerHTML = page.rows.map(user => `
    <tr>
      <td>
        <strong>${escapeHtml(user.username)}</strong>
        <div class="muted">${escapeHtml(user.nickname || '')}</div>
        <div>${user.active ? '<span class="badge">启用</span>' : '<span class="badge off">停用</span>'}</div>
      </td>
      <td>
        <strong>${bytes(total(user.usage.today))}</strong>
        <div class="muted">上 ${bytes(user.usage.today.uploadBytes)} / 下 ${bytes(user.usage.today.downloadBytes)}</div>
      </td>
      <td>
        <strong>${bytes(total(user.usage.month))}</strong>
        <div class="muted">上 ${bytes(user.usage.month.uploadBytes)} / 下 ${bytes(user.usage.month.downloadBytes)}</div>
      </td>
      <td>
        <div>日 ${limitText(user.dailyLimitBytes)}</div>
        <div>月 ${limitText(user.monthlyLimitBytes)}</div>
      </td>
      <td>${escapeHtml(user.remark || '')}</td>
      <td>
        <div class="toolbar table-actions">
          <button class="secondary" type="button" data-action="edit" data-id="${user.id}">编辑</button>
          <button class="secondary" type="button" data-action="toggle" data-id="${user.id}">${user.active ? '停用' : '启用'}</button>
          <button class="secondary" type="button" data-action="reset" data-id="${user.id}">重置今日</button>
          <button class="secondary" type="button" data-action="copy-config" data-id="${user.id}">复制配置</button>
          <button class="danger" type="button" data-action="delete" data-id="${user.id}">删除</button>
        </div>
      </td>
    </tr>
  `).join('');

  if (page.rows.length === 0) {
    $('#usersBody').innerHTML = '<tr><td colspan="6" class="muted">没有匹配的用户</td></tr>';
  }

  const from = page.total === 0 ? 0 : page.start + 1;
  const to = Math.min(page.start + page.rows.length, page.total);
  $('#userPageInfo').textContent = `第 ${state.userPage} / ${page.totalPages} 页，共 ${page.total} 条，当前 ${from}-${to}`;
  $('#prevPageBtn').disabled = state.userPage <= 1;
  $('#nextPageBtn').disabled = state.userPage >= page.totalPages;
};

const openUserModal = user => {
  const form = $('#userModalForm');
  const fields = form.elements;
  form.reset();
  setStatus('#userModalStatus', '');
  state.editingUserId = user ? Number(user.id) : null;
  $('#userModalTitle').textContent = user ? `编辑用户：${user.username}` : '新增用户';
  fields.id.value = user?.id || '';
  fields.username.value = user?.username || '';
  fields.username.readOnly = Boolean(user);
  fields.password.required = !user;
  fields.password.placeholder = user ? '留空则不修改' : '至少 8 位';
  fields.nickname.value = user?.nickname || '';
  fields.dailyLimitGb.value = user?.dailyLimitGb ?? 0;
  fields.monthlyLimitGb.value = user?.monthlyLimitGb ?? 0;
  fields.active.value = user?.active === false ? 'false' : 'true';
  fields.remark.value = user?.remark || '';
  $('#resetSecretBtn').classList.toggle('hidden', !user);
  $('#userModal').classList.remove('hidden');
};

const closeUserModal = () => {
  $('#userModal').classList.add('hidden');
  state.editingUserId = null;
};

const saveUserFromModal = async event => {
  event.preventDefault();
  const form = event.currentTarget;
  const data = formData(form);
  const payload = {
    username: data.username,
    password: data.password,
    nickname: data.nickname,
    dailyLimitGb: data.dailyLimitGb,
    monthlyLimitGb: data.monthlyLimitGb,
    remark: data.remark,
    active: data.active === 'true'
  };

  if (state.editingUserId) {
    await api(`api/admin/users/${state.editingUserId}`, {
      method: 'PUT',
      body: JSON.stringify(payload)
    });
    setStatus('#userStatus', '用户已更新', 'ok');
  } else {
    await api('api/admin/users', {
      method: 'POST',
      body: JSON.stringify(payload)
    });
    setStatus('#userStatus', '用户已创建，密钥已自动生成', 'ok');
  }

  closeUserModal();
  await loadUsers();
};

const updateUser = async (user, patch) => {
  await api(`api/admin/users/${user.id}`, {
    method: 'PUT',
    body: JSON.stringify({
      nickname: user.nickname || '',
      remark: user.remark || '',
      dailyLimitGb: user.dailyLimitGb,
      monthlyLimitGb: user.monthlyLimitGb,
      active: user.active,
      ...patch
    })
  });
};

const copyUserConfig = async id => {
  const config = await api(`api/admin/users/${id}/config`);
  const text = `websocket地址：\n${config.serverWsUrl || ''}\n密钥：\n${config.secret || ''}`;
  await copyText(text);
  setStatus('#userStatus', '配置已复制', 'ok');
};

const loadStats = async () => {
  state.stats = await api('api/admin/stats');
  renderStats();
};

const renderStats = () => {
  const stats = state.stats;
  const totalToday = stats.daily[0] ? Number(stats.daily[0].uploadBytes || 0) + Number(stats.daily[0].downloadBytes || 0) : 0;
  $('#statsMetrics').innerHTML = `
    <div class="metric"><span>活跃连接</span><strong>${stats.runtime.connections.activeConnections}</strong></div>
    <div class="metric"><span>累计连接</span><strong>${stats.runtime.connections.totalConnections}</strong></div>
    <div class="metric"><span>今日总流量</span><strong>${bytes(totalToday)}</strong></div>
    <div class="metric"><span>用户数</span><strong>${stats.users.length}</strong></div>
    <div class="metric"><span>内存 RSS</span><strong>${bytes(stats.runtime.memory.rss)}</strong></div>
    <div class="metric"><span>运行时长</span><strong>${Math.floor(stats.runtime.uptimeSeconds / 60)} 分钟</strong></div>
  `;
  $('#dailyBody').innerHTML = stats.daily.map(row => `
    <tr>
      <td>${row.day}</td>
      <td>${bytes(row.uploadBytes)}</td>
      <td>${bytes(row.downloadBytes)}</td>
      <td><strong>${bytes(Number(row.uploadBytes || 0) + Number(row.downloadBytes || 0))}</strong></td>
      <td>${row.connections}</td>
      <td>${row.failedConnections}</td>
    </tr>
  `).join('');
};

const loadSettings = async () => {
  const settings = await api('api/admin/settings');
  $('#settingsForm').clientWsUrl.value = settings.clientWsUrl || '';
  $('#settingsForm').contextPath.value = settings.contextPath || '/';
};

const escapeHtml = value => String(value || '').replace(/[&<>"']/g, char => ({
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#039;'
}[char]));

$('#loginForm').addEventListener('submit', async event => {
  event.preventDefault();
  setStatus('#loginStatus', '登录中...');
  try {
    await api('api/admin/login', {
      method: 'POST',
      body: JSON.stringify({ password: $('#adminPassword').value })
    });
    $('#adminPassword').value = '';
    setStatus('#loginStatus', '');
    await loadMe();
  } catch (err) {
    setStatus('#loginStatus', err.message, 'error');
  }
});

$('#logoutBtn').addEventListener('click', async () => {
  await api('api/admin/logout', { method: 'POST', body: '{}' });
  showApp(false);
});

$('#openCreateUserBtn').addEventListener('click', () => openUserModal(null));
$('#closeUserModalBtn').addEventListener('click', closeUserModal);
$('#userModal').addEventListener('click', event => {
  if (event.target === event.currentTarget) closeUserModal();
});

$('#userModalForm').addEventListener('submit', async event => {
  try {
    setStatus('#userModalStatus', '保存中...');
    await saveUserFromModal(event);
  } catch (err) {
    setStatus('#userModalStatus', err.message, 'error');
  }
});

$('#resetSecretBtn').addEventListener('click', async () => {
  if (!state.editingUserId) return;
  try {
    await api(`api/admin/users/${state.editingUserId}`, {
      method: 'PUT',
      body: JSON.stringify({ resetSecret: true })
    });
    setStatus('#userModalStatus', '密钥已重置', 'ok');
    await loadUsers();
  } catch (err) {
    setStatus('#userModalStatus', err.message, 'error');
  }
});

$('#usersBody').addEventListener('click', async event => {
  const button = event.target.closest('button[data-action]');
  if (!button) return;
  const id = Number(button.dataset.id);
  const user = state.users.find(item => Number(item.id) === id);
  if (!user) return;

  try {
    if (button.dataset.action === 'edit') openUserModal(user);
    if (button.dataset.action === 'toggle') {
      await updateUser(user, { active: !user.active });
      setStatus('#userStatus', user.active ? '用户已停用' : '用户已启用', 'ok');
      await loadUsers();
    }
    if (button.dataset.action === 'reset') {
      if (!confirm('确定重置该用户今日流量吗？')) return;
      await api(`api/admin/users/${id}/reset-daily`, { method: 'POST', body: '{}' });
      setStatus('#userStatus', '今日流量已重置', 'ok');
      await loadUsers();
    }
    if (button.dataset.action === 'copy-config') await copyUserConfig(id);
    if (button.dataset.action === 'delete') {
      if (!confirm('确定删除该用户吗？')) return;
      await api(`api/admin/users/${id}`, { method: 'DELETE' });
      setStatus('#userStatus', '用户已删除', 'ok');
      await loadUsers();
    }
  } catch (err) {
    setStatus('#userStatus', err.message, 'error');
  }
});

$('#refreshUsersBtn').addEventListener('click', loadUsers);
$('#refreshStatsBtn').addEventListener('click', loadStats);

$('#userSearchInput').addEventListener('input', event => {
  state.userSearch = event.target.value;
  state.userPage = 1;
  renderUsers();
});

$('#pageSizeSelect').addEventListener('change', event => {
  state.userPageSize = Number(event.target.value) || 20;
  state.userPage = 1;
  renderUsers();
});

$('#prevPageBtn').addEventListener('click', () => {
  state.userPage -= 1;
  renderUsers();
});

$('#nextPageBtn').addEventListener('click', () => {
  state.userPage += 1;
  renderUsers();
});

$('#settingsForm').addEventListener('submit', async event => {
  event.preventDefault();
  const form = event.currentTarget;
  try {
    await api('api/admin/settings', {
      method: 'PUT',
      body: JSON.stringify(formData(form))
    });
    setStatus('#settingsStatus', '设置已保存', 'ok');
  } catch (err) {
    setStatus('#settingsStatus', err.message, 'error');
  }
});

$('#adminPasswordForm').addEventListener('submit', async event => {
  event.preventDefault();
  const form = event.currentTarget;
  try {
    await api('api/admin/password', {
      method: 'POST',
      body: JSON.stringify(formData(form))
    });
    form.reset();
    setStatus('#passwordStatus', '管理员密码已修改', 'ok');
  } catch (err) {
    setStatus('#passwordStatus', err.message, 'error');
  }
});

$$('.tab').forEach(tab => {
  tab.addEventListener('click', () => {
    $$('.tab').forEach(item => item.classList.remove('active'));
    $$('.tab-page').forEach(page => page.classList.add('hidden'));
    tab.classList.add('active');
    $(`#tab-${tab.dataset.tab}`).classList.remove('hidden');
    if (tab.dataset.tab === 'stats') loadStats();
  });
});

loadMe();
