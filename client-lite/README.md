# Easy-Net Lite

Easy-Net Lite 是一个使用本地网页管理界面、常驻系统托盘的轻量混合代理客户端，支持：

- Easy-Net WebSocket 隧道。
- WebSocket 配置支持 SOCKS5 `UDP ASSOCIATE`，可承载 DNS、QUIC 和应用 UDP 数据报。
- SSH 动态代理，效果等同于 `ssh -D`，但不依赖系统 SSH 命令。
- SSH 密码、OpenSSH 私钥和加密私钥。
- Windows Credential Manager 与 macOS Keychain 凭据存储。
- 多代理配置、独立启停、自动启动和系统托盘。
- 每个配置可选“内网目标本机直连”，让局域网和 VPN 地址绕过远端代理。
- 每个配置可选“中国大陆 IP 直连”，按内置 APNIC CN 地址表让国内目标走本机网络、其他目标走代理。
- 独立“测试连接”、本地监听状态与最近远端连接结果。
- 加密分享码，可复制分享并快速导入 WebSocket 或 SSH 配置。
- Windows 上可把 Lite 自建的 WebSocket/SSH 代理、已运行的 Clash/v2rayN 外部 SOCKS5，以及导入的 Clash 订阅节点，统一放在「网络代理管理」，并设置唯一默认代理。
- 可导入 Clash / Mihomo 订阅：每个订阅一个 Tab，选中节点后由本机 `mihomo` 启动本地 SOCKS5，并可设为默认代理供应用继承。
- 「应用代理管理」维护共享 WinDivert 接管规则，可从运行进程批量添加，或快速导入 Cursor、ChatGPT、Antigravity、Claude Code、Chrome、Edge。
- 启动后自动打开 `http://127.0.0.1:18081` 管理页面。

## 使用

启动程序后会自动打开浏览器管理页。点击“添加 WebSocket”或“添加 SSH”，每项配置会开放一个只监听回环地址的本地混合代理端口，同时接受 SOCKS5 和 HTTP CONNECT，例如：

```text
127.0.0.1:1080
```

可以把浏览器或其它应用的 SOCKS5 或 HTTP 代理设置成该地址。默认情况下，SOCKS5 域名和 HTTP CONNECT 目标主机会原样交给远端解析。WebSocket 配置同时接受 SOCKS5 UDP；SSH 配置的公网代理仍然只有 TCP，因为标准 SSH 动态转发没有 UDP 通道。

网络代理页用内层 Tab 区分来源。默认 Tab「手动添加」继续管理 WebSocket、SSH 和外部 SOCKS5。点击「导入 Clash 订阅」填写名称和 URL 后，会新增一个同名 Tab，列出订阅里的节点。启动某个节点会在本机拉起 `mihomo`，监听该订阅自己的回环端口；同一订阅同时只运行一个节点。节点也可以设为全局唯一的默认代理，未单独指定代理的应用会继承它。

Lite 不会自动下载 `mihomo`。请把 `mihomo.exe`（macOS 为 `mihomo`）放到 Easy-Net Lite 同一目录、`mihomo` 子目录，或配置目录下的 `mihomo` 子目录；也可以设置环境变量 `EASY_NET_MIHOMO` 指向完整路径。找不到程序时，启动节点会给出明确错误。订阅内容保存在同一配置目录的 `subscriptions.json`。

新建配置默认勾选“局域网与私有地址直连”。RFC 1918 私网（`10/8`、`172.16/12`、`192.168/16`）、回环、链路本地、CGNAT `100.64/10` 和 IPv6 ULA 会使用本机路由，不再发给 Easy-Net 服务端。例如 `192.168.0.252:8311` 会直接通过当前局域网或 VPN 连接。

新建配置默认同时勾选“中国大陆 IP 直连（国外 IP 走代理）”。SOCKS5 TCP、HTTP CONNECT 和 UDP 目标会使用内置的 APNIC 中国大陆 IPv4/IPv6 地址表分流：IP 命中地址表时走本机网络，未命中时走配置的 WebSocket/SSH 代理。域名会先用本机 DNS 解析，只有全部解析结果都命中已启用的直连规则时才直连，避免混合解析结果泄漏。这个功能按 IP 而不是域名分类，CDN 和跨境服务的实际线路取决于本机 DNS 返回的地址。修改正在运行的配置后需要停止再启动该配置才会生效。

“本地监听中”只表示混合代理端口已经启动。可点击配置卡片上的“测试连接”主动校验 WebSocket 地址和密钥或 SSH 认证信息；浏览器实际使用代理时，卡片也会更新最近一次远端连接成功/失败状态和时间，并给出可操作的错误提示。

SSH 首次连接会显示服务器 SHA-256 指纹。请与服务器管理员确认后再信任；如果服务器指纹发生变化，程序会拒绝连接。

每个代理卡片都可以生成 `ENL1.` 开头的加密分享码。分享码会包含完整认证信息和 SSH 服务器指纹，导入时自动避开已有配置以及其他程序正在监听的本地端口，并默认关闭自动启动。Windows 上可在 Lite 的「应用」页用这些配置启动 ChatGPT、Cursor、微信等。分享码本身属于敏感凭据，任何获得它的人都可以导入并使用，只应发送给可信的人。

关闭网页不会停止代理。可通过托盘菜单再次打开管理页；请通过托盘菜单“退出程序”完全关闭。

## 应用启动（Windows）

Windows 管理页有「代理」和「应用」两个标签。代理页继续管理 SOCKS5/HTTP 配置；应用页保存启动入口，并在启动前自动打开所选配置的本地端口，然后拉起同目录中的 `easy-net-hook.exe`。

- 分享码仍然只在「代理」页导入。应用入口可以选择 Lite 配置，也可手动填写另一个已运行的 SOCKS5（例如 `127.0.0.1:10808`）。手动代理不由 Lite 启停或保存认证口令。
- 「通用 Hook」通过 DLL 注入代理 TCP，资源占用低，但不覆盖 UDP，并受进程架构和保护策略限制。
- Chrome/Edge 默认使用 Chromium 原生 SOCKS5 参数启动。建议保留“独立代理配置目录”，否则已经运行的浏览器可能接收新窗口并忽略本次代理参数。
- 「从运行进程添加」会列出当前可访问的 EXE，并支持多选；应用名称直接使用进程文件名。保存后会自动刷新共享规则，让已运行和以后启动的同名进程的新连接生效。
- 「通用 WinDivert」按进程名覆盖 TCP+UDP，需要完整 x64 包和管理员授权。如果程序由辅助 EXE 发起网络，需在入口中补充这些进程名。同名的全部进程都会匹配，已建立连接不会迁移，需要重新打开页面或让应用重新连接。
- Lite 可以保持普通用户权限；首次保存接管规则时会单独弹出 Windows UAC。Lite 会等待授权和引擎就绪，用户取消授权或驱动启动失败时会在管理页显示“配置已保存但接管未刷新”。
- 应用没有单独指定代理时会实时继承网络代理页的默认代理；切换默认代理会同步刷新共享规则。外部代理只由 Lite 引用，Lite 不会启动或关闭 Clash、v2rayN。导入的 Clash 订阅节点由 Lite 通过 `mihomo` 启停，设为默认后同样可被应用继承。
- 应用列表不再直接启动程序。「桌面快捷方式」指向 `Easy-Net-Lite.exe --launch-entry <ID>` 并内嵌一份不含密码的恢复快照；入口被误删后仍可自动恢复。ChatGPT/Cursor/Antigravity/Chrome/Edge 使用各自的原生 SOCKS5 启动方式，Claude Code 和普通程序使用 Hook；共享 WinDivert 会显式直连该应用所用的 SOCKS5 服务器端点，防止快捷启动流量被二次代理。启动前仍会检查代理可用性。
- 请把 `easy-net-hook.exe`、Hook DLL、`windivert` 和 `mihomo` 目录与 Lite 保持在发布包原有结构中，或设置环境变量 `EASY_NET_HOOK`。
- macOS 不显示「应用」标签。首次在 Windows 上打开 Lite 时，如果存在旧的 `%LOCALAPPDATA%\EasyNetHook\launcher-entries.tsv`，会按本地监听地址匹配并导入到 `%AppData%\Easy-Net Lite\launches.json`。

双击 `easy-net-hook.exe` 或传入 `--gui` 会打开 Lite 管理页的 `#apps`。

## 配置与凭据

普通配置文件：

- Windows：`%AppData%\Easy-Net Lite\config.json`
- macOS：`~/Library/Application Support/Easy-Net Lite/config.json`

Windows 应用启动入口保存在同一目录的 `launches.json`。Clash 订阅保存在同一目录的 `subscriptions.json`。运行日志保存在同一目录的 `easy-net-lite.log`。

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

- 本地入口为免认证 SOCKS5/HTTP CONNECT 混合代理，因此强制只监听 `127.0.0.1` 或 `::1`。
- 支持 SOCKS5 `CONNECT`、SOCKS5 `UDP ASSOCIATE` 和 HTTP `CONNECT`；不支持普通明文 HTTP 转发。
- 公网 UDP 代理只适用于 WebSocket 配置，并要求服务端支持 `X-Easy-Net-Protocol: 3`。启用内网直连后，SSH 配置也可通过 UDP ASSOCIATE 直连内网 UDP 目标，但公网 UDP 仍不会通过 SSH 转发。旧 WebSocket 服务端会返回明确的版本错误。
- 一个 UDP ASSOCIATE 对应一条独立 WebSocket 和一个服务端 UDP 会话，生命周期跟随 SOCKS5 TCP 控制连接。客户端锁定首次合法 UDP 数据报的本地源 IP/端口，防止其他本机进程借用该 relay。
- 保留域名、IPv4/IPv6 和 UDP 报文边界；不支持 SOCKS5 UDP 分片（`FRAG != 0`），此类数据报会被丢弃。单个 UDP 负载最大 65507 字节。
- TCP WebSocket 默认通过 `Authorization`、`X-Target-Host` 和 `X-Target-Port` 请求头传递连接信息；UDP 会话使用 `X-Easy-Net-Network: udp` 和协议版本 3，目标地址包含在每条二进制数据报帧中。连接密钥不放在 URL；旧 TCP 服务端可在配置中显式启用查询参数兼容模式。
- `wss://` 会正常校验证书；`ws://` 默认被拒绝，只有在配置中显式允许后才能使用。
