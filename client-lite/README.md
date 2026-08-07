# Easy-Net Lite

Easy-Net Lite 是一个使用本地网页管理界面、常驻系统托盘的轻量 SOCKS5 客户端，支持：

- Easy-Net WebSocket 隧道。
- SSH 动态代理，效果等同于 `ssh -D`，但不依赖系统 SSH 命令。
- SSH 密码、OpenSSH 私钥和加密私钥。
- Windows Credential Manager 与 macOS Keychain 凭据存储。
- 多代理配置、独立启停、自动启动和系统托盘。
- 独立“测试连接”、本地监听状态与最近远端连接结果。
- 加密分享码，可复制分享并快速导入 WebSocket 或 SSH 配置。
- 启动后自动打开 `http://127.0.0.1:18081` 管理页面。

## 使用

启动程序后会自动打开浏览器管理页。点击“添加 WebSocket”或“添加 SSH”，每项配置会开放一个只监听回环地址的本地 SOCKS5 端口，例如：

```text
127.0.0.1:1080
```

把浏览器或其它应用的 SOCKS5 代理设置成该地址即可。域名会原样交给远端解析。

“本地监听中”只表示 SOCKS5 端口已经启动。可点击配置卡片上的“测试连接”主动校验 WebSocket 地址和密钥或 SSH 认证信息；浏览器实际使用代理时，卡片也会更新最近一次远端连接成功/失败状态和时间，并给出可操作的错误提示。

SSH 首次连接会显示服务器 SHA-256 指纹。请与服务器管理员确认后再信任；如果服务器指纹发生变化，程序会拒绝连接。

每个代理卡片都可以生成 `ENL1.` 开头的加密分享码。分享码会包含完整认证信息和 SSH 服务器指纹，导入时自动选择未被配置占用的本地端口，并默认关闭自动启动。分享码本身属于敏感凭据，任何获得它的人都可以导入并使用，只应发送给可信的人。

关闭网页不会停止代理。可通过托盘菜单再次打开管理页；请通过托盘菜单“退出程序”完全关闭。

## 配置与凭据

普通配置文件：

- Windows：`%AppData%\Easy-Net Lite\config.json`
- macOS：`~/Library/Application Support/Easy-Net Lite/config.json`

运行日志保存在同一目录的 `easy-net-lite.log`。

WebSocket 密钥、SSH 密码和私钥口令不会写入 JSON，而是保存在系统凭据库。编辑配置时密钥框留空会继续使用原密钥；如果服务端密钥已变更，必须重新填写。网页选择的私钥会复制到应用专用 `keys` 目录并设置为仅当前用户可读，配置中只记录该副本路径。

## 开发验证

```powershell
go test ./...
go vet ./...
```

当前版本由根目录下的 `VERSION` 文件统一管理。正式构建会把该版本写入程序、网页管理界面和 macOS 应用信息；命令行也可以使用 `--version` 查看。

## Windows 构建

只需要项目指定的 Go 1.26.5 工具链，不需要 GCC、Node.js 或 WebView。执行：

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

输出：`dist/Easy-Net Lite.app`。Intel 和 Apple Silicon 应分别在对应构建机上构建，最低支持 macOS 12.0；构建脚本会同时校验应用清单和二进制的最低系统版本。面向外部分发时还需要 Developer ID 签名和 Apple 公证。

## GitHub Release

推送 `client-lite-v<版本号>` 格式的 Git 标签时，GitHub Actions 会构建 Windows x64、macOS arm64 和 macOS x64，校验标签与 `VERSION` 一致，然后自动创建 Release、上传三个程序包和 `SHA256SUMS.txt`。

例如发布 `VERSION` 中的 `0.1.3`：

```bash
git tag -a client-lite-v0.1.3 -m "Easy-Net Lite v0.1.3"
git push origin client-lite-v0.1.3
```

普通推送到 `main` 只生成 Actions 构建产物，不会创建 Release。

## 当前协议边界

- 本地入口为免认证 SOCKS5，因此强制只监听 `127.0.0.1` 或 `::1`。
- 仅支持 SOCKS5 `CONNECT` 和 TCP，不支持 UDP ASSOCIATE。
- WebSocket 默认通过 `Authorization`、`X-Target-Host` 和 `X-Target-Port` 请求头传递连接信息，不在 URL 中携带密钥；旧服务端可在配置中显式启用查询参数兼容模式。
- `wss://` 会正常校验证书；`ws://` 默认被拒绝，只有在配置中显式允许后才能使用。
