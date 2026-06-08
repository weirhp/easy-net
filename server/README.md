# Easy-Net WebSocket 代理中继服务端 (VPS 部署版)

本项目是 Easy-Net 代理中继的服务端，基于 Node.js 与原生 WebSocket 开发，主要负责接收本地客户端的代理流量并中继到目标主机。它支持多连接凭证隔离校验和实时的流量统计。

---

## 部署前提

在 VPS 上部署该服务，宿主机需要具备以下环境：

1. **Docker** (建议最新版本)
2. **Docker Compose** (或 Docker 自带的 `docker compose` 命令行插件)

---

## 部署与运行步骤

服务端提供了快捷管理脚本 `deploy.sh`，可极大地简化部署流程。

### 第一步：准备文件

将 `server` 文件夹下的所有内容（包括 `server.js`、`package.json`、`Dockerfile`、`docker-compose.yml`、`deploy.sh` 和 `.env.example`）上传到您的 VPS 目标目录中。

### 第二步：配置凭证与端口

为了服务安全性，本服务**无默认连接凭证**。在部署前，您可以通过以下方式进行环境配置：

#### 方式 A：通过 `.env` 配置文件（推荐）

直接复制目录下的 `.env.example` 为 `.env`：

```bash
cp .env.example .env
```

打开 `.env` 文件并编辑其中的环境变量：

```env
# 宿主机映射端口
HOST_PORT=3100

# 客户端连接秘钥白名单，多个凭证以逗号分隔
SECRETS=your-custom-secret-key-1,your-custom-secret-key-2

# 流量统计查询管理员秘钥
ADMIN_KEY=your-admin-stat-key
```

#### 方式 B：通过命令行参数直接启动

在启动时，您可以通过在命令前直接附带环境变量来配置，例如：

```bash
SECRETS="my-secret-key" ADMIN_KEY="my-admin-key" ./deploy.sh start --port 3100
```

---

### 第三步：使用脚本进行服务管理

首先赋予脚本执行权限：

```bash
chmod +x deploy.sh
```

#### 1. 启动服务

如果您已在 `.env` 中配置好了密码和端口，可直接运行：

```bash
./deploy.sh start
```

如果想覆盖 `.env` 中的宿主机映射端口，可以通过 `--port` 参数指定：

```bash
./deploy.sh start --port 3100
```

* **`--port [port]`**：指定服务在宿主机上映射的端口（若未指定，优先使用 `.env` 中的 `HOST_PORT`，其次为默认值 3000）。
* **`-p [project_name]`**：指定 Docker Compose 项目的名称（当需要在一台 VPS 上运行多个代理实例时，非常有用）。

#### 2. 查看服务状态

```bash
./deploy.sh status
```

#### 3. 查看实时运行日志

```bash
./deploy.sh logs
```

#### 4. 重启服务

```bash
./deploy.sh restart
```

#### 5. 停止服务

```bash
./deploy.sh stop
```

---

## 环境变量说明

| 环境变量名 | 说明 | 是否必填 |
| :--- | :--- | :--- |
| `SECRETS` | 客户端连接秘钥白名单，多个凭证以逗号 `,` 分隔。若未配置，**所有客户端连接请求均会被拒绝**。 | **必填** (为保障安全无默认值) |
| `ADMIN_KEY` | 用于访问流量统计的管理员凭证。若未配置，**流量统计接口将安全禁用**。 | 选填 (建议配置) |
| `PORT` | 容器内部 Node.js 服务端监听的端口，默认为 `3000` (由 Docker 内部使用，无需手动修改)。 | 选填 |
| `HOST_PORT` | 映射到宿主机的端口。您可在 `deploy.sh` 启动时通过 `--port` 指定，或修改 `.env` 配置文件。 | 选填 |
| `MONITOR_INTERVAL_SECONDS` | 定时输出监控日志的间隔秒数。默认 `0` 表示关闭；例如设置为 `60` 后每分钟输出一次 RSS、容器内存、连接数与缓冲区指标。 | 选填 |
| `MAX_WS_PAYLOAD_BYTES` | WebSocket 单条消息大小限制，默认 `1048576` (1 MiB)。本地客户端默认分片较小，通常无需修改。 | 选填 |
| `WS_BACKPRESSURE_LIMIT_BYTES` | 单连接 WebSocket 待发送缓冲超过该值时暂停读取目标 TCP，默认 `4194304` (4 MiB)。 | 选填 |
| `WS_BACKPRESSURE_RESUME_BYTES` | 单连接 WebSocket 待发送缓冲低于该值时恢复读取目标 TCP，默认 `2097152` (2 MiB)。 | 选填 |

---

## 使用与接口说明

### 1. 客户端连接接口 `/tunnel`

本接口只接受 WebSocket 协议的升级连接。客户端建立连接时需携带以下 Query 参数：

* `secret`: 对应的访问凭证（必须在 `SECRETS` 列表中）
* `host`: 目标访问的主机地址
* `port`: 目标访问的端口号

**客户端配置示例 (`local-config.json`)：**

```json
{
  "workerHost": "您的VPS的公网IP:映射端口",
  "localPort": 1080,
  "secret": "在SECRETS中配置的密钥之一"
}
```

### 2. 流量统计接口 `/stats`

本接口为 HTTP GET 接口，访问时需要提供正确的管理员查询凭证。

* **访问地址**：`http://您的VPS的公网IP:映射端口/stats?admin_key=您的ADMIN_KEY`
* **响应格式**：返回各凭证当前的活跃连接数及上行、下行流量字节数（**注意：该统计数据存储在内存中，服务重启后将清零**）。

  ```json
  {
    "status": "success",
    "timestamp": "2026-06-06T14:00:00.000Z",
    "runtime": {
      "pid": 1,
      "nodeVersion": "v22.22.1",
      "uptimeSeconds": 86400,
      "limits": {
        "maxWsPayloadBytes": 1048576,
        "wsBackpressureLimitBytes": 4194304,
        "wsBackpressureResumeBytes": 2097152
      },
      "memory": {
        "rss": 67108864,
        "heapTotal": 8388608,
        "heapUsed": 5242880,
        "external": 2097152,
        "arrayBuffers": 1048576
      },
      "containerMemory": {
        "currentBytes": 322961408,
        "limitBytes": 3848290697,
        "stat": {
          "anon": 67108864,
          "file": 251658240,
          "sock": 0
        }
      },
      "connections": {
        "totalConnections": 120,
        "activeConnections": 1,
        "maxActiveConnections": 18,
        "rejectedUpgrades": 3,
        "targetErrors": 2,
        "websocketErrors": 0,
        "deadConnectionsTerminated": 1
      },
      "websocket": {
        "backpressureEvents": 0,
        "totalWsBufferedAmount": 0,
        "maxWsBufferedAmount": 0,
        "maxWsBufferedAmountSeen": 0,
        "pausedTargetReads": 0,
        "resumedTargetReads": 0,
        "openTargetSockets": 1,
        "totalTargetWritableLength": 0
      }
    },
    "stats": {
      "your-custom-secret-key-1": {
        "uploadBytes": 10240,
        "downloadBytes": 20480,
        "activeConnections": 1,
        "totalConnections": 10,
        "failedConnections": 0
      }
    }
  }
  ```

  其中 `runtime.memory.rss` 是 Node 主进程 RSS，`runtime.containerMemory.currentBytes` 对应容器 cgroup 当前内存，通常更接近 `docker stats`。如果 `containerMemory.stat.file` 很高，说明主要是文件缓存；如果 `anon` 或 `memory.rss` 持续增长，才更像应用实际内存增长。
