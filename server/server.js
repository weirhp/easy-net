const http = require('http');
const WebSocket = require('ws');
const net = require('net');
const url = require('url');

// 服务运行端口
const PORT = process.env.PORT || 3000;

// 支持多个访问凭证 (通过环境变量 SECRETS 以逗号分隔传入，如: key1,key2,key3)
const allowedSecrets = process.env.SECRETS 
  ? process.env.SECRETS.split(',').map(s => s.trim()).filter(Boolean) 
  : [];

// 管理员查询凭证，用于获取流量统计信息
const ADMIN_KEY = process.env.ADMIN_KEY || null;

// 内存中初始化每个访问凭证的流量统计对象
const trafficStats = {};
allowedSecrets.forEach(sec => {
  trafficStats[sec] = {
    uploadBytes: 0,       // 上行流量 (字节)
    downloadBytes: 0,     // 下行流量 (字节)
    activeConnections: 0  // 当前活跃连接数
  };
});

// 默认主页展示的正常 HTML 内容
const htmlContent = `
<!DOCTYPE html>
<html>
<head>
  <title>React Dashboard Demo</title>
  <style>
    body { font-family: sans-serif; text-align: center; padding: 50px; background: #f4f6f9; color: #333; }
    h1 { color: #4f46e5; }
    .card { background: white; padding: 20px; border-radius: 8px; box-shadow: 0 4px 6px rgba(0,0,0,0.1); display: inline-block; max-width: 400px; }
  </style>
</head>
<body>
  <div class="card">
    <h1>React Project Dashboard</h1>
    <p>Status: <span style="color: green; font-weight: bold;">Running</span></p>
    <p>Environment: Production</p>
    <p>This is a demonstration application hosted on the server.</p>
  </div>
</body>
</html>
`;

// 创建 HTTP 服务，响应正常仪表盘或流量统计数据
const server = http.createServer((req, res) => {
  const parsedUrl = url.parse(req.url, true);
  
  if (parsedUrl.pathname === '/') {
    // 1. 响应主页展示
    res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
    res.end(htmlContent);
  } else if (parsedUrl.pathname === '/stats') {
    // 2. 响应流量统计接口 (需要正确的管理员凭证进行验证，防止数据泄露)
    const { admin_key } = parsedUrl.query;
    if (ADMIN_KEY && admin_key === ADMIN_KEY) {
      res.writeHead(200, { 'Content-Type': 'application/json; charset=utf-8' });
      res.end(JSON.stringify({
        status: "success",
        timestamp: new Date().toISOString(),
        stats: trafficStats
      }, null, 2));
    } else {
      // 凭证不匹配时，返回标准 404 响应以隐藏接口存在
      res.writeHead(404, { 'Content-Type': 'text/html; charset=utf-8' });
      res.end('<!DOCTYPE html><html><head><title>404 Not Found</title></head><body><center><h1>404 Not Found</h1></center><hr><center>nginx</center></body></html>');
    }
  } else {
    // 3. 模拟标准 Web 服务的 404 响应，防主动探测
    res.writeHead(404, { 'Content-Type': 'text/html; charset=utf-8' });
    res.end('<!DOCTYPE html><html><head><title>404 Not Found</title></head><body><center><h1>404 Not Found</h1></center><hr><center>nginx</center></body></html>');
  }
});

// 独立创建 WebSocket 服务，不自动绑定 HTTP 升级事件
const wss = new WebSocket.Server({ noServer: true });

// 监听升级事件，手动进行安全校验与升级
server.on('upgrade', (request, socket, head) => {
  const parsedUrl = url.parse(request.url, true);
  const { secret, host, port } = parsedUrl.query;

  // 仅当访问特定路径、凭证在白名单内且参数完整时，才允许建立网络连接
  if (parsedUrl.pathname === '/tunnel' && allowedSecrets.includes(secret) && host && port) {
    wss.handleUpgrade(request, socket, head, (ws) => {
      // 在 ws 实例上记录当前连接对应的凭证，方便统计使用
      ws.clientSecret = secret;
      wss.emit('connection', ws, request);
    });
  } else {
    // 凭证错误或路径不对，返回标准 Nginx 404 静态响应，防探测识别
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

server.listen(PORT, () => {
  console.log(`=================================================`);
  console.log(`[Easy-Net] 服务端已成功启动！`);
  console.log(`监听端口: ${PORT}`);
  console.log(`支持凭证数量: ${allowedSecrets.length}`);
  if (allowedSecrets.length === 0) {
    console.warn(`[警告] 未配置 SECRETS 环境变量，且无默认凭证。所有客户端连接均会被拒绝！`);
  }
  if (!ADMIN_KEY) {
    console.log(`[提示] 未配置 ADMIN_KEY 环境变量，流量统计接口已禁用。`);
  }
  console.log(`=================================================`);
});

wss.on('connection', (ws, req) => {
  const secret = ws.clientSecret;
  const parsedUrl = url.parse(req.url, true);
  const { host, port } = parsedUrl.query;

  try {
    console.log(`[Easy-Net] [连接] 凭证 [${secret.substring(0, 8)}...] 请求网络连接 -> ${host}:${port}`);

    // 更新当前凭证的活跃连接数
    if (trafficStats[secret]) {
      trafficStats[secret].activeConnections++;
    }

    // 创建到目标主机的原生 TCP 连接
    const targetSocket = net.connect(port, host, () => {
      console.log(`[Easy-Net] [连接] 成功与目标建立连接 -> ${host}:${port}`);

      // 启用 TCP 保活
      targetSocket.setKeepAlive(true, 30000);

      // 数据转发与流量统计 (客户端 -> 目标主机：上行)
      ws.on('message', data => {
        if (targetSocket.writable) {
          targetSocket.write(data);
          if (trafficStats[secret]) {
            trafficStats[secret].uploadBytes += data.length;
          }
        }
      });

      // 数据转发与流量统计 (目标主机 -> 客户端：下行)
      targetSocket.on('data', data => {
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(data);
          if (trafficStats[secret]) {
            trafficStats[secret].downloadBytes += data.length;
          }
        }
      });
    });

    // 绑定心跳检测保活
    ws.isAlive = true;
    ws.on('pong', () => {
      ws.isAlive = true;
    });

    // 清理连接并扣减活跃连接数的封装函数
    let hasCleaned = false;
    const cleanupConnection = () => {
      if (hasCleaned) return;
      hasCleaned = true;
      if (trafficStats[secret] && trafficStats[secret].activeConnections > 0) {
        trafficStats[secret].activeConnections--;
      }
      targetSocket.destroy();
    };

    targetSocket.on('error', err => {
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
      console.error(`[Easy-Net] [错误] 连接发生错误 -> ${host}:${port}: ${err.message}`);
      cleanupConnection();
    });

  } catch (err) {
    console.error(`[Easy-Net] [严重错误] 处理连接时崩溃: ${err.message}`);
    ws.close();
  }
});

// 定时清理非活动死链接 (每 30 秒检测一次)
const interval = setInterval(() => {
  wss.clients.forEach(ws => {
    if (ws.isAlive === false) {
      return ws.terminate();
    }
    ws.isAlive = false;
    ws.ping();
  });
}, 30000);

wss.on('close', () => {
  clearInterval(interval);
});
