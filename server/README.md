# Easy-Net WebSocket 代理中继服务端

Easy-Net 服务端基于 Node.js 和 WebSocket，负责接收本地客户端代理流量并中继到目标主机。当前版本内置管理端、用户端和本地 SQLite 数据库，可以在浏览器里维护用户、连接密钥、流量额度和客户端配置。

## 主要功能

- 管理端：`/admin`
  - 管理员登录、修改管理员密码。
  - 登录失败锁定，默认 15 分钟内连续 5 次失败会临时锁定。
  - 创建、编辑、删除用户。
  - 设置用户每日和月周期流量上限。
  - 设置用户名、密码、昵称、连接密钥和管理员备注。
  - 重置用户当日流量。
  - 下载指定用户客户端配置。
  - 查看服务运行统计和每日流量统计。
  - 设置客户端配置中使用的服务器域名和本地端口。
- 用户端：`/user`
  - 使用管理端创建的账号登录。
  - 查看当天和月周期内的使用情况。
  - 修改自己的登录密码和连接密钥。
  - 下载自己的客户端配置。
- 代理连接：`/tunnel`
  - 客户端连接密钥从数据库用户读取，不再依赖 `SECRETS` 白名单。
  - 用户被停用或超过每日/月周期额度后会拒绝或关闭连接。

## 本地数据库

服务使用 `sql.js` 提供 SQLite 数据库能力，默认数据库文件为：

```text
server/data/easy-net.sqlite
```

Docker Compose 默认会挂载：

```text
./data:/app/data
```

请保留 `data` 目录，避免重启或重建容器后丢失用户、密码、设置和流量统计。

## 环境变量

| 环境变量名 | 说明 | 默认值 |
| :--- | :--- | :--- |
| `HOST_PORT` | Docker 映射到宿主机的端口。 | `3100` |
| `PORT` | 容器内部 Node.js 监听端口。 | `3000` |
| `DATA_DIR` | 数据库目录。 | `/app/data` |
| `CONTEXT_PATH` | 服务统一路径前缀，例如 `/easy-net`。 | 空 |
| `ADMIN_PASSWORD` | 初始管理员密码，仅首次创建数据库时生效。 | 未设置时会随机生成并打印到日志 |
| `CLIENT_WS_URL` | 客户端配置里的完整 WebSocket 地址。也可在管理端修改。 | 空，下载配置时按当前请求推断 |
| `CLIENT_HOST` | 旧版兼容：客户端配置里的服务器域名或 `host:port`。 | 空 |
| `CLIENT_LOCAL_PORT` | 客户端配置里的本地 SOCKS 端口。 | `1080` |
| `LOGIN_MAX_FAILURES` | 登录失败锁定阈值。 | `5` |
| `LOGIN_LOCK_MINUTES` | 触发锁定后的锁定分钟数。 | `15` |
| `SECRETS` | 旧版兼容。首次启动时会迁移为 `legacy_x` 用户，后续推荐在管理端维护。 | 空 |
| `ADMIN_KEY` | 旧版兼容。仍可访问 `/stats?admin_key=...`。 | 空 |
| `MONITOR_INTERVAL_SECONDS` | 定时输出监控日志间隔，`0` 表示关闭。 | `0` |
| `MAX_WS_PAYLOAD_BYTES` | WebSocket 单条消息大小限制。 | `1048576` |
| `WS_BACKPRESSURE_LIMIT_BYTES` | 单连接 WebSocket 待发送缓冲暂停阈值。 | `4194304` |
| `WS_BACKPRESSURE_RESUME_BYTES` | 单连接 WebSocket 待发送缓冲恢复阈值。 | `2097152` |

## 部署步骤

1. 复制环境变量示例：

```bash
cp .env.example .env
```

2. 修改 `.env`，至少设置：

```env
HOST_PORT=3100
CONTEXT_PATH=/easy-net
ADMIN_PASSWORD=change-this-admin-password
CLIENT_WS_URL=wss://proxy.example.com/easy-net/tunnel
CLIENT_LOCAL_PORT=1080
```

3. 启动服务：

```bash
chmod +x deploy.sh
./deploy.sh start
```

4. 打开管理端：

```text
http://你的服务器:3100/easy-net/admin
```

首次登录使用 `.env` 中的 `ADMIN_PASSWORD`。登录后请尽快在“管理员密码”里修改。

## 客户端配置

管理端或用户端下载的配置格式如下：

```json
{
  "serverWsUrl": "wss://proxy.example.com/easy-net/tunnel",
  "workerHost": "proxy.example.com:3100",
  "localPort": 1080,
  "secret": "用户自己的连接密钥"
}
```

`serverWsUrl` 和 `localPort` 来自管理端“系统设置”，`secret` 来自当前用户。`workerHost` 仅用于兼容旧客户端。

## 旧版接口

### WebSocket 代理接口 `/tunnel`

客户端仍然使用 WebSocket 连接：

```text
ws://服务器/easy-net/tunnel?secret=用户连接密钥&host=目标主机&port=目标端口
```

### 统计接口 `/stats`

如果配置了 `ADMIN_KEY`，旧统计接口仍可使用：

```text
http://服务器/stats?admin_key=你的ADMIN_KEY
```

新管理端推荐使用 `/admin` 查看统计信息。
