const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const net = require('node:net');
const os = require('node:os');
const path = require('node:path');
const { spawn } = require('node:child_process');
const WebSocket = require('ws');

const listen = server => new Promise((resolve, reject) => {
  server.once('error', reject);
  server.listen(0, '127.0.0.1', () => resolve(server.address().port));
});

const close = server => new Promise(resolve => server.close(resolve));

const requestJson = (port, requestPath) => new Promise((resolve, reject) => {
  const request = http.get({ host: '127.0.0.1', port, path: requestPath }, response => {
    let body = '';
    response.setEncoding('utf8');
    response.on('data', chunk => { body += chunk; });
    response.once('end', () => {
      if (response.statusCode !== 200) {
        reject(new Error(`unexpected status ${response.statusCode}: ${body}`));
        return;
      }
      try {
        resolve(JSON.parse(body));
      } catch (err) {
        reject(err);
      }
    });
  });
  request.once('error', reject);
});

async function startEasyNetServer(t, secret, adminKey) {
  const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), 'easy-net-backpressure-test-'));
  t.after(() => fs.rmSync(dataDir, { recursive: true, force: true }));
  const portProbe = http.createServer();
  const serverPort = await listen(portProbe);
  await close(portProbe);
  const child = spawn(process.execPath, ['server.js'], {
    cwd: __dirname,
    env: {
      ...process.env,
      PORT: String(serverPort),
      DATA_DIR: dataDir,
      SECRETS: secret,
      ADMIN_KEY: adminKey,
      ADMIN_PASSWORD: 'test-admin-password',
      CONNECTION_LOG_ENABLED: 'false'
    },
    stdio: ['ignore', 'pipe', 'pipe']
  });
  t.after(() => {
    if (!child.killed) child.kill();
  });

  let output = '';
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`server start timeout: ${output}`)), 10000);
    const onData = chunk => {
      output += chunk.toString();
      if (output.includes('服务端已成功启动')) {
        clearTimeout(timer);
        resolve();
      }
    };
    child.stdout.on('data', onData);
    child.stderr.on('data', chunk => { output += chunk.toString(); });
    child.once('exit', code => {
      clearTimeout(timer);
      reject(new Error(`server exited early (${code}): ${output}`));
    });
  });
  return serverPort;
}

test('TCP upload pauses WebSocket reads until the target socket drains', { timeout: 30000 }, async t => {
  const uploadBytes = 8 * 1024 * 1024;
  let receivedBytes = 0;
  let resolveUpload;
  const uploadReceived = new Promise(resolve => { resolveUpload = resolve; });
  const target = net.createServer(socket => {
    socket.pause();
    setTimeout(() => socket.resume(), 300);
    socket.on('data', chunk => {
      receivedBytes += chunk.length;
      if (receivedBytes >= uploadBytes) resolveUpload();
    });
  });
  const targetPort = await listen(target);
  t.after(() => close(target));

  const secret = 'backpressure-test-secret-2026';
  const adminKey = 'backpressure-test-admin-key';
  const serverPort = await startEasyNetServer(t, secret, adminKey);
  const ws = new WebSocket(`ws://127.0.0.1:${serverPort}/tunnel`, {
    headers: {
      Authorization: `Bearer ${secret}`,
      'X-Target-Host': '127.0.0.1',
      'X-Target-Port': String(targetPort),
      'X-Easy-Net-Protocol': '2'
    }
  });
  t.after(() => ws.terminate());

  await new Promise((resolve, reject) => {
    ws.once('error', reject);
    ws.on('message', (data, isBinary) => {
      if (isBinary || data.toString() !== 'READY') return;
      const chunk = Buffer.alloc(64 * 1024, 0x5a);
      for (let sent = 0; sent < uploadBytes; sent += chunk.length) ws.send(chunk);
      resolve();
    });
  });

  await uploadReceived;
  assert.equal(receivedBytes, uploadBytes);

  let stats;
  for (let attempt = 0; attempt < 20; attempt++) {
    stats = await requestJson(serverPort, `/stats?admin_key=${adminKey}`);
    if (stats.runtime.websocket.resumedClientReads > 0) break;
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  assert.ok(stats.runtime.websocket.pausedClientReads > 0, 'upload never activated target backpressure');
  assert.ok(stats.runtime.websocket.resumedClientReads > 0, 'WebSocket reads did not resume after target drain');

  ws.close();
});
