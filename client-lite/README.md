# Easy-Net Lite

Easy-Net Lite 是一个使用本地网页管理界面、常驻系统托盘的轻量 SOCKS5 客户端，支持：

- Easy-Net WebSocket 隧道。
- SSH 动态代理，效果等同于 `ssh -D`，但不依赖系统 SSH 命令。
- SSH 密码、OpenSSH 私钥和加密私钥。
- Windows Credential Manager 与 macOS Keychain 凭据存储。
- 多代理配置、独立启停、自动启动和系统托盘。
- 启动后自动打开 `http://127.0.0.1:18081` 管理页面。

## 使用

启动程序后会自动打开浏览器管理页。点击“添加 WebSocket”或“添加 SSH”，每项配置会开放一个只监听回环地址的本地 SOCKS5 端口，例如：

```text
127.0.0.1:1080
```

把浏览器或其它应用的 SOCKS5 代理设置成该地址即可。域名会原样交给远端解析。

SSH 首次连接会显示服务器 SHA-256 指纹。请与服务器管理员确认后再信任；如果服务器指纹发生变化，程序会拒绝连接。

关闭网页不会停止代理。可通过托盘菜单再次打开管理页；请通过托盘菜单“退出程序”完全关闭。

## 配置与凭据

普通配置文件：

- Windows：`%AppData%\Easy-Net Lite\config.json`
- macOS：`~/Library/Application Support/Easy-Net Lite/config.json`

运行日志保存在同一目录的 `easy-net-lite.log`。

WebSocket 密钥、SSH 密码和私钥口令不会写入 JSON，而是保存在系统凭据库。网页选择的私钥会复制到应用专用 `keys` 目录并设置为仅当前用户可读，配置中只记录该副本路径。

## 开发验证

```powershell
go test ./...
go vet ./...
```

## Windows 构建

只需要 Go 1.23 或更高版本，不需要 GCC、Node.js 或 WebView。执行：

```powershell
.\scripts\build-windows.ps1
```

输出：`dist\Easy-Net-Lite.exe`。

## macOS 构建

在对应架构的 Mac 上安装 Go 和 Xcode Command Line Tools，然后执行：

```bash
chmod +x scripts/build-macos.sh
./scripts/build-macos.sh
```

输出：`dist/Easy-Net Lite.app`。Intel 和 Apple Silicon 应分别在对应构建机上构建；面向外部分发时还需要 Developer ID 签名和 Apple 公证。

## 当前协议边界

- 本地入口为免认证 SOCKS5，因此强制只监听 `127.0.0.1` 或 `::1`。
- 仅支持 SOCKS5 `CONNECT` 和 TCP，不支持 UDP ASSOCIATE。
- WebSocket 兼容现有 Easy-Net `/tunnel?secret=&host=&port=` 服务端协议，同时也发送 `Authorization: Bearer` 请求头，便于服务端后续迁移到不在 URL 中携带密钥的协议。
- `wss://` 会正常校验证书；如使用 `ws://`，传输内容和密钥不会得到 TLS 保护。
