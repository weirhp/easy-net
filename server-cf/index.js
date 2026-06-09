/**
 * OmniGate - 专属云端网络直连服务 (Cloudflare Workers)
 * 
 * 核心升级与优化：
 * 1. 采用官方推荐的 WebSocket (WebSocketPair) 模式做 TCP Socket 桥接。
 * 2. 全面加固安全性：自动过滤转发给目标站点的敏感鉴权 Cookie 和 Header。
 * 3. 兼容性修复：解决 Cloudflare 压缩双斜杠为单斜杠的 Bug。
 * 4. 原生资源解析：注入 <base> 标签以实现免 Referer 的相对路径资源加载。
 * 5. 性能提升：引入内存级 GraphQL 用量数据与 Expected Token 缓存。
 */

import { connect } from 'cloudflare:sockets';
import config from './config.json';

// 全局缓存变量，减少不必要的 CPU 消耗与 GraphQL API 压力
let cachedExpectedToken = null;
const usageCache = {
  data: null,
  timestamp: 0
};

/**
 * 辅助函数：根据密钥和账号密码生成安全的 Session Token (SHA-256)
 */
async function getSessionToken(username, password, secret) {
  const data = new TextEncoder().encode(`${username}:${password}:${secret}`);
  const hashBuffer = await crypto.subtle.digest('SHA-256', data);
  const hashArray = Array.from(new Uint8Array(hashBuffer));
  return hashArray.map(b => b.toString(16).padStart(2, '0')).join('');
}

/**
 * 获取期望的 Session Token（优先读取缓存）
 */
async function getExpectedToken() {
  if (!cachedExpectedToken) {
    cachedExpectedToken = await getSessionToken(config.adminUser, config.adminPass, config.secret);
  }
  return cachedExpectedToken;
}

/**
 * 辅助函数：获取 Request 中的指定 Cookie 值（优化后的正则匹配版）
 */
function getCookie(request, name) {
  const cookieString = request.headers.get('Cookie');
  if (!cookieString) return null;
  const regex = new RegExp(`(?:^|; )${name.replace(/[-\/\\^$*+?.()|[\]{}]/g, '\\$&')}=([^;]*)`);
  const match = cookieString.match(regex);
  return match ? decodeURIComponent(match[1]) : null;
}

export default {
  async fetch(request, env, ctx) {
    const url = new URL(request.url);
    const workerHost = url.host;
    const workerProto = url.protocol;

    // --------------------------------------------------
    // A. 处理网络直连传输通道连接 (/tunnel)
    // --------------------------------------------------
    if (url.pathname === '/tunnel') {
      // 1. 验证是否为 WebSocket 升级请求
      if (request.headers.get('Upgrade') !== 'websocket') {
        return new Response('Expected WebSocket', { status: 400 });
      }

      // 优先从 URL 参数（防止 CDN 过滤 Header）读取鉴权和目标地址，其次从 Header 读取
      const secret = url.searchParams.get('secret') || request.headers.get('x-secret');
      if (secret !== config.secret) {
        return new Response('Unauthorized', { status: 401 });
      }

      const targetHost = url.searchParams.get('host') || request.headers.get('x-target-host');
      const targetPort = parseInt(url.searchParams.get('port') || request.headers.get('x-target-port'));

      if (!targetHost || !targetPort) {
        return new Response('Missing target parameters', { status: 400 });
      }

      // 2. 创建一对 WebSocket 实例（一个用于返回客户端，一个用于在云端处理）
      const pair = new WebSocketPair();
      const clientWS = pair[0];
      const serverWS = pair[1];
      
      // 明确指定二进制帧的传输类型为 'arraybuffer'，适配 2026 新版 Cloudflare Workers 规范（默认是 Blob）
      serverWS.binaryType = 'arraybuffer';
      serverWS.accept();

      try {
        // 3. 直连目标网站 (例如 google.com:443)
        const socket = connect({ hostname: targetHost, port: targetPort });

        // 4. 双向管道桥接 (WebSocket <=> TCP Socket)
        // 获取 TCP Socket 的写入锁（整个连接周期内复用，防止并发获取锁导致 stream is already locked 异常）
        const writer = socket.writable.getWriter();
        let isClosed = false;

        const safeClose = () => {
          if (isClosed) return;
          isClosed = true;
          try { writer.releaseLock(); } catch (e) {}
          try { socket.close(); } catch (e) {}
          try { serverWS.close(); } catch (e) {}
        };
        
        // 当收到本地客户端发来的 WebSocket 数据包时，将其二进制流安全写入 TCP Socket 中
        serverWS.addEventListener('message', async (event) => {
          try {
            // 将 ArrayBuffer 包装为 Uint8Array 以适配 TCP socket 写入格式
            const data = typeof event.data === 'string' 
              ? new TextEncoder().encode(event.data) 
              : new Uint8Array(event.data);
            await writer.write(data);
          } catch (e) {
            console.error('Outbound TCP socket write failed:', e);
            safeClose();
          }
        });

        // 监听来自目标主机的 TCP 回传数据，转换为二进制发送回本地客户端 WebSocket
        const pipeToWS = async () => {
          const reader = socket.readable.getReader();
          try {
            while (true) {
              const { value, done } = await reader.read();
              if (done) break;
              serverWS.send(value);
            }
          } catch (err) {
            console.error('TCP read error:', err);
          } finally {
            try { reader.releaseLock(); } catch (e) {}
            safeClose();
          }
        };

        // 在后台异步搬运 TCP 数据到 WebSocket (无需使用 ctx.waitUntil，防止事件超时挂起)
        pipeToWS();

        // 监听连接关闭事件
        serverWS.addEventListener('close', () => {
          safeClose();
        });

        serverWS.addEventListener('error', () => {
          safeClose();
        });

        // 将客户端一侧 of WS 实例放入 101 Switching Protocols 响应中返回
        return new Response(null, {
          status: 101,
          webSocket: clientWS
        });

      } catch (err) {
        serverWS.close();
        return new Response(`TCP connect to ${targetHost}:${targetPort} failed: ${err.message}`, { status: 502 });
      }
    }

    // 计算期望的 Session Token（优先使用全局缓存）
    const expectedToken = await getExpectedToken();
    const userToken = getCookie(request, 'superblog_session');
    const isAuthenticated = userToken === expectedToken;

    // 1. 处理登录 POST 接口
    if (url.pathname === '/login' && request.method === 'POST') {
      try {
        const formData = await request.formData();
        const username = formData.get('username');
        const password = formData.get('password');
        
        if (username === config.adminUser && password === config.adminPass) {
          return new Response(JSON.stringify({ success: true }), {
            headers: {
              'Content-Type': 'application/json; charset=utf-8',
              'Set-Cookie': `superblog_session=${expectedToken}; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=2592000` // 30天有效
            }
          });
        } else {
          return new Response(JSON.stringify({ success: false, message: '用户名或密码错误。' }), {
            status: 400,
            headers: { 'Content-Type': 'application/json; charset=utf-8' }
          });
        }
      } catch (e) {
        return new Response(JSON.stringify({ success: false, message: '请求格式不正确。' }), {
          status: 400,
          headers: { 'Content-Type': 'application/json; charset=utf-8' }
        });
      }
    }

    // 2. 处理退出登录
    if (url.pathname === '/logout') {
      return new Response(null, {
        status: 302,
        headers: {
          'Location': '/',
          'Set-Cookie': `superblog_session=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=0`
        }
      });
    }

    // 3. 未登录拦截：如果是未登录用户，直接展示登录界面
    if (!isAuthenticated) {
      return new Response(renderLoginHTML(workerHost), {
        headers: { 'Content-Type': 'text/html; charset=utf-8' }
      });
    }

    // 4. 已登录用户访问 /login 自动重定向至主页
    if (url.pathname === '/login') {
      return new Response(null, {
        status: 302,
        headers: { 'Location': '/' }
      });
    }

    // 5. 处理已登录用户的 /api/usage 路由
    if (url.pathname === '/api/usage') {
      const nowTime = Date.now();
      // 内存缓存 60 秒，减少对 Cloudflare API 的调用，加速控制台响应
      if (usageCache.data && (nowTime - usageCache.timestamp < 60000)) {
        return new Response(JSON.stringify(usageCache.data), {
          headers: { 'Content-Type': 'application/json; charset=utf-8' }
        });
      }

      const accountId = env.CLOUDFLARE_ACCOUNT_ID || config.cloudflareAccountId;
      const apiToken = env.CLOUDFLARE_API_TOKEN || config.cloudflareApiToken;

      if (!accountId || !apiToken) {
        return new Response(JSON.stringify({
          success: false,
          message: '未配置 Cloudflare API 凭证 (Account ID 或 Token)。请在 config.json 或 Worker 环境变量中进行配置。',
          todayRequests: 0,
          limit: 100000,
          remaining: 100000,
          configured: false
        }), {
          headers: { 'Content-Type': 'application/json; charset=utf-8' }
        });
      }

      try {
        const now = new Date();
        const todayUTCString = now.toISOString().split('T')[0];
        const start = `${todayUTCString}T00:00:00Z`;
        const end = `${todayUTCString}T23:59:59Z`;

        const query = `
          query GetWorkersAnalytics($accountId: String!, $scriptName: String!, $start: String!, $end: String!) {
            viewer {
              accounts(filter: { accountTag: $accountId }) {
                workersInvocationsAdaptive(
                  limit: 1,
                  filter: {
                    scriptName: $scriptName,
                    datetime_geq: $start,
                    datetime_leq: $end
                  }
                ) {
                  sum {
                    requests
                  }
                }
              }
            }
          }
        `;

        const response = await fetch('https://api.cloudflare.com/client/v4/graphql', {
          method: 'POST',
          headers: {
            'Authorization': `Bearer ${apiToken}`,
            'Content-Type': 'application/json'
          },
          body: JSON.stringify({
            query: query,
            variables: {
              accountId: accountId,
              scriptName: "omnigate-proxy",
              start: start,
              end: end
            }
          })
        });

        const data = await response.json();
        if (data.errors && data.errors.length > 0) {
          throw new Error(data.errors[0].message);
        }

        const sumData = data.data?.viewer?.accounts?.[0]?.workersInvocationsAdaptive?.[0]?.sum;
        const todayRequests = sumData ? sumData.requests : 0;
        const limit = 100000;
        const remaining = Math.max(0, limit - todayRequests);

        const responseData = {
          success: true,
          todayRequests: todayRequests,
          limit: limit,
          remaining: remaining,
          configured: true
        };

        // 更新全局缓存
        usageCache.data = responseData;
        usageCache.timestamp = nowTime;

        return new Response(JSON.stringify(responseData), {
          headers: { 'Content-Type': 'application/json; charset=utf-8' }
        });
      } catch (err) {
        return new Response(JSON.stringify({
          success: false,
          message: `无法获取用量数据: ${err.message}`,
          todayRequests: 0,
          limit: 100000,
          remaining: 100000,
          configured: true
        }), {
          headers: { 'Content-Type': 'application/json; charset=utf-8' }
        });
      }
    }

    // --------------------------------------------------
    // B. 原有网页直连门户与重写逻辑
    // --------------------------------------------------
    if (url.pathname === '/' || url.pathname === '') {
      const cfInfo = {
        ip: request.headers.get('CF-Connecting-IP') || request.headers.get('X-Real-IP') || '未知',
        city: request.cf?.city || '未知',
        colo: request.cf?.colo || '未知',
        country: request.cf?.country || '未知',
        asn: request.cf?.asn || '未知'
      };
      return new Response(renderPortalHTML(workerHost, cfInfo), {
        headers: { 'Content-Type': 'text/html; charset=utf-8' }
      });
    }

    // 解析常规直连的 Target URL
    let targetUrlStr = '';
    let pathPart = url.pathname.slice(1);

    // 兼容 Cloudflare 压缩双斜杠为单斜杠的 Bug (将 https:/ 补全为 https://)
    if (pathPart.startsWith('http:/') && !pathPart.startsWith('http://')) {
      pathPart = 'http://' + pathPart.slice(6);
    } else if (pathPart.startsWith('https:/') && !pathPart.startsWith('https://')) {
      pathPart = 'https://' + pathPart.slice(7);
    }

    if (pathPart.startsWith('http://') || pathPart.startsWith('https://')) {
      targetUrlStr = pathPart + url.search;
    } else {
      const referer = request.headers.get('Referer');
      if (referer) {
        try {
          const refererUrl = new URL(referer);
          if (refererUrl.host === workerHost) {
            let refPath = refererUrl.pathname.slice(1);
            // 同样兼容 Referer 里的压缩双斜杠
            if (refPath.startsWith('http:/') && !refPath.startsWith('http://')) {
              refPath = 'http://' + refPath.slice(6);
            } else if (refPath.startsWith('https:/') && !refPath.startsWith('https://')) {
              refPath = 'https://' + refPath.slice(7);
            }
            if (refPath.startsWith('http://') || refPath.startsWith('https://')) {
              const actualTargetOrigin = new URL(refPath).origin;
              targetUrlStr = actualTargetOrigin + url.pathname + url.search;
            }
          }
        } catch (e) {}
      }
    }

    if (!targetUrlStr) {
      return new Response(renderErrorHTML('无法识别的请求', '该请求未携带完整的目标网址，且无法通过 Referer 进行回溯。请返回主页重新输入。', workerHost), {
        status: 404,
        headers: { 'Content-Type': 'text/html; charset=utf-8' }
      });
    }

    let targetUrl;
    try {
      targetUrl = new URL(targetUrlStr);
    } catch (e) {
      return new Response(renderErrorHTML('无效的网址格式', `解析的目标网址不合法: ${targetUrlStr}`, workerHost), {
        status: 400,
        headers: { 'Content-Type': 'text/html; charset=utf-8' }
      });
    }

    const newHeaders = new Headers(request.headers);
    newHeaders.set('Host', targetUrl.host);
    newHeaders.set('Referer', targetUrl.origin);

    // 过滤掉发送给第三方目标网站的 Worker 鉴权 Cookie (保障安全)
    const rawCookie = newHeaders.get('Cookie');
    if (rawCookie) {
      const cleanedCookie = rawCookie.split(';')
        .map(c => c.trim())
        .filter(c => !c.startsWith('superblog_session='))
        .join('; ');
      if (cleanedCookie) {
        newHeaders.set('Cookie', cleanedCookie);
      } else {
        newHeaders.delete('Cookie');
      }
    }

    // 移除敏感的 x-secret Header，避免泄漏给外部服务器
    newHeaders.delete('x-secret');

    // 显式删除 Accept-Encoding，迫使目标网站返回未压缩数据，或允许 Cloudflare 稳定解压，避免 text() 乱码
    newHeaders.delete('Accept-Encoding');

    if (targetUrl.hostname.includes('google.com')) {
      const existingCookie = newHeaders.get('Cookie') || '';
      if (!existingCookie.includes('NCR=')) {
        newHeaders.set('Cookie', existingCookie + '; PREF=ID=0:FF=0:LD=zh-CN:NW=1:TM=0:LM=0:GM=0:SG=1:CR=2:NCR=1');
      }
    }

    let response;
    try {
      response = await fetch(targetUrl.toString(), {
        method: request.method,
        headers: newHeaders,
        body: request.method !== 'GET' && request.method !== 'HEAD' ? request.body : undefined,
        redirect: 'manual'
      });
    } catch (err) {
      return new Response(renderErrorHTML('无法连接目标网站', `在访问目标网址时发生错误: ${err.message}<br><br>可能该网站本身暂时不可达或网络中断。`, workerHost), {
        status: 502,
        headers: { 'Content-Type': 'text/html; charset=utf-8' }
      });
    }

    const modifiedHeaders = new Headers(response.headers);
    modifiedHeaders.set('Access-Control-Allow-Origin', '*');

    if ([301, 302, 303, 307, 308].includes(response.status)) {
      let location = modifiedHeaders.get('Location');
      if (location) {
        try {
          let locationUrl;
          if (location.startsWith('http://') || location.startsWith('https://')) {
            locationUrl = new URL(location);
          } else {
            locationUrl = new URL(location, targetUrl.origin);
          }
          const rewrittenLocation = `${workerProto}//${workerHost}/${locationUrl.toString()}`;
          modifiedHeaders.set('Location', rewrittenLocation);
        } catch (e) {}
      }
    }

    const contentType = modifiedHeaders.get('content-type') || '';
    if (
      contentType.includes('text/html') || 
      contentType.includes('text/javascript') || 
      contentType.includes('application/javascript') ||
      contentType.includes('text/css')
    ) {
      let text = await response.text();

      // 如果是 HTML 页面，注入 <base> 标签以实现免 Referer 相对路径资源加载
      if (contentType.includes('text/html')) {
        const baseHref = `${workerProto}//${workerHost}/${targetUrl.origin}/`;
        const baseTag = `<base href="${baseHref}">`;
        
        // 优先插入在 <head> 之后，若没有则在 <html> 之后，否则插在最前面
        if (/<head[^>]*>/i.test(text)) {
          text = text.replace(/(<head[^>]*>)/i, `$1${baseTag}`);
        } else if (/<html[^>]*>/i.test(text)) {
          text = text.replace(/(<html[^>]*>)/i, `$1${baseTag}`);
        } else {
          text = baseTag + text;
        }
      }

      // 匹配任意非当前 Worker 域名的 http/https 链接进行重写以走直连通道
      const escapedHost = workerHost.replace(/\./g, '\\.');
      const rewriteRegex = new RegExp(`(?<!https?:\\/\\/${escapedHost}\\/)(https?:\\/\\/(?!${escapedHost}(?:\\/|$))([a-zA-Z0-9-]+\\.)+[a-zA-Z0-9-]+(:\\d+)?)`, 'g');
      
      text = text.replace(rewriteRegex, `${workerProto}//${workerHost}/$1`);

      return new Response(text, {
        status: response.status,
        statusText: response.statusText,
        headers: modifiedHeaders
      });
    }

    return new Response(response.body, {
      status: response.status,
      statusText: response.statusText,
      headers: modifiedHeaders
    });
  }
};

/**
 * 动态渲染毛玻璃门户页面
 */
function renderPortalHTML(workerHost, cfInfo) {
  return `<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>${config.title} - 控制中心</title>
  <link rel="preconnect" href="https://fonts.googleapis.com">
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
  <link href="https://fonts.googleapis.com/css2?family=Outfit:wght@300;400;500;600;700;800&family=Noto+Sans+SC:wght@300;400;500;700&display=swap" rel="stylesheet">
  <style>
    :root {
      --bg-base: #060813;
      --glass-bg: rgba(15, 18, 36, 0.45);
      --glass-border: rgba(255, 255, 255, 0.08);
      --text-main: #f3f4f6;
      --text-muted: #9ca3af;
      --primary: linear-gradient(135deg, #6366f1, #a855f7);
      --primary-glow: rgba(168, 85, 247, 0.35);
      --card-hover-border: rgba(99, 102, 241, 0.3);
      --success: #10b981;
      --warning: #f59e0b;
      --error: #ef4444;
    }

    * {
      box-sizing: border-box;
      margin: 0;
      padding: 0;
    }

    body {
      font-family: 'Outfit', 'Noto Sans SC', sans-serif;
      background-color: var(--bg-base);
      color: var(--text-main);
      min-height: 100vh;
      display: flex;
      flex-direction: column;
      align-items: center;
      overflow-x: hidden;
      position: relative;
      padding: 40px 20px;
    }

    body::before, body::after {
      content: "";
      position: absolute;
      width: 500px;
      height: 500px;
      border-radius: 50%;
      background: radial-gradient(circle, var(--primary-glow) 0%, rgba(6, 8, 19, 0) 70%);
      z-index: -1;
      filter: blur(50px);
      pointer-events: none;
    }
    body::before {
      top: -10%;
      left: -10%;
      animation: float 18s infinite alternate ease-in-out;
    }
    body::after {
      bottom: -10%;
      right: -10%;
      animation: float 22s infinite alternate-reverse ease-in-out;
    }

    @keyframes float {
      0% { transform: translate(0, 0) scale(1); }
      100% { transform: translate(100px, 80px) scale(1.15); }
    }

    .container {
      max-width: 1000px;
      width: 100%;
      animation: cardEntrance 0.8s cubic-bezier(0.16, 1, 0.3, 1) forwards;
      opacity: 0;
      transform: translateY(20px);
    }

    @keyframes cardEntrance {
      to {
        transform: translateY(0);
        opacity: 1;
      }
    }

    /* 顶部导航与 Header */
    header {
      display: flex;
      justify-content: space-between;
      align-items: center;
      width: 100%;
      margin-bottom: 40px;
      position: relative;
    }

    .brand {
      display: flex;
      flex-direction: column;
    }

    .logo-title {
      font-size: 2.2rem;
      font-weight: 800;
      letter-spacing: -1px;
      background: linear-gradient(135deg, #a5b4fc 0%, #c084fc 50%, #f472b6 100%);
      -webkit-background-clip: text;
      -webkit-text-fill-color: transparent;
    }

    .subtitle {
      font-size: 0.95rem;
      color: var(--text-muted);
      margin-top: 4px;
      font-weight: 300;
    }

    /* 退出按钮 */
    .logout-btn {
      background: rgba(255, 255, 255, 0.03);
      border: 1px solid var(--glass-border);
      border-radius: 12px;
      padding: 8px 18px;
      color: var(--text-muted);
      font-size: 0.85rem;
      font-weight: 600;
      cursor: pointer;
      text-decoration: none;
      display: flex;
      align-items: center;
      gap: 6px;
      transition: all 0.3s cubic-bezier(0.25, 0.8, 0.25, 1);
    }

    .logout-btn:hover {
      background: rgba(239, 68, 68, 0.1);
      border-color: rgba(239, 68, 68, 0.3);
      color: #f87171;
      transform: translateY(-2px);
      box-shadow: 0 4px 12px rgba(239, 68, 68, 0.15);
    }

    /* 主布局 */
    .dashboard-grid {
      display: grid;
      grid-template-columns: 1.2fr 0.8fr;
      gap: 24px;
      width: 100%;
    }

    /* 卡片基础样式 */
    .card {
      background: var(--glass-bg);
      backdrop-filter: blur(20px);
      -webkit-backdrop-filter: blur(20px);
      border: 1px solid var(--glass-border);
      border-radius: 24px;
      padding: 30px;
      box-shadow: 0 20px 40px -15px rgba(0, 0, 0, 0.5);
      transition: border-color 0.3s ease, box-shadow 0.3s ease;
      position: relative;
    }

    .card:hover {
      border-color: var(--card-hover-border);
      box-shadow: 0 25px 50px -12px rgba(99, 102, 241, 0.15);
    }

    .card-title {
      font-size: 1.25rem;
      font-weight: 700;
      margin-bottom: 20px;
      display: flex;
      align-items: center;
      gap: 10px;
      background: linear-gradient(135deg, #f3f4f6 0%, #e5e7eb 100%);
      -webkit-background-clip: text;
      -webkit-text-fill-color: transparent;
    }

    .card-title svg {
      color: #818cf8;
    }

    /* 快捷直连 */
    .access-box {
      margin-top: 15px;
    }

    .access-input-group {
      display: flex;
      background: rgba(255, 255, 255, 0.03);
      border: 1px solid var(--glass-border);
      border-radius: 16px;
      padding: 6px;
      transition: all 0.3s ease;
    }

    .access-input-group:focus-within {
      border-color: #818cf8;
      box-shadow: 0 0 0 4px rgba(99, 102, 241, 0.15);
      background: rgba(255, 255, 255, 0.05);
    }

    .access-input {
      flex: 1;
      background: transparent;
      border: none;
      outline: none;
      color: var(--text-main);
      font-size: 1rem;
      padding: 12px 16px;
      font-family: inherit;
    }

    .access-input::placeholder {
      color: #6b7280;
    }

    .access-btn {
      background: var(--primary);
      color: white;
      border: none;
      outline: none;
      font-weight: 600;
      font-size: 0.95rem;
      padding: 0 24px;
      border-radius: 12px;
      cursor: pointer;
      display: flex;
      align-items: center;
      gap: 8px;
      transition: all 0.3s ease;
      box-shadow: 0 4px 12px var(--primary-glow);
    }

    .access-btn:hover {
      filter: brightness(1.1);
      transform: translateY(-1px);
      box-shadow: 0 6px 16px var(--primary-glow);
    }

    .access-tips {
      margin-top: 14px;
      font-size: 0.85rem;
      color: var(--text-muted);
      line-height: 1.5;
    }

    /* WebSocket 连接指引 */
    .link-card {
      margin-top: 24px;
    }

    .code-container {
      background: rgba(0, 0, 0, 0.3);
      border: 1px solid rgba(255, 255, 255, 0.05);
      border-radius: 12px;
      padding: 16px;
      position: relative;
      margin-top: 15px;
    }

    .code-text {
      font-family: 'Courier New', Courier, monospace;
      font-size: 0.85rem;
      color: #a5b4fc;
      word-break: break-all;
      padding-right: 40px;
    }

    .copy-btn {
      position: absolute;
      top: 12px;
      right: 12px;
      background: rgba(255, 255, 255, 0.05);
      border: 1px solid rgba(255, 255, 255, 0.08);
      border-radius: 8px;
      width: 32px;
      height: 32px;
      display: flex;
      align-items: center;
      justify-content: center;
      color: var(--text-muted);
      cursor: pointer;
      transition: all 0.2s ease;
    }

    .copy-btn:hover {
      background: rgba(255, 255, 255, 0.1);
      color: var(--text-main);
    }

    .link-steps {
      margin-top: 16px;
      padding-left: 20px;
      font-size: 0.85rem;
      color: var(--text-muted);
      line-height: 1.6;
    }

    .link-steps li {
      margin-bottom: 6px;
    }

    /* 用量监控卡片 */
    .usage-header {
      display: flex;
      justify-content: space-between;
      align-items: center;
      margin-bottom: 15px;
    }

    .usage-title {
      font-size: 0.85rem;
      font-weight: 600;
      color: var(--text-muted);
      letter-spacing: 0.5px;
      text-transform: uppercase;
    }

    .usage-badge {
      font-size: 0.75rem;
      padding: 4px 10px;
      border-radius: 20px;
      font-weight: 700;
    }

    .badge-loading {
      background: rgba(245, 158, 11, 0.15);
      color: #fbbf24;
      border: 1px solid rgba(245, 158, 11, 0.25);
    }

    .badge-success {
      background: rgba(16, 185, 129, 0.15);
      color: #34d399;
      border: 1px solid rgba(16, 185, 129, 0.25);
    }

    .badge-error {
      background: rgba(239, 68, 68, 0.15);
      color: #f87171;
      border: 1px solid rgba(239, 68, 68, 0.25);
    }

    .usage-bar-bg {
      height: 10px;
      background: rgba(255, 255, 255, 0.05);
      border-radius: 5px;
      overflow: hidden;
      margin-bottom: 12px;
      position: relative;
    }

    .usage-bar-fill {
      height: 100%;
      background: linear-gradient(90deg, #6366f1, #a855f7);
      border-radius: 5px;
      width: 0%;
      transition: width 1.2s cubic-bezier(0.16, 1, 0.3, 1);
      box-shadow: 0 0 12px rgba(168, 85, 247, 0.6);
    }

    .usage-footer {
      display: flex;
      justify-content: space-between;
      font-size: 0.85rem;
      color: var(--text-muted);
    }

    #usagePercent {
      font-weight: 700;
      color: var(--text-main);
    }

    /* 接入节点卡片 */
    .node-card {
      margin-top: 24px;
    }

    .grid-info {
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: 16px;
      margin-top: 15px;
    }

    .info-item {
      background: rgba(255, 255, 255, 0.02);
      border: 1px solid rgba(255, 255, 255, 0.04);
      border-radius: 12px;
      padding: 12px 16px;
    }

    .info-label {
      font-size: 0.75rem;
      color: var(--text-muted);
      text-transform: uppercase;
      letter-spacing: 0.5px;
      margin-bottom: 4px;
    }

    .info-value {
      font-size: 0.95rem;
      font-weight: 600;
      color: var(--text-main);
      word-break: break-all;
    }

    footer {
      margin-top: 60px;
      font-size: 0.8rem;
      color: #4b5563;
      text-align: center;
      width: 100%;
    }

    @media (max-width: 900px) {
      .dashboard-grid {
        grid-template-columns: 1fr;
      }
      .logo-title {
        font-size: 1.8rem;
      }
    }
  </style>
</head>
<body>
  <div class="container">
    <!-- 头部栏 -->
    <header>
      <div class="brand">
        <div class="logo-title">${config.title}</div>
        <div class="subtitle">${config.subtitle}</div>
      </div>
      <a class="logout-btn" href="/logout">
        <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
          <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"></path>
          <polyline points="16 17 21 12 16 7"></polyline>
          <line x1="21" y1="12" x2="9" y2="12"></line>
        </svg>
        <span>退出系统</span>
      </a>
    </header>

    <!-- 主面板 -->
    <div class="dashboard-grid">
      <!-- 左侧栏：网络与接入配置 -->
      <div class="layout-left">
        <!-- 快捷直连控制台 -->
        <div class="card">
          <div class="card-title">
            <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <circle cx="12" cy="12" r="10"></circle>
              <line x1="2" y1="12" x2="22" y2="12"></line>
              <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"></path>
            </svg>
            <span>快捷网络直连 (Direct Access)</span>
          </div>
          <div class="access-box">
            <form id="accessForm" onsubmit="handleAccessSubmit(event)">
              <div class="access-input-group">
                <input class="access-input" type="text" id="targetUrl" placeholder="输入您想直达的网址 (如 google.com)" required autocomplete="off">
                <button class="access-btn" type="submit">
                  <span>开启直连</span>
                  <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                    <line x1="5" y1="12" x2="19" y2="12"></line>
                    <polyline points="12 5 19 12 12 19"></polyline>
                  </svg>
                </button>
              </div>
            </form>
            <p class="access-tips">
              <b>使用提示：</b>请输入完整网址或域名。系统将重写并优化页面内的超链接，并自动规避区域性限制，保障流畅直连。
            </p>
          </div>
        </div>

        <!-- WebSocket 连接通道 -->
        <div class="card link-card">
          <div class="card-title">
            <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <path d="M22 12h-4l-3 9L9 3l-3 9H2"></path>
            </svg>
            <span>专用网络接入指南 (Direct Link)</span>
          </div>
          <p class="access-tips" style="margin-top: 0;">
            本地客户端可通过与本 Worker 的 WebSocket 建立双向数据通道进行直连传输：
          </p>
          <div class="code-container">
            <div class="code-text" id="connectCode">wss://${workerHost}/tunnel?secret=${config.secret}&host=TARGET_HOST&port=TARGET_PORT</div>
            <button class="copy-btn" onclick="copyLinkUrl()" title="复制连接地址">
              <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
              </svg>
            </button>
          </div>
          <ul class="link-steps">
            <li><b>秘密密钥：</b>通过查询参数 <code>secret</code> 鉴权。</li>
            <li><b>目标服务器：</b>将 <code>TARGET_HOST</code> 和 <code>TARGET_PORT</code> 替换为您想要直连的目标服务器及端口。</li>
            <li><b>无挂起：</b>数据在后台采用流式传输，在连接异常或中断时会自动安全释放，保障轻量级运行。</li>
          </ul>
        </div>
      </div>

      <!-- 右侧栏：状态与统计 -->
      <div class="layout-right">
        <!-- 额度监控 -->
        <div class="card">
          <div class="card-title">
            <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <line x1="18" y1="20" x2="18" y2="10"></line>
              <line x1="12" y1="20" x2="12" y2="4"></line>
              <line x1="6" y1="20" x2="6" y2="14"></line>
            </svg>
            <span>用量额度统计</span>
          </div>
          <div class="usage-container" id="usageContainer">
            <div class="usage-header">
              <span class="usage-title">系统今日已用额度</span>
              <span class="usage-badge" id="usageBadge">检测中...</span>
            </div>
            <div class="usage-bar-bg">
              <div class="usage-bar-fill" id="usageBarFill"></div>
            </div>
            <div class="usage-footer">
              <span id="usageText">已用: -- / -- 请求</span>
              <span id="usagePercent">0%</span>
            </div>
          </div>
        </div>

        <!-- 接入节点状态 -->
        <div class="card node-card">
          <div class="card-title">
            <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <path d="M21 10c0 7-9 13-9 13s-9-6-9-13a9 9 0 0 1 18 0z"></path>
              <circle cx="12" cy="10" r="3"></circle>
            </svg>
            <span>接入节点状态</span>
          </div>
          <div class="grid-info">
            <div class="info-item">
              <div class="info-label">客户端 IP</div>
              <div class="info-value">${cfInfo.ip}</div>
            </div>
            <div class="info-item">
              <div class="info-label">CF 边缘节点</div>
              <div class="info-value">${cfInfo.colo}</div>
            </div>
            <div class="info-item">
              <div class="info-label">接入城市</div>
              <div class="info-value">${cfInfo.city}</div>
            </div>
            <div class="info-item">
              <div class="info-label">国家 / 地区</div>
              <div class="info-value">${cfInfo.country}</div>
            </div>
            <div class="info-item" style="grid-column: span 2;">
              <div class="info-label">运营商 ASN</div>
              <div class="info-value">AS${cfInfo.asn}</div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <footer>
      本服务仅运行在安全且私有的 Cloudflare Workers Serverless 内存中
    </footer>
  </div>

  <script>
    // 快捷直连提交
    function handleAccessSubmit(e) {
      e.preventDefault();
      const input = document.getElementById('targetUrl');
      let val = input.value.trim();
      if (!val) return;
      
      // 检查并自动补全协议
      if (!val.startsWith('http://') && !val.startsWith('https://')) {
        // 如果是简写域名，自动使用 https 补全
        val = 'https://' + val;
      }
      
      window.location.href = window.location.origin + '/' + val;
    }

    // 复制连接链接
    function copyLinkUrl() {
      const urlText = document.getElementById('connectCode').innerText;
      navigator.clipboard.writeText(urlText).then(() => {
        alert('连接地址已复制到剪贴板！');
      }).catch(err => {
        alert('复制失败，请手动选择复制。');
      });
    }

    // 加载并显示用量
    async function loadUsage() {
      const fill = document.getElementById('usageBarFill');
      const text = document.getElementById('usageText');
      const percent = document.getElementById('usagePercent');
      const badge = document.getElementById('usageBadge');

      badge.className = 'usage-badge badge-loading';
      badge.innerText = '获取中...';

      try {
        const response = await fetch('/api/usage');
        const data = await response.json();
        
        if (data.success) {
          const today = data.todayRequests;
          const limit = data.limit;
          const pct = Math.min(100, Math.round((today / limit) * 100));
          
          fill.style.width = pct + '%';
          text.innerText = '已用: ' + today.toLocaleString() + ' / ' + limit.toLocaleString() + ' 请求';
          percent.innerText = pct + '%';
          
          if (pct >= 90) {
            badge.className = 'usage-badge badge-error';
            badge.innerText = '额度告急';
          } else {
            badge.className = 'usage-badge badge-success';
            badge.innerText = '运行正常';
          }
        } else {
          badge.className = 'usage-badge badge-error';
          badge.innerText = data.configured ? '获取失败' : '未配置凭证';
          text.innerText = data.message || '配置未生效或获取失败';
          percent.innerText = '0%';
        }
      } catch (err) {
        badge.className = 'usage-badge badge-error';
        badge.innerText = '网络异常';
        text.innerText = '用量接口请求失败';
      }
    }

    loadUsage();
  </script>
</body>
</html>`;
}

/**
 * 错误渲染页面
 */
function renderErrorHTML(title, detail, workerHost) {
  return `<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>请求遇到问题 - ${config.title}</title>
  <link href="https://fonts.googleapis.com/css2?family=Outfit:wght@400;600;800&family=Noto+Sans+SC:wght@300;400;700&display=swap" rel="stylesheet">
  <style>
    body {
      font-family: 'Outfit', 'Noto Sans SC', sans-serif;
      background-color: #060813;
      color: #f3f4f6;
      display: flex;
      justify-content: center;
      align-items: center;
      min-height: 100vh;
      margin: 0;
    }
    .card {
      background: rgba(15, 18, 36, 0.45);
      backdrop-filter: blur(20px);
      -webkit-backdrop-filter: blur(20px);
      border: 1px solid rgba(239, 68, 68, 0.2);
      border-radius: 24px;
      padding: 40px;
      max-width: 500px;
      width: 90%;
      text-align: center;
      box-shadow: 0 20px 50px rgba(0, 0, 0, 0.5);
    }
    h1 {
      color: #ef4444;
      font-size: 1.8rem;
      margin-bottom: 15px;
    }
    p {
      color: #9ca3af;
      line-height: 1.6;
      margin-bottom: 30px;
    }
    .btn {
      background: linear-gradient(135deg, #6366f1, #a855f7);
      color: white;
      text-decoration: none;
      font-weight: 600;
      padding: 12px 28px;
      border-radius: 12px;
      display: inline-block;
      transition: transform 0.2s ease;
    }
    .btn:hover {
      transform: translateY(-2px);
    }
  </style>
</head>
<body>
  <div class="card">
    <h1>${title}</h1>
    <p>${detail}</p>
    <a class="btn" href="https://${workerHost}/">返回主页门户</a>
  </div>
</body>
</html>`;
}

/**
 * 登录渲染页面 (SuperBlog Branded)
 */
function renderLoginHTML(workerHost) {
  return `<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>登录 - ${config.title}</title>
  <link rel="preconnect" href="https://fonts.googleapis.com">
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
  <link href="https://fonts.googleapis.com/css2?family=Outfit:wght@300;400;600;800&family=Noto+Sans+SC:wght@300;400;700&display=swap" rel="stylesheet">
  <style>
    :root {
      --bg-base: #060813;
      --glass-bg: rgba(15, 18, 36, 0.45);
      --glass-border: rgba(255, 255, 255, 0.08);
      --text-main: #f3f4f6;
      --text-muted: #9ca3af;
      --primary: linear-gradient(135deg, #6366f1, #a855f7);
      --primary-glow: rgba(168, 85, 247, 0.35);
      --card-hover-border: rgba(99, 102, 241, 0.3);
    }

    * {
      box-sizing: border-box;
      margin: 0;
      padding: 0;
    }

    body {
      font-family: 'Outfit', 'Noto Sans SC', sans-serif;
      background-color: var(--bg-base);
      color: var(--text-main);
      min-height: 100vh;
      display: flex;
      flex-direction: column;
      justify-content: center;
      align-items: center;
      overflow: hidden;
      position: relative;
    }

    body::before, body::after {
      content: "";
      position: absolute;
      width: 450px;
      height: 450px;
      border-radius: 50%;
      background: radial-gradient(circle, var(--primary-glow) 0%, rgba(6, 8, 19, 0) 70%);
      z-index: -1;
      filter: blur(40px);
    }
    body::before {
      top: -10%;
      left: -10%;
      animation: float 15s infinite alternate ease-in-out;
    }
    body::after {
      bottom: -10%;
      right: -10%;
      animation: float 20s infinite alternate-reverse ease-in-out;
    }

    @keyframes float {
      0% { transform: translate(0, 0) scale(1); }
      100% { transform: translate(80px, 50px) scale(1.2); }
    }

    .container {
      max-width: 450px;
      width: 90%;
      perspective: 1000px;
    }

    .card {
      background: var(--glass-bg);
      backdrop-filter: blur(24px);
      -webkit-backdrop-filter: blur(24px);
      border: 1px solid var(--glass-border);
      border-radius: 28px;
      padding: 48px 40px;
      box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.5);
      text-align: center;
      animation: cardEntrance 0.8s cubic-bezier(0.16, 1, 0.3, 1) forwards;
      transform: translateY(30px);
      opacity: 0;
    }

    @keyframes cardEntrance {
      to {
        transform: translateY(0);
        opacity: 1;
      }
    }

    .logo-title {
      font-size: 3rem;
      font-weight: 800;
      letter-spacing: -1.5px;
      background: linear-gradient(135deg, #a5b4fc 0%, #c084fc 50%, #f472b6 100%);
      -webkit-background-clip: text;
      -webkit-text-fill-color: transparent;
      margin-bottom: 8px;
    }

    .subtitle {
      font-size: 1rem;
      color: var(--text-muted);
      margin-bottom: 35px;
      font-weight: 300;
    }

    .input-group {
      margin-bottom: 20px;
      text-align: left;
    }

    .input-label {
      display: block;
      font-size: 0.85rem;
      color: var(--text-muted);
      margin-bottom: 8px;
      font-weight: 600;
      letter-spacing: 0.5px;
      text-transform: uppercase;
    }

    .input-wrapper {
      position: relative;
      display: flex;
      align-items: center;
      background: rgba(255, 255, 255, 0.03);
      border: 1px solid var(--glass-border);
      border-radius: 14px;
      padding: 0 15px;
      transition: all 0.3s ease;
    }

    .input-wrapper:focus-within {
      border-color: #818cf8;
      box-shadow: 0 0 0 4px rgba(99, 102, 241, 0.15);
      background: rgba(255, 255, 255, 0.05);
    }

    .input-icon {
      color: var(--text-muted);
      margin-right: 12px;
      flex-shrink: 0;
    }

    .input-field {
      background: transparent;
      border: none;
      outline: none;
      color: var(--text-main);
      font-size: 1rem;
      font-family: inherit;
      width: 100%;
      height: 48px;
    }

    .btn-submit {
      width: 100%;
      background: var(--primary);
      color: white;
      border: none;
      outline: none;
      font-weight: 600;
      font-size: 1.05rem;
      height: 52px;
      border-radius: 14px;
      cursor: pointer;
      display: flex;
      align-items: center;
      justify-content: center;
      gap: 10px;
      margin-top: 30px;
      transition: all 0.3s cubic-bezier(0.25, 0.8, 0.25, 1);
      box-shadow: 0 4px 12px var(--primary-glow);
    }

    .btn-submit:hover {
      transform: translateY(-2px);
      box-shadow: 0 8px 20px var(--primary-glow);
      filter: brightness(1.1);
    }

    .btn-submit:active {
      transform: translateY(0);
    }

    .error-msg {
      background: rgba(239, 68, 68, 0.15);
      border: 1px solid rgba(239, 68, 68, 0.3);
      color: #f87171;
      padding: 12px 16px;
      border-radius: 12px;
      font-size: 0.9rem;
      margin-bottom: 24px;
      display: none;
      align-items: center;
      gap: 8px;
      animation: shake 0.4s ease-in-out;
    }

    @keyframes shake {
      0%, 100% { transform: translateX(0); }
      20%, 60% { transform: translateX(-6px); }
      40%, 80% { transform: translateX(6px); }
    }

    footer {
      margin-top: 30px;
      font-size: 0.8rem;
      color: #4b5563;
      text-align: center;
    }
  </style>
</head>
<body>
  <div class="container">
    <div class="card">
      <div class="logo-title">${config.title}</div>
      <div class="subtitle">${config.subtitle}</div>

      <div class="error-msg" id="errorMsg">
        <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="12" cy="12" r="10"></circle>
          <line x1="12" y1="8" x2="12" y2="12"></line>
          <line x1="12" y1="16" x2="12.01" y2="16"></line>
        </svg>
        <span id="errorText">用户名或密码错误</span>
      </div>

      <form id="loginForm">
        <div class="input-group">
          <label class="input-label">用户名</label>
          <div class="input-wrapper">
            <svg class="input-icon" xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"></path>
              <circle cx="12" cy="7" r="4"></circle>
            </svg>
            <input class="input-field" type="text" id="username" placeholder="请输入管理员账号" required autocomplete="username">
          </div>
        </div>

        <div class="input-group">
          <label class="input-label">密码</label>
          <div class="input-wrapper">
            <svg class="input-icon" xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <rect x="3" y="11" width="18" height="11" rx="2" ry="2"></rect>
              <path d="M7 11V7a5 5 0 0 1 10 0v4"></path>
            </svg>
            <input class="input-field" type="password" id="password" placeholder="请输入管理员密码" required autocomplete="current-password">
          </div>
        </div>

        <button class="btn-submit" type="submit" id="submitBtn">
          <span>验证并进入系统</span>
          <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <line x1="5" y1="12" x2="19" y2="12"></line>
            <polyline points="12 5 19 12 12 19"></polyline>
          </svg>
        </button>
      </form>
    </div>
    <footer>
      运行在私有且安全的 Cloudflare Workers 环境中
    </footer>
  </div>

  <script>
    document.getElementById('loginForm').addEventListener('submit', async function(e) {
      e.preventDefault();
      const usernameInput = document.getElementById('username');
      const passwordInput = document.getElementById('password');
      const submitBtn = document.getElementById('submitBtn');
      const errorMsg = document.getElementById('errorMsg');
      const errorText = document.getElementById('errorText');

      errorMsg.style.display = 'none';
      submitBtn.disabled = true;
      submitBtn.querySelector('span').innerText = '正在验证...';

      try {
        const formData = new FormData();
        formData.append('username', usernameInput.value.trim());
        formData.append('password', passwordInput.value);

        const response = await fetch('/login', {
          method: 'POST',
          body: formData
        });

        const data = await response.json();
        if (response.ok && data.success) {
          window.location.href = '/';
        } else {
          showError(data.message || '登录失败，请检查账号密码');
        }
      } catch (err) {
        showError('网络连接错误，请稍后再试');
      } finally {
        submitBtn.disabled = false;
        submitBtn.querySelector('span').innerText = '验证并进入系统';
      }
    });

    function showError(text) {
      const errorMsg = document.getElementById('errorMsg');
      const errorText = document.getElementById('errorText');
      errorText.innerText = text;
      errorMsg.style.display = 'flex';
      errorMsg.style.animation = 'none';
      errorMsg.offsetHeight;
      errorMsg.style.animation = null;
    }
  </script>
</body>
</html>`;
}
