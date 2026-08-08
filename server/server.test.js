const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const dgram = require('node:dgram');
const os = require('node:os');
const path = require('node:path');
const { spawn } = require('node:child_process');
const WebSocket = require('ws');

const listen = server => new Promise((resolve, reject) => {
  server.once('error', reject);
  server.listen(0, '127.0.0.1', () => resolve(server.address().port));
});

const close = server => new Promise(resolve => server.close(resolve));

const listenUdp = socket => new Promise((resolve, reject) => {
  socket.once('error', reject);
  socket.bind(0, '127.0.0.1', () => resolve(socket.address().port));
});

const closeUdp = socket => new Promise(resolve => socket.close(resolve));

async function startEasyNetServer(t, secret, prefix) {
  const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), prefix));
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
      ADMIN_PASSWORD: 'test-admin-password'
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

test('secure header protocol relays target traffic', { timeout: 20000 }, async t => {
  const target = http.createServer((req, res) => {
    res.writeHead(200, { 'Content-Type': 'text/plain', 'Content-Length': '9' });
    res.end('target-ok');
  });
  const targetPort = await listen(target);
  t.after(() => close(target));

  const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), 'easy-net-server-test-'));
  t.after(() => fs.rmSync(dataDir, { recursive: true, force: true }));
  const secret = 'header-protocol-test-secret-2026';
  const serverPortProbe = http.createServer();
  const serverPort = await listen(serverPortProbe);
  await close(serverPortProbe);
  const child = spawn(process.execPath, ['server.js'], {
    cwd: __dirname,
    env: {
      ...process.env,
      PORT: String(serverPort),
      DATA_DIR: dataDir,
      SECRETS: secret,
      ADMIN_PASSWORD: 'test-admin-password'
    },
    stdio: ['ignore', 'pipe', 'pipe']
  });
  t.after(() => {
    if (!child.killed) child.kill();
  });

  let serverOutput = '';
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`server start timeout: ${serverOutput}`)), 10000);
    const onData = chunk => {
      serverOutput += chunk.toString();
      if (serverOutput.includes('服务端已成功启动')) {
        clearTimeout(timer);
        resolve();
      }
    };
    child.stdout.on('data', onData);
    child.stderr.on('data', chunk => { serverOutput += chunk.toString(); });
    child.once('exit', code => {
      clearTimeout(timer);
      reject(new Error(`server exited early (${code}): ${serverOutput}`));
    });
  });

  const response = await new Promise((resolve, reject) => {
    const ws = new WebSocket(`ws://127.0.0.1:${serverPort}/tunnel`, {
      headers: {
        Authorization: `Bearer ${secret}`,
        'X-Target-Host': '127.0.0.1',
        'X-Target-Port': String(targetPort)
      }
    });
    let received = '';
    const timer = setTimeout(() => {
      ws.terminate();
      reject(new Error('relay response timeout'));
    }, 8000);
    ws.once('open', () => {
      ws.send('GET / HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n');
    });
    ws.on('message', data => {
      received += data.toString();
      if (received.includes('target-ok')) {
        clearTimeout(timer);
        ws.close();
        resolve(received);
      }
    });
    ws.once('error', err => {
      clearTimeout(timer);
      reject(err);
    });
  });

  assert.match(response, /^HTTP\/1\.1 200 OK/m);
  assert.match(response, /target-ok/);
});

test('v2 tunnel waits for the target connection before reporting ready', { timeout: 20000 }, async t => {
  const target = http.createServer((req, res) => {
    res.writeHead(200, { 'Content-Type': 'text/plain', 'Content-Length': '8' });
    res.end('v2-ready');
  });
  const targetPort = await listen(target);
  t.after(() => close(target));

  const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), 'easy-net-server-v2-test-'));
  t.after(() => fs.rmSync(dataDir, { recursive: true, force: true }));
  const secret = 'v2-protocol-test-secret-2026';
  const serverPortProbe = http.createServer();
  const serverPort = await listen(serverPortProbe);
  await close(serverPortProbe);
  const child = spawn(process.execPath, ['server.js'], {
    cwd: __dirname,
    env: {
      ...process.env,
      PORT: String(serverPort),
      DATA_DIR: dataDir,
      SECRETS: secret,
      ADMIN_PASSWORD: 'test-admin-password'
    },
    stdio: ['ignore', 'pipe', 'pipe']
  });
  t.after(() => {
    if (!child.killed) child.kill();
  });

  let serverOutput = '';
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`server start timeout: ${serverOutput}`)), 10000);
    const onData = chunk => {
      serverOutput += chunk.toString();
      if (serverOutput.includes('服务端已成功启动')) {
        clearTimeout(timer);
        resolve();
      }
    };
    child.stdout.on('data', onData);
    child.stderr.on('data', chunk => { serverOutput += chunk.toString(); });
    child.once('exit', code => {
      clearTimeout(timer);
      reject(new Error(`server exited early (${code}): ${serverOutput}`));
    });
  });

  const response = await new Promise((resolve, reject) => {
    const ws = new WebSocket(`ws://127.0.0.1:${serverPort}/tunnel`, {
      headers: {
        Authorization: `Bearer ${secret}`,
        'X-Target-Host': '127.0.0.1',
        'X-Target-Port': String(targetPort),
        'X-Easy-Net-Protocol': '2'
      }
    });
    let received = '';
    let upgradedProtocol = '';
    const timer = setTimeout(() => {
      ws.terminate();
      reject(new Error('v2 relay response timeout'));
    }, 8000);
    ws.once('upgrade', res => {
      upgradedProtocol = res.headers['x-easy-net-protocol'];
    });
    ws.on('message', (data, isBinary) => {
      if (!isBinary && data.toString() === 'READY') {
        ws.send('GET / HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n');
        return;
      }
      received += data.toString();
      if (received.includes('v2-ready')) {
        clearTimeout(timer);
        ws.close();
        resolve({ received, upgradedProtocol });
      }
    });
    ws.once('error', err => {
      clearTimeout(timer);
      reject(err);
    });
  });

  assert.equal(response.upgradedProtocol, '2');
  assert.match(response.received, /^HTTP\/1\.1 200 OK/m);
  assert.match(response.received, /v2-ready/);
});

test('v3 UDP tunnel preserves datagram boundaries and target addresses', { timeout: 20000 }, async t => {
  const target = dgram.createSocket('udp4');
  target.on('message', (message, remote) => {
    target.send(Buffer.concat([Buffer.from('udp:'), message]), remote.port, remote.address);
  });
  const targetPort = await listenUdp(target);
  t.after(() => closeUdp(target));

  const secret = 'udp-protocol-test-secret-2026';
  const serverPort = await startEasyNetServer(t, secret, 'easy-net-server-udp-test-');
  const result = await new Promise((resolve, reject) => {
    const ws = new WebSocket(`ws://127.0.0.1:${serverPort}/tunnel`, {
      headers: {
        Authorization: `Bearer ${secret}`,
        'X-Easy-Net-Protocol': '3',
        'X-Easy-Net-Network': 'udp'
      }
    });
    let upgradedProtocol = '';
    const timer = setTimeout(() => {
      ws.terminate();
      reject(new Error('UDP relay response timeout'));
    }, 8000);
    ws.once('upgrade', response => {
      upgradedProtocol = response.headers['x-easy-net-protocol'];
    });
    ws.on('message', (data, isBinary) => {
      if (!isBinary && data.toString() === 'READY') {
        const frame = Buffer.alloc(1 + 4 + 2 + 5);
        frame[0] = 0x01;
        frame.set([127, 0, 0, 1], 1);
        frame.writeUInt16BE(targetPort, 5);
        frame.write('hello', 7);
        ws.send(frame);
        return;
      }
      if (!isBinary) return;
      clearTimeout(timer);
      ws.close();
      resolve({ frame: Buffer.from(data), upgradedProtocol });
    });
    ws.once('error', err => {
      clearTimeout(timer);
      reject(err);
    });
  });

  assert.equal(result.upgradedProtocol, '3');
  assert.equal(result.frame[0], 0x01);
  assert.deepEqual([...result.frame.subarray(1, 5)], [127, 0, 0, 1]);
  assert.equal(result.frame.readUInt16BE(5), targetPort);
  assert.equal(result.frame.subarray(7).toString(), 'udp:hello');
});
