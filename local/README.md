# Easy-Net Go Client - SOCKS5 加密代理客户端

本项目是 Easy-Net SOCKS5 代理客户端的 Go 语言实现版本。它可以作为 Node.js 客户端的直接替代品。

## 准备工作

请确保您的系统上已安装了 [Go 编译器](https://go.dev/)（推荐版本 1.21 或更高）。

## 目录结构

* `main.go` - Go 客户端核心代码。
* `go.mod` - Go 模块管理文件。
* `local-config.json` - 客户端配置文件。

## 配置说明

配置文件为 `local-config.json`，格式与 Node.js 客户端完全一致：

```json
{
  "workerHost": "部署服务的域名",
  "localPort": 1080,
  "secret": "easy-net-secret-key-12345"
}
```

* `workerHost`: Cloudflare Worker 绑定的域名，或者 AWS CloudFront 分发域名。
* `localPort`: 本地监听端口，客户端将开启一个 SOCKS5 服务。
* `secret`: 服务端预共享密钥，用于底层隧道安全认证。

## 运行与编译说明

在 `proxy-go` 目录中：

### 1. 下载外部依赖

由于我们使用了最成熟的 Gorilla WebSocket 库，运行以下命令获取依赖：

```bash
go mod tidy
```

### 2. 直接运行

如果您只想快速运行客户端而无需编译：

```bash
go run main.go
```

### 3. 编译成独立二进制程序

如果您希望编译成一个独立的、无任何依赖的轻量级可执行文件：

```bash
# 编译出当前系统的二进制文件
go build -o easy-net-client.exe main.go

# 运行编译出来的程序
./easy-net-client.exe
```

## 代码特性

1. **防 403 阻断**：已内置浏览器 User-Agent 标头参数，彻底解决空 User-Agent 导致的 AWS CloudFront WAF 拦截阻断。
2. **并发安全与高性能**：利用 Go 原生并发特性的 Goroutine 和 `sync.WaitGroup` 结构，高效、双向零拷贝转发底层 TCP 与 WebSocket 流量。
