const http = require('http');
const WebSocket = require('ws');
const net = require('net');
const url = require('url');
const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const initSqlJs = require('sql.js');

const positiveNumber = (value, fallback) => {
  const number = Number(value);
  return Number.isFinite(number) && number > 0 ? number : fallback;
};

const PORT = process.env.PORT || 3000;
const DATA_DIR = process.env.DATA_DIR || path.join(__dirname, 'data');
const DB_FILE = process.env.DB_FILE || path.join(DATA_DIR, 'easy-net.sqlite');
const CONTEXT_PATH = normalizeContextPath(process.env.CONTEXT_PATH || '');
const MONITOR_INTERVAL_SECONDS = Number(process.env.MONITOR_INTERVAL_SECONDS || 0);
const MAX_WS_PAYLOAD_BYTES = positiveNumber(process.env.MAX_WS_PAYLOAD_BYTES, 1024 * 1024);
const WS_BACKPRESSURE_LIMIT_BYTES = positiveNumber(process.env.WS_BACKPRESSURE_LIMIT_BYTES, 4 * 1024 * 1024);
const WS_BACKPRESSURE_RESUME_BYTES = positiveNumber(
  process.env.WS_BACKPRESSURE_RESUME_BYTES,
  Math.max(1024 * 1024, Math.floor(WS_BACKPRESSURE_LIMIT_BYTES / 2))
);
const LOGIN_MAX_FAILURES = positiveNumber(process.env.LOGIN_MAX_FAILURES, 5);
const LOGIN_LOCK_MINUTES = positiveNumber(process.env.LOGIN_LOCK_MINUTES, 15);
const LOGIN_FAILURE_WINDOW_MINUTES = positiveNumber(process.env.LOGIN_FAILURE_WINDOW_MINUTES, 15);
const SESSION_TTL_HOURS = positiveNumber(process.env.SESSION_TTL_HOURS, 12);
const TRAFFIC_FLUSH_SECONDS = positiveNumber(process.env.TRAFFIC_FLUSH_SECONDS, 5);

const allowedLegacySecrets = process.env.SECRETS
  ? process.env.SECRETS.split(',').map(s => s.trim()).filter(Boolean)
  : [];
const ADMIN_KEY = process.env.ADMIN_KEY || null;

let SQL = null;
let db = null;
let dbSaveTimer = null;
let dbDirty = false;

const sessions = new Map();
const usageCache = new Map();
const trafficBuffer = new Map();

const runtimeStats = {
  startedAt: new Date().toISOString(),
  totalConnections: 0,
  rejectedUpgrades: 0,
  activeConnections: 0,
  maxActiveConnections: 0,
  targetErrors: 0,
  websocketErrors: 0,
  deadConnectionsTerminated: 0,
  backpressureEvents: 0,
  maxWsBufferedAmount: 0,
  pausedTargetReads: 0,
  resumedTargetReads: 0,
  quotaClosedConnections: 0
};

const hashPassword = (password, salt = crypto.randomBytes(16).toString('hex')) => {
  const hash = crypto.pbkdf2Sync(String(password), salt, 120000, 32, 'sha256').toString('hex');
  return { salt, hash };
};

const verifyPassword = (password, salt, hash) => {
  const actual = hashPassword(password, salt).hash;
  const actualBuf = Buffer.from(actual, 'hex');
  const expectedBuf = Buffer.from(hash, 'hex');
  return actualBuf.length === expectedBuf.length && crypto.timingSafeEqual(actualBuf, expectedBuf);
};

const randomSecret = () => crypto.randomBytes(24).toString('base64url');
const nowIso = () => new Date().toISOString();
const todayKey = () => new Date().toISOString().slice(0, 10);
const monthKey = () => new Date().toISOString().slice(0, 7);
const safeInt = value => {
  const number = Number(value);
  return Number.isFinite(number) && number > 0 ? Math.floor(number) : 0;
};
const toBytesFromGb = value => safeInt(Number(value) * 1024 * 1024 * 1024);
const fromBytesToGb = value => Math.round((Number(value || 0) / 1024 / 1024 / 1024) * 1000) / 1000;

function normalizeContextPath(value) {
  const trimmed = String(value || '').trim();
  if (!trimmed || trimmed === '/') return '';
  return `/${trimmed.replace(/^\/+|\/+$/g, '')}`;
}

const withContextPath = pathname => `${CONTEXT_PATH}${pathname === '/' ? '' : pathname}` || '/';

const stripContextPath = pathname => {
  if (!CONTEXT_PATH) return pathname;
  if (pathname === CONTEXT_PATH) return '/';
  if (pathname.startsWith(`${CONTEXT_PATH}/`)) {
    return pathname.slice(CONTEXT_PATH.length) || '/';
  }
  return null;
};

const ensureDataDir = () => {
  fs.mkdirSync(path.dirname(DB_FILE), { recursive: true });
};

const saveDbNow = () => {
  if (!db) return;
  ensureDataDir();
  const data = Buffer.from(db.export());
  const tempFile = `${DB_FILE}.tmp`;
  fs.writeFileSync(tempFile, data);
  fs.renameSync(tempFile, DB_FILE);
  dbDirty = false;
};

const scheduleDbSave = () => {
  dbDirty = true;
  if (dbSaveTimer) return;
  dbSaveTimer = setTimeout(() => {
    dbSaveTimer = null;
    if (dbDirty) saveDbNow();
  }, 250);
  dbSaveTimer.unref();
};

const run = (sql, params = [], persist = true) => {
  db.run(sql, params);
  if (persist) scheduleDbSave();
};

const all = (sql, params = []) => {
  const stmt = db.prepare(sql, params);
  const rows = [];
  while (stmt.step()) rows.push(stmt.getAsObject());
  stmt.free();
  return rows;
};

const get = (sql, params = []) => all(sql, params)[0] || null;

const execute = sql => {
  db.run(sql);
  scheduleDbSave();
};

const initDatabase = async () => {
  ensureDataDir();
  SQL = await initSqlJs({
    locateFile: file => path.join(__dirname, 'node_modules', 'sql.js', 'dist', file)
  });
  if (fs.existsSync(DB_FILE)) {
    db = new SQL.Database(fs.readFileSync(DB_FILE));
  } else {
    db = new SQL.Database();
  }

  execute(`
    CREATE TABLE IF NOT EXISTS admin_credentials (
      id INTEGER PRIMARY KEY CHECK (id = 1),
      password_hash TEXT NOT NULL,
      password_salt TEXT NOT NULL,
      updated_at TEXT NOT NULL
    );

    CREATE TABLE IF NOT EXISTS users (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      username TEXT NOT NULL UNIQUE,
      password_hash TEXT NOT NULL,
      password_salt TEXT NOT NULL,
      nickname TEXT DEFAULT '',
      secret TEXT NOT NULL UNIQUE,
      remark TEXT DEFAULT '',
      daily_limit_bytes INTEGER NOT NULL DEFAULT 0,
      monthly_limit_bytes INTEGER NOT NULL DEFAULT 0,
      active INTEGER NOT NULL DEFAULT 1,
      created_at TEXT NOT NULL,
      updated_at TEXT NOT NULL
    );

    CREATE TABLE IF NOT EXISTS traffic_daily (
      user_id INTEGER NOT NULL,
      day TEXT NOT NULL,
      upload_bytes INTEGER NOT NULL DEFAULT 0,
      download_bytes INTEGER NOT NULL DEFAULT 0,
      connections INTEGER NOT NULL DEFAULT 0,
      failed_connections INTEGER NOT NULL DEFAULT 0,
      PRIMARY KEY (user_id, day)
    );

    CREATE TABLE IF NOT EXISTS settings (
      key TEXT PRIMARY KEY,
      value TEXT NOT NULL
    );

    CREATE TABLE IF NOT EXISTS login_attempts (
      kind TEXT NOT NULL,
      username TEXT NOT NULL,
      failed_count INTEGER NOT NULL DEFAULT 0,
      locked_until INTEGER NOT NULL DEFAULT 0,
      updated_at INTEGER NOT NULL,
      PRIMARY KEY (kind, username)
    );
  `);

  const admin = get('SELECT id FROM admin_credentials WHERE id = 1');
  if (!admin) {
    const initialPassword = process.env.ADMIN_PASSWORD || process.env.ADMIN_KEY || randomSecret();
    const { salt, hash } = hashPassword(initialPassword);
    run(
      'INSERT INTO admin_credentials (id, password_hash, password_salt, updated_at) VALUES (1, ?, ?, ?)',
      [hash, salt, nowIso()]
    );
    console.log(`[Easy-Net] 初始管理员密码: ${initialPassword}`);
    console.log('[Easy-Net] 请登录管理端后尽快修改管理员密码。');
  }

  setDefaultSetting('client_ws_url', process.env.CLIENT_WS_URL || '');
  setDefaultSetting('client_host', process.env.CLIENT_HOST || '');
  setDefaultSetting('client_local_port', process.env.CLIENT_LOCAL_PORT || '1080');

  allowedLegacySecrets.forEach((secret, index) => {
    const existing = get('SELECT id FROM users WHERE secret = ?', [secret]);
    if (!existing) {
      const password = randomSecret();
      const username = `legacy_${index + 1}`;
      const { salt, hash } = hashPassword(password);
      run(
        `INSERT INTO users
          (username, password_hash, password_salt, nickname, secret, remark, daily_limit_bytes, monthly_limit_bytes, active, created_at, updated_at)
         VALUES (?, ?, ?, ?, ?, ?, 0, 0, 1, ?, ?)`,
        [username, hash, salt, `Legacy ${index + 1}`, secret, '由 SECRETS 环境变量自动迁移', nowIso(), nowIso()]
      );
      console.log(`[Easy-Net] 已迁移旧密钥为用户 ${username}，初始密码: ${password}`);
    }
  });

  saveDbNow();
};

const setDefaultSetting = (key, value) => {
  const existing = get('SELECT value FROM settings WHERE key = ?', [key]);
  if (!existing) {
    run('INSERT INTO settings (key, value) VALUES (?, ?)', [key, value || '']);
  }
};

const getSetting = key => {
  const row = get('SELECT value FROM settings WHERE key = ?', [key]);
  return row ? row.value : '';
};

const setSetting = (key, value) => {
  run(
    `INSERT INTO settings (key, value) VALUES (?, ?)
     ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
    [key, String(value || '')]
  );
};

const readNumberFile = filePath => {
  try {
    const value = fs.readFileSync(filePath, 'utf8').trim();
    const number = Number(value);
    return Number.isFinite(number) ? number : null;
  } catch (err) {
    return null;
  }
};

const readMemoryStat = () => {
  const paths = ['/sys/fs/cgroup/memory.stat', '/sys/fs/cgroup/memory/memory.stat'];
  for (const filePath of paths) {
    try {
      const stat = {};
      fs.readFileSync(filePath, 'utf8').trim().split('\n').forEach(line => {
        const [key, value] = line.trim().split(/\s+/);
        const number = Number(value);
        if (key && Number.isFinite(number)) stat[key] = number;
      });
      return stat;
    } catch (err) {
      // Try the next cgroup layout.
    }
  }
  return null;
};

const getContainerMemory = () => {
  const current =
    readNumberFile('/sys/fs/cgroup/memory.current') ??
    readNumberFile('/sys/fs/cgroup/memory/memory.usage_in_bytes');
  const maxRaw = (() => {
    try {
      const value = fs.readFileSync('/sys/fs/cgroup/memory.max', 'utf8').trim();
      return value === 'max' ? null : Number(value);
    } catch (err) {
      return readNumberFile('/sys/fs/cgroup/memory/memory.limit_in_bytes');
    }
  })();

  return {
    currentBytes: current,
    limitBytes: Number.isFinite(maxRaw) ? maxRaw : null,
    stat: readMemoryStat()
  };
};

const getSocketSummary = () => {
  let openWebSockets = 0;
  let closingWebSockets = 0;
  let totalWsBufferedAmount = 0;
  let maxWsBufferedAmount = 0;
  let openTargetSockets = 0;
  let totalTargetWritableLength = 0;
  let maxTargetWritableLength = 0;

  wss.clients.forEach(ws => {
    if (ws.readyState === WebSocket.OPEN) openWebSockets++;
    else closingWebSockets++;

    const bufferedAmount = ws.bufferedAmount || 0;
    totalWsBufferedAmount += bufferedAmount;
    if (bufferedAmount > maxWsBufferedAmount) maxWsBufferedAmount = bufferedAmount;

    if (ws.targetSocket && !ws.targetSocket.destroyed) {
      openTargetSockets++;
      const writableLength = ws.targetSocket.writableLength || 0;
      totalTargetWritableLength += writableLength;
      if (writableLength > maxTargetWritableLength) maxTargetWritableLength = writableLength;
    }
  });

  return {
    openWebSockets,
    closingWebSockets,
    trackedClients: wss.clients.size,
    totalWsBufferedAmount,
    maxWsBufferedAmount,
    openTargetSockets,
    totalTargetWritableLength,
    maxTargetWritableLength
  };
};

const getRuntimeStats = () => ({
  pid: process.pid,
  nodeVersion: process.version,
  platform: process.platform,
  arch: process.arch,
  startedAt: runtimeStats.startedAt,
  uptimeSeconds: Math.floor(process.uptime()),
  limits: {
    maxWsPayloadBytes: MAX_WS_PAYLOAD_BYTES,
    wsBackpressureLimitBytes: WS_BACKPRESSURE_LIMIT_BYTES,
    wsBackpressureResumeBytes: WS_BACKPRESSURE_RESUME_BYTES
  },
  memory: process.memoryUsage(),
  resourceUsage: process.resourceUsage ? process.resourceUsage() : null,
  containerMemory: getContainerMemory(),
  connections: {
    totalConnections: runtimeStats.totalConnections,
    activeConnections: runtimeStats.activeConnections,
    maxActiveConnections: runtimeStats.maxActiveConnections,
    rejectedUpgrades: runtimeStats.rejectedUpgrades,
    targetErrors: runtimeStats.targetErrors,
    websocketErrors: runtimeStats.websocketErrors,
    deadConnectionsTerminated: runtimeStats.deadConnectionsTerminated,
    quotaClosedConnections: runtimeStats.quotaClosedConnections
  },
  websocket: {
    backpressureEvents: runtimeStats.backpressureEvents,
    maxWsBufferedAmountSeen: runtimeStats.maxWsBufferedAmount,
    pausedTargetReads: runtimeStats.pausedTargetReads,
    resumedTargetReads: runtimeStats.resumedTargetReads,
    ...getSocketSummary()
  }
});

const getPendingTraffic = (userId, dayPrefix = null) => {
  let uploadBytes = 0;
  let downloadBytes = 0;
  let connections = 0;
  let failedConnections = 0;
  for (const [key, value] of trafficBuffer.entries()) {
    const [bufferUserId, day] = key.split(':');
    if (Number(bufferUserId) !== Number(userId)) continue;
    if (dayPrefix && !day.startsWith(dayPrefix)) continue;
    uploadBytes += value.uploadBytes || 0;
    downloadBytes += value.downloadBytes || 0;
    connections += value.connections || 0;
    failedConnections += value.failedConnections || 0;
  }
  return { uploadBytes, downloadBytes, connections, failedConnections };
};

const getUserUsage = userId => {
  const day = todayKey();
  const month = monthKey();
  const today = get(
    `SELECT COALESCE(SUM(upload_bytes), 0) AS uploadBytes,
            COALESCE(SUM(download_bytes), 0) AS downloadBytes,
            COALESCE(SUM(connections), 0) AS connections,
            COALESCE(SUM(failed_connections), 0) AS failedConnections
       FROM traffic_daily WHERE user_id = ? AND day = ?`,
    [userId, day]
  );
  const monthly = get(
    `SELECT COALESCE(SUM(upload_bytes), 0) AS uploadBytes,
            COALESCE(SUM(download_bytes), 0) AS downloadBytes,
            COALESCE(SUM(connections), 0) AS connections,
            COALESCE(SUM(failed_connections), 0) AS failedConnections
       FROM traffic_daily WHERE user_id = ? AND day LIKE ?`,
    [userId, `${month}%`]
  );
  const pendingToday = getPendingTraffic(userId, day);
  const pendingMonth = getPendingTraffic(userId, month);
  return {
    today: {
      uploadBytes: Number(today.uploadBytes || 0) + pendingToday.uploadBytes,
      downloadBytes: Number(today.downloadBytes || 0) + pendingToday.downloadBytes,
      connections: Number(today.connections || 0) + pendingToday.connections,
      failedConnections: Number(today.failedConnections || 0) + pendingToday.failedConnections
    },
    month: {
      uploadBytes: Number(monthly.uploadBytes || 0) + pendingMonth.uploadBytes,
      downloadBytes: Number(monthly.downloadBytes || 0) + pendingMonth.downloadBytes,
      connections: Number(monthly.connections || 0) + pendingMonth.connections,
      failedConnections: Number(monthly.failedConnections || 0) + pendingMonth.failedConnections
    }
  };
};

const ensureUsageCache = userId => {
  const day = todayKey();
  const month = monthKey();
  const cached = usageCache.get(userId);
  if (cached && cached.day === day && cached.month === month) return cached;
  const usage = getUserUsage(userId);
  const next = {
    day,
    month,
    todayBytes: usage.today.uploadBytes + usage.today.downloadBytes,
    monthBytes: usage.month.uploadBytes + usage.month.downloadBytes
  };
  usageCache.set(userId, next);
  return next;
};

const addTraffic = (userId, uploadBytes, downloadBytes, userLimits) => {
  const day = todayKey();
  const key = `${userId}:${day}`;
  const current = trafficBuffer.get(key) || {
    uploadBytes: 0,
    downloadBytes: 0,
    connections: 0,
    failedConnections: 0
  };
  current.uploadBytes += uploadBytes || 0;
  current.downloadBytes += downloadBytes || 0;
  trafficBuffer.set(key, current);

  const usage = ensureUsageCache(userId);
  usage.todayBytes += (uploadBytes || 0) + (downloadBytes || 0);
  usage.monthBytes += (uploadBytes || 0) + (downloadBytes || 0);

  const dailyLimit = Number(userLimits.daily_limit_bytes || 0);
  const monthlyLimit = Number(userLimits.monthly_limit_bytes || 0);
  return !((dailyLimit > 0 && usage.todayBytes > dailyLimit) || (monthlyLimit > 0 && usage.monthBytes > monthlyLimit));
};

const addConnectionStat = (userId, field) => {
  const day = todayKey();
  const key = `${userId}:${day}`;
  const current = trafficBuffer.get(key) || {
    uploadBytes: 0,
    downloadBytes: 0,
    connections: 0,
    failedConnections: 0
  };
  current[field] += 1;
  trafficBuffer.set(key, current);
};

const flushTraffic = () => {
  if (trafficBuffer.size === 0) return;
  db.run('BEGIN TRANSACTION');
  for (const [key, value] of trafficBuffer.entries()) {
    const [userId, day] = key.split(':');
    db.run(
      `INSERT INTO traffic_daily
        (user_id, day, upload_bytes, download_bytes, connections, failed_connections)
       VALUES (?, ?, ?, ?, ?, ?)
       ON CONFLICT(user_id, day) DO UPDATE SET
        upload_bytes = upload_bytes + excluded.upload_bytes,
        download_bytes = download_bytes + excluded.download_bytes,
        connections = connections + excluded.connections,
        failed_connections = failed_connections + excluded.failed_connections`,
      [Number(userId), day, value.uploadBytes, value.downloadBytes, value.connections, value.failedConnections]
    );
  }
  db.run('COMMIT');
  trafficBuffer.clear();
  scheduleDbSave();
};

const resetUserDailyTraffic = userId => {
  flushTraffic();
  run(
    `INSERT INTO traffic_daily (user_id, day, upload_bytes, download_bytes, connections, failed_connections)
     VALUES (?, ?, 0, 0, 0, 0)
     ON CONFLICT(user_id, day) DO UPDATE SET
      upload_bytes = 0,
      download_bytes = 0,
      connections = 0,
      failed_connections = 0`,
    [userId, todayKey()]
  );
  usageCache.delete(Number(userId));
};

const buildStatsResponse = () => {
  flushTraffic();
  const users = all(`
    SELECT id, username, nickname, secret, daily_limit_bytes, monthly_limit_bytes, active
      FROM users ORDER BY id DESC
  `).map(user => {
    const usage = getUserUsage(user.id);
    return {
      id: user.id,
      username: user.username,
      nickname: user.nickname,
      secretPreview: `${String(user.secret).slice(0, 8)}...`,
      active: Boolean(user.active),
      dailyLimitBytes: user.daily_limit_bytes,
      monthlyLimitBytes: user.monthly_limit_bytes,
      usage
    };
  });

  const daily = all(`
    SELECT day,
           COALESCE(SUM(upload_bytes), 0) AS uploadBytes,
           COALESCE(SUM(download_bytes), 0) AS downloadBytes,
           COALESCE(SUM(connections), 0) AS connections,
           COALESCE(SUM(failed_connections), 0) AS failedConnections
      FROM traffic_daily
     GROUP BY day
     ORDER BY day DESC
     LIMIT 90
  `);

  return {
    status: 'success',
    timestamp: nowIso(),
    runtime: getRuntimeStats(),
    users,
    daily
  };
};

const mib = bytes => Math.round((bytes || 0) / 1024 / 1024 * 10) / 10;

const logMonitorSnapshot = () => {
  const stats = buildStatsResponse();
  const memory = stats.runtime.memory;
  const containerMemory = stats.runtime.containerMemory;
  const websocket = stats.runtime.websocket;
  const connections = stats.runtime.connections;

  console.log(
    `[Easy-Net] [监控] rss=${mib(memory.rss)}MiB heapUsed=${mib(memory.heapUsed)}MiB ` +
    `external=${mib(memory.external)}MiB container=${mib(containerMemory.currentBytes)}MiB ` +
    `active=${connections.activeConnections} maxActive=${connections.maxActiveConnections} ` +
    `wsBuffered=${websocket.totalWsBufferedAmount} maxWsBuffered=${websocket.maxWsBufferedAmount} ` +
    `targetBuffered=${websocket.totalTargetWritableLength}`
  );
};

const parseCookies = req => {
  const cookies = {};
  String(req.headers.cookie || '').split(';').forEach(part => {
    const index = part.indexOf('=');
    if (index === -1) return;
    const key = part.slice(0, index).trim();
    const value = part.slice(index + 1).trim();
    if (key) cookies[key] = decodeURIComponent(value);
  });
  return cookies;
};

const createSession = payload => {
  const token = randomSecret();
  sessions.set(token, {
    ...payload,
    expiresAt: Date.now() + SESSION_TTL_HOURS * 60 * 60 * 1000
  });
  return token;
};

const getSession = req => {
  const token = parseCookies(req).EN_SESSION;
  if (!token) return null;
  const session = sessions.get(token);
  if (!session || session.expiresAt < Date.now()) {
    sessions.delete(token);
    return null;
  }
  session.expiresAt = Date.now() + SESSION_TTL_HOURS * 60 * 60 * 1000;
  return { token, ...session };
};

const clearSession = (req, res) => {
  const token = parseCookies(req).EN_SESSION;
  if (token) sessions.delete(token);
  res.setHeader('Set-Cookie', 'EN_SESSION=; Path=/; HttpOnly; SameSite=Lax; Max-Age=0');
};

const setSessionCookie = (res, token) => {
  res.setHeader(
    'Set-Cookie',
    `EN_SESSION=${encodeURIComponent(token)}; Path=/; HttpOnly; SameSite=Lax; Max-Age=${SESSION_TTL_HOURS * 60 * 60}`
  );
};

const sendJson = (res, statusCode, payload) => {
  res.writeHead(statusCode, {
    'Content-Type': 'application/json; charset=utf-8',
    'Cache-Control': 'no-store'
  });
  res.end(JSON.stringify(payload));
};

const sendNotFound = res => {
  res.writeHead(404, { 'Content-Type': 'text/html; charset=utf-8' });
  res.end('<!DOCTYPE html><html><head><title>404 Not Found</title></head><body><center><h1>404 Not Found</h1></center><hr><center>nginx</center></body></html>');
};

const parseBody = req => new Promise((resolve, reject) => {
  let body = '';
  req.on('data', chunk => {
    body += chunk;
    if (body.length > 128 * 1024) {
      reject(new Error('请求体过大'));
      req.destroy();
    }
  });
  req.on('end', () => {
    if (!body) return resolve({});
    try {
      resolve(JSON.parse(body));
    } catch (err) {
      reject(new Error('JSON 格式错误'));
    }
  });
  req.on('error', reject);
});

const requireRole = (req, res, role) => {
  const session = getSession(req);
  if (!session || session.role !== role) {
    sendJson(res, 401, { error: '未登录或登录已过期' });
    return null;
  }
  return session;
};

const isLoginLocked = (kind, username) => {
  const row = get('SELECT locked_until FROM login_attempts WHERE kind = ? AND username = ?', [kind, username]);
  if (!row) return 0;
  return Number(row.locked_until || 0) > Date.now() ? Number(row.locked_until) : 0;
};

const clearLoginFailures = (kind, username) => {
  run('DELETE FROM login_attempts WHERE kind = ? AND username = ?', [kind, username]);
};

const recordLoginFailure = (kind, username) => {
  const now = Date.now();
  const row = get('SELECT failed_count, updated_at FROM login_attempts WHERE kind = ? AND username = ?', [kind, username]);
  const fresh = row && (now - Number(row.updated_at || 0)) < LOGIN_FAILURE_WINDOW_MINUTES * 60 * 1000;
  const nextCount = fresh ? Number(row.failed_count || 0) + 1 : 1;
  const lockedUntil = nextCount >= LOGIN_MAX_FAILURES ? now + LOGIN_LOCK_MINUTES * 60 * 1000 : 0;
  run(
    `INSERT INTO login_attempts (kind, username, failed_count, locked_until, updated_at)
     VALUES (?, ?, ?, ?, ?)
     ON CONFLICT(kind, username) DO UPDATE SET
      failed_count = excluded.failed_count,
      locked_until = excluded.locked_until,
      updated_at = excluded.updated_at`,
    [kind, username, nextCount, lockedUntil, now]
  );
  return { failedCount: nextCount, lockedUntil };
};

const sanitizeUser = user => {
  const usage = getUserUsage(user.id);
  return {
    id: user.id,
    username: user.username,
    nickname: user.nickname || '',
    secret: user.secret,
    remark: user.remark || '',
    active: Boolean(user.active),
    dailyLimitBytes: Number(user.daily_limit_bytes || 0),
    monthlyLimitBytes: Number(user.monthly_limit_bytes || 0),
    dailyLimitGb: fromBytesToGb(user.daily_limit_bytes),
    monthlyLimitGb: fromBytesToGb(user.monthly_limit_bytes),
    createdAt: user.created_at,
    updatedAt: user.updated_at,
    usage
  };
};

const validateUsername = username => /^[a-zA-Z0-9_.-]{3,32}$/.test(username || '');
const validatePassword = password => typeof password === 'string' && password.length >= 8;
const validateSecret = secret => typeof secret === 'string' && secret.length >= 12 && secret.length <= 128;

const requestHost = req => String(req.headers['x-forwarded-host'] || req.headers.host || `127.0.0.1:${PORT}`);

const requestWsScheme = req => {
  const proto = String(req.headers['x-forwarded-proto'] || '').split(',')[0].trim().toLowerCase();
  if (proto === 'https') return 'wss';
  if (proto === 'http') return 'ws';
  return req.socket.encrypted ? 'wss' : 'ws';
};

const normalizeWsUrl = value => {
  const trimmed = String(value || '').trim();
  if (!trimmed) return '';
  if (/^wss?:\/\//i.test(trimmed)) return trimmed.replace(/\/+$/, '');
  if (/^https?:\/\//i.test(trimmed)) {
    const parsed = new URL(trimmed);
    parsed.protocol = parsed.protocol === 'https:' ? 'wss:' : 'ws:';
    return parsed.toString().replace(/\/+$/, '');
  }
  return `wss://${trimmed.replace(/^\/+|\/+$/g, '')}${CONTEXT_PATH}/tunnel`;
};

const inferClientWsUrl = req => {
  const configuredWsUrl = normalizeWsUrl(getSetting('client_ws_url'));
  if (configuredWsUrl) return configuredWsUrl;

  const configuredHost = getSetting('client_host');
  if (configuredHost) return normalizeWsUrl(configuredHost);

  return `${requestWsScheme(req)}://${requestHost(req)}${CONTEXT_PATH}/tunnel`;
};

const getHostFromWsUrl = wsUrl => {
  try {
    return new URL(wsUrl).host;
  } catch (err) {
    return String(wsUrl || '').replace(/^wss?:\/\//, '').split('/')[0];
  }
};

const buildClientConfig = (user, req) => {
  const serverWsUrl = inferClientWsUrl(req);
  return {
    serverWsUrl,
    workerHost: getHostFromWsUrl(serverWsUrl),
    localPort: Number(getSetting('client_local_port') || 1080),
    secret: user.secret
  };
};

const sendConfigDownload = (req, res, user) => {
  const config = buildClientConfig(user, req);
  const filename = `easy-net-${user.username}.json`;
  res.writeHead(200, {
    'Content-Type': 'application/json; charset=utf-8',
    'Content-Disposition': `attachment; filename="${filename}"`,
    'Cache-Control': 'no-store'
  });
  res.end(JSON.stringify(config, null, 2));
};

const publicHtml = `
<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Easy-Net</title>
  <link rel="stylesheet" href="${withContextPath('/assets/app.css')}">
</head>
<body>
  <main class="landing">
    <section>
      <p class="eyebrow">Easy-Net</p>
      <h1>服务正在运行</h1>
      <p>请进入管理端或用户端。</p>
      <div class="actions">
        <a class="button" href="${withContextPath('/admin')}">管理端</a>
        <a class="button secondary" href="${withContextPath('/user')}">用户端</a>
      </div>
    </section>
  </main>
</body>
</html>
`;

const serveStatic = (pathname, res) => {
  const staticRoot = path.join(__dirname, 'public');
  const cleanPath = path.normalize(pathname.replace(/^\/assets\//, ''));
  if (cleanPath.startsWith('..')) return sendNotFound(res);
  const filePath = path.join(staticRoot, cleanPath);
  if (!filePath.startsWith(staticRoot) || !fs.existsSync(filePath)) return sendNotFound(res);
  const ext = path.extname(filePath);
  const contentTypes = {
    '.css': 'text/css; charset=utf-8',
    '.js': 'application/javascript; charset=utf-8',
    '.html': 'text/html; charset=utf-8'
  };
  res.writeHead(200, {
    'Content-Type': contentTypes[ext] || 'application/octet-stream',
    'Cache-Control': 'no-store'
  });
  fs.createReadStream(filePath).pipe(res);
};

const handleAdminApi = async (req, res, pathname, method) => {
  if (pathname === '/api/admin/login' && method === 'POST') {
    const body = await parseBody(req);
    const key = 'admin';
    const lockedUntil = isLoginLocked('admin', key);
    if (lockedUntil) return sendJson(res, 429, { error: '错误次数过多，请稍后再试', lockedUntil });
    const admin = get('SELECT password_hash, password_salt FROM admin_credentials WHERE id = 1');
    if (!admin || !verifyPassword(body.password || '', admin.password_salt, admin.password_hash)) {
      const failure = recordLoginFailure('admin', key);
      return sendJson(res, 401, { error: '密码错误', ...failure });
    }
    clearLoginFailures('admin', key);
    const token = createSession({ role: 'admin', username: 'admin' });
    setSessionCookie(res, token);
    return sendJson(res, 200, { ok: true });
  }

  if (pathname === '/api/admin/logout' && method === 'POST') {
    clearSession(req, res);
    return sendJson(res, 200, { ok: true });
  }

  const session = requireRole(req, res, 'admin');
  if (!session) return;

  if (pathname === '/api/admin/me' && method === 'GET') {
    return sendJson(res, 200, { username: session.username });
  }

  if (pathname === '/api/admin/password' && method === 'POST') {
    const body = await parseBody(req);
    if (!validatePassword(body.newPassword)) return sendJson(res, 400, { error: '新密码至少 8 位' });
    const admin = get('SELECT password_hash, password_salt FROM admin_credentials WHERE id = 1');
    if (!verifyPassword(body.oldPassword || '', admin.password_salt, admin.password_hash)) {
      return sendJson(res, 401, { error: '原密码错误' });
    }
    const { salt, hash } = hashPassword(body.newPassword);
    run('UPDATE admin_credentials SET password_hash = ?, password_salt = ?, updated_at = ? WHERE id = 1', [hash, salt, nowIso()]);
    return sendJson(res, 200, { ok: true });
  }

  if (pathname === '/api/admin/users' && method === 'GET') {
    flushTraffic();
    const users = all('SELECT * FROM users ORDER BY id DESC').map(sanitizeUser);
    return sendJson(res, 200, { users });
  }

  if (pathname === '/api/admin/users' && method === 'POST') {
    const body = await parseBody(req);
    const username = String(body.username || '').trim();
    const password = String(body.password || '');
    const secret = String(body.secret || randomSecret()).trim();
    if (!validateUsername(username)) return sendJson(res, 400, { error: '用户名需为 3-32 位字母、数字、点、下划线或短横线' });
    if (!validatePassword(password)) return sendJson(res, 400, { error: '密码至少 8 位' });
    if (!validateSecret(secret)) return sendJson(res, 400, { error: '连接密钥长度需为 12-128 位' });
    const { salt, hash } = hashPassword(password);
    try {
      run(
        `INSERT INTO users
          (username, password_hash, password_salt, nickname, secret, remark, daily_limit_bytes, monthly_limit_bytes, active, created_at, updated_at)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
        [
          username,
          hash,
          salt,
          String(body.nickname || '').trim(),
          secret,
          String(body.remark || '').trim(),
          toBytesFromGb(body.dailyLimitGb),
          toBytesFromGb(body.monthlyLimitGb),
          body.active === false ? 0 : 1,
          nowIso(),
          nowIso()
        ]
      );
      return sendJson(res, 201, { ok: true });
    } catch (err) {
      return sendJson(res, 409, { error: '用户名或连接密钥已存在' });
    }
  }

  const userMatch = pathname.match(/^\/api\/admin\/users\/(\d+)(?:\/(reset-daily|config))?$/);
  if (userMatch) {
    const userId = Number(userMatch[1]);
    const action = userMatch[2] || '';
    const user = get('SELECT * FROM users WHERE id = ?', [userId]);
    if (!user) return sendJson(res, 404, { error: '用户不存在' });

    if (method === 'GET' && action === 'config') return sendConfigDownload(req, res, user);

    if (method === 'POST' && action === 'reset-daily') {
      resetUserDailyTraffic(userId);
      return sendJson(res, 200, { ok: true });
    }

    if (method === 'PUT' && !action) {
      const body = await parseBody(req);
      const has = field => Object.prototype.hasOwnProperty.call(body, field);
      const updates = {
        nickname: has('nickname') ? String(body.nickname || '').trim() : user.nickname,
        remark: has('remark') ? String(body.remark || '').trim() : user.remark,
        dailyLimitBytes: has('dailyLimitGb') ? toBytesFromGb(body.dailyLimitGb) : Number(user.daily_limit_bytes || 0),
        monthlyLimitBytes: has('monthlyLimitGb') ? toBytesFromGb(body.monthlyLimitGb) : Number(user.monthly_limit_bytes || 0),
        active: has('active') ? (body.active === false ? 0 : 1) : Number(user.active || 0),
        secret: body.resetSecret ? randomSecret() : (has('secret') ? String(body.secret || '').trim() : user.secret)
      };
      if (!updates.secret) updates.secret = user.secret;
      if (!validateSecret(updates.secret)) return sendJson(res, 400, { error: '连接密钥长度需为 12-128 位' });
      let passwordSql = '';
      const params = [updates.nickname, updates.remark, updates.secret, updates.dailyLimitBytes, updates.monthlyLimitBytes, updates.active, nowIso()];
      if (body.password) {
        if (!validatePassword(body.password)) return sendJson(res, 400, { error: '新密码至少 8 位' });
        const { salt, hash } = hashPassword(body.password);
        passwordSql = ', password_hash = ?, password_salt = ?';
        params.push(hash, salt);
      }
      params.push(userId);
      try {
        run(
          `UPDATE users SET
             nickname = ?,
             remark = ?,
             secret = ?,
             daily_limit_bytes = ?,
             monthly_limit_bytes = ?,
             active = ?,
             updated_at = ?
             ${passwordSql}
           WHERE id = ?`,
          params
        );
        usageCache.delete(userId);
        return sendJson(res, 200, { ok: true });
      } catch (err) {
        return sendJson(res, 409, { error: '连接密钥已存在' });
      }
    }

    if (method === 'DELETE' && !action) {
      run('DELETE FROM users WHERE id = ?', [userId]);
      run('DELETE FROM traffic_daily WHERE user_id = ?', [userId]);
      usageCache.delete(userId);
      return sendJson(res, 200, { ok: true });
    }
  }

  if (pathname === '/api/admin/stats' && method === 'GET') {
    return sendJson(res, 200, buildStatsResponse());
  }

  if (pathname === '/api/admin/settings' && method === 'GET') {
    return sendJson(res, 200, {
      clientWsUrl: getSetting('client_ws_url'),
      clientHost: getSetting('client_host'),
      clientLocalPort: Number(getSetting('client_local_port') || 1080),
      contextPath: CONTEXT_PATH
    });
  }

  if (pathname === '/api/admin/settings' && method === 'PUT') {
    const body = await parseBody(req);
    setSetting('client_ws_url', normalizeWsUrl(body.clientWsUrl || ''));
    setSetting('client_host', String(body.clientHost || '').trim());
    setSetting('client_local_port', String(safeInt(body.clientLocalPort || 1080) || 1080));
    return sendJson(res, 200, { ok: true });
  }

  sendJson(res, 404, { error: '接口不存在' });
};

const handleUserApi = async (req, res, pathname, method) => {
  if (pathname === '/api/user/login' && method === 'POST') {
    const body = await parseBody(req);
    const username = String(body.username || '').trim();
    const key = username.toLowerCase();
    const lockedUntil = isLoginLocked('user', key);
    if (lockedUntil) return sendJson(res, 429, { error: '错误次数过多，请稍后再试', lockedUntil });
    const user = get('SELECT * FROM users WHERE username = ? AND active = 1', [username]);
    if (!user || !verifyPassword(body.password || '', user.password_salt, user.password_hash)) {
      const failure = recordLoginFailure('user', key || 'unknown');
      return sendJson(res, 401, { error: '用户名或密码错误', ...failure });
    }
    clearLoginFailures('user', key);
    const token = createSession({ role: 'user', userId: user.id, username: user.username });
    setSessionCookie(res, token);
    return sendJson(res, 200, { ok: true });
  }

  if (pathname === '/api/user/logout' && method === 'POST') {
    clearSession(req, res);
    return sendJson(res, 200, { ok: true });
  }

  const session = requireRole(req, res, 'user');
  if (!session) return;
  const user = get('SELECT * FROM users WHERE id = ? AND active = 1', [session.userId]);
  if (!user) {
    clearSession(req, res);
    return sendJson(res, 401, { error: '用户已停用' });
  }

  if (pathname === '/api/user/me' && method === 'GET') {
    const sanitized = sanitizeUser(user);
    delete sanitized.remark;
    return sendJson(res, 200, { user: sanitized, config: buildClientConfig(user, req) });
  }

  if (pathname === '/api/user/password' && method === 'POST') {
    const body = await parseBody(req);
    if (!verifyPassword(body.oldPassword || '', user.password_salt, user.password_hash)) {
      return sendJson(res, 401, { error: '原密码错误' });
    }
    if (!validatePassword(body.newPassword)) return sendJson(res, 400, { error: '新密码至少 8 位' });
    const { salt, hash } = hashPassword(body.newPassword);
    run('UPDATE users SET password_hash = ?, password_salt = ?, updated_at = ? WHERE id = ?', [hash, salt, nowIso(), user.id]);
    return sendJson(res, 200, { ok: true });
  }

  if (pathname === '/api/user/secret' && method === 'POST') {
    const body = await parseBody(req);
    const secret = String(body.secret || randomSecret()).trim();
    if (!validateSecret(secret)) return sendJson(res, 400, { error: '连接密钥长度需为 12-128 位' });
    try {
      run('UPDATE users SET secret = ?, updated_at = ? WHERE id = ?', [secret, nowIso(), user.id]);
      return sendJson(res, 200, { ok: true, secret });
    } catch (err) {
      return sendJson(res, 409, { error: '连接密钥已存在' });
    }
  }

  if (pathname === '/api/user/config' && method === 'GET') {
    return sendConfigDownload(req, res, user);
  }

  sendJson(res, 404, { error: '接口不存在' });
};

const server = http.createServer(async (req, res) => {
  const parsedUrl = url.parse(req.url, true);
  const originalPathname = parsedUrl.pathname;
  let pathname = stripContextPath(originalPathname);
  const method = req.method;

  try {
    if (!pathname) {
      if (originalPathname === '/') {
        res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store' });
        return res.end(publicHtml);
      }
      return sendNotFound(res);
    }
    if (pathname === '/') {
      res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store' });
      return res.end(publicHtml);
    }
    if (pathname === '/admin/') {
      res.writeHead(302, { Location: withContextPath('/admin') });
      return res.end();
    }
    if (pathname === '/user/') {
      res.writeHead(302, { Location: withContextPath('/user') });
      return res.end();
    }
    if (pathname === '/admin') return serveStatic('/assets/admin.html', res);
    if (pathname === '/user') return serveStatic('/assets/user.html', res);
    if (pathname.startsWith('/assets/')) return serveStatic(pathname, res);

    if (pathname === '/stats') {
      const { admin_key } = parsedUrl.query;
      if (ADMIN_KEY && admin_key === ADMIN_KEY) {
        return sendJson(res, 200, buildStatsResponse());
      }
      return sendNotFound(res);
    }

    if (pathname.startsWith('/api/admin/')) return handleAdminApi(req, res, pathname, method);
    if (pathname.startsWith('/api/user/')) return handleUserApi(req, res, pathname, method);

    return sendNotFound(res);
  } catch (err) {
    if (!res.headersSent) sendJson(res, 500, { error: err.message || '服务器错误' });
  }
});

const wss = new WebSocket.Server({
  noServer: true,
  maxPayload: MAX_WS_PAYLOAD_BYTES,
  perMessageDeflate: false
});

server.on('upgrade', (request, socket, head) => {
  const parsedUrl = url.parse(request.url, true);
  const pathname = stripContextPath(parsedUrl.pathname);
  const { secret, host, port } = parsedUrl.query;
  const user = secret ? get('SELECT * FROM users WHERE secret = ? AND active = 1', [secret]) : null;

  if (pathname === '/tunnel' && user && host && port) {
    const usage = getUserUsage(user.id);
    const totalToday = usage.today.uploadBytes + usage.today.downloadBytes;
    const totalMonth = usage.month.uploadBytes + usage.month.downloadBytes;
    if ((user.daily_limit_bytes > 0 && totalToday >= user.daily_limit_bytes) ||
        (user.monthly_limit_bytes > 0 && totalMonth >= user.monthly_limit_bytes)) {
      runtimeStats.rejectedUpgrades++;
      socket.write('HTTP/1.1 429 Too Many Requests\r\nConnection: close\r\n\r\n');
      socket.destroy();
      return;
    }

    wss.handleUpgrade(request, socket, head, ws => {
      ws.clientUser = user;
      wss.emit('connection', ws, request);
    });
  } else {
    runtimeStats.rejectedUpgrades++;
    socket.write(
      'HTTP/1.1 404 Not Found\r\n' +
      'Content-Type: text/html; charset=utf-8\r\n' +
      'Content-Length: 139\r\n' +
      'Connection: close\r\n\r\n' +
      '<!DOCTYPE html><html><head><title>404 Not Found</title></head><body><center><h1>404 Not Found</h1></center><hr><center>nginx</center></body></html>'
    );
    socket.destroy();
  }
});

wss.on('connection', (ws, req) => {
  const user = ws.clientUser;
  const parsedUrl = url.parse(req.url, true);
  const { host, port } = parsedUrl.query;

  try {
    console.log(`[Easy-Net] [连接] 用户 [${user.username}] 请求网络连接 -> ${host}:${port}`);
    let targetPausedForBackpressure = false;

    const updateWsBackpressureStats = () => {
      const bufferedAmount = ws.bufferedAmount || 0;
      if (bufferedAmount > 0) runtimeStats.backpressureEvents++;
      if (bufferedAmount > runtimeStats.maxWsBufferedAmount) runtimeStats.maxWsBufferedAmount = bufferedAmount;
      return bufferedAmount;
    };

    const pauseTargetForBackpressure = targetSocket => {
      if (targetPausedForBackpressure || targetSocket.destroyed) return;
      targetSocket.pause();
      targetPausedForBackpressure = true;
      runtimeStats.pausedTargetReads++;
    };

    const resumeTargetAfterBackpressure = targetSocket => {
      if (!targetPausedForBackpressure || targetSocket.destroyed) return;
      if ((ws.bufferedAmount || 0) > WS_BACKPRESSURE_RESUME_BYTES) return;
      targetSocket.resume();
      targetPausedForBackpressure = false;
      runtimeStats.resumedTargetReads++;
    };

    runtimeStats.totalConnections++;
    runtimeStats.activeConnections++;
    if (runtimeStats.activeConnections > runtimeStats.maxActiveConnections) {
      runtimeStats.maxActiveConnections = runtimeStats.activeConnections;
    }
    addConnectionStat(user.id, 'connections');

    const closeForQuota = () => {
      runtimeStats.quotaClosedConnections++;
      ws.close(1008, 'Traffic quota exceeded');
      cleanupConnection();
    };

    const targetSocket = net.connect(port, host, () => {
      console.log(`[Easy-Net] [连接] 成功与目标建立连接 -> ${host}:${port}`);
      targetSocket.setKeepAlive(true, 30000);

      ws.on('message', data => {
        if (targetSocket.writable) {
          targetSocket.write(data);
          if (!addTraffic(user.id, data.length, 0, user)) closeForQuota();
        }
      });

      targetSocket.on('data', data => {
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(data, err => {
            if (err) {
              runtimeStats.websocketErrors++;
              console.error(`[Easy-Net] [错误] WebSocket 发送失败 -> ${host}:${port}: ${err.message}`);
              cleanupConnection();
              return;
            }
            resumeTargetAfterBackpressure(targetSocket);
          });

          const bufferedAmount = updateWsBackpressureStats();
          if (bufferedAmount >= WS_BACKPRESSURE_LIMIT_BYTES) pauseTargetForBackpressure(targetSocket);
          if (!addTraffic(user.id, 0, data.length, user)) closeForQuota();
        }
      });
    });
    ws.targetSocket = targetSocket;

    ws.isAlive = true;
    ws.on('pong', () => {
      ws.isAlive = true;
    });

    let hasCleaned = false;
    function cleanupConnection() {
      if (hasCleaned) return;
      hasCleaned = true;
      if (runtimeStats.activeConnections > 0) runtimeStats.activeConnections--;
      targetSocket.destroy();
    }

    targetSocket.on('error', err => {
      runtimeStats.targetErrors++;
      addConnectionStat(user.id, 'failedConnections');
      console.error(`[Easy-Net] [错误] 无法连接到目标 ${host}:${port}: ${err.message}`);
      ws.close();
      cleanupConnection();
    });

    targetSocket.on('close', () => {
      console.log(`[Easy-Net] [关闭] 目标主机已断开连接 -> ${host}:${port}`);
      ws.close();
      cleanupConnection();
    });

    ws.on('close', () => {
      console.log(`[Easy-Net] [关闭] 客户端已断开连接 -> ${host}:${port}`);
      cleanupConnection();
    });

    ws.on('error', err => {
      runtimeStats.websocketErrors++;
      console.error(`[Easy-Net] [错误] 连接发生错误 -> ${host}:${port}: ${err.message}`);
      cleanupConnection();
    });
  } catch (err) {
    console.error(`[Easy-Net] [严重错误] 处理连接时崩溃: ${err.message}`);
    ws.close();
  }
});

const heartbeatInterval = setInterval(() => {
  wss.clients.forEach(ws => {
    if (ws.isAlive === false) {
      runtimeStats.deadConnectionsTerminated++;
      return ws.terminate();
    }
    ws.isAlive = false;
    ws.ping();
  });
}, 30000);

const sessionCleanupInterval = setInterval(() => {
  const now = Date.now();
  sessions.forEach((session, token) => {
    if (session.expiresAt < now) sessions.delete(token);
  });
}, 60000);
sessionCleanupInterval.unref();

const trafficFlushInterval = setInterval(flushTraffic, TRAFFIC_FLUSH_SECONDS * 1000);
trafficFlushInterval.unref();

let monitorInterval = null;
if (MONITOR_INTERVAL_SECONDS > 0) {
  monitorInterval = setInterval(logMonitorSnapshot, MONITOR_INTERVAL_SECONDS * 1000);
  monitorInterval.unref();
}

const shutdown = () => {
  clearInterval(heartbeatInterval);
  clearInterval(sessionCleanupInterval);
  clearInterval(trafficFlushInterval);
  if (monitorInterval) clearInterval(monitorInterval);
  flushTraffic();
  saveDbNow();
  process.exit(0);
};

process.on('SIGINT', shutdown);
process.on('SIGTERM', shutdown);

initDatabase()
  .then(() => {
    server.listen(PORT, () => {
      const userCount = get('SELECT COUNT(*) AS count FROM users').count;
      console.log('=================================================');
      console.log('[Easy-Net] 服务端已成功启动！');
      console.log(`监听端口: ${PORT}`);
      console.log(`本地数据库: ${DB_FILE}`);
      console.log(`Context Path: ${CONTEXT_PATH || '/'}`);
      console.log(`用户数量: ${userCount}`);
      console.log(`管理端: ${withContextPath('/admin')}`);
      console.log(`用户端: ${withContextPath('/user')}`);
      if (MONITOR_INTERVAL_SECONDS > 0) {
        console.log(`[监控] 已启用定时监控日志，间隔: ${MONITOR_INTERVAL_SECONDS} 秒`);
      }
      console.log('=================================================');
    });
  })
  .catch(err => {
    console.error(`[Easy-Net] 启动失败: ${err.stack || err.message}`);
    process.exit(1);
  });
