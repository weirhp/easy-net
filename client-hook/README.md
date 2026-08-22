# Easy-Net Hook

Easy-Net Hook 是 Windows 应用级代理工具，与常驻托盘的 Easy-Net Lite 一起发布。

- 普通 Win32 程序通过 Microsoft Detours Hook Winsock TCP API。
- ChatGPT、Cursor、Antigravity、Chrome 和 Edge 使用原生 SOCKS5 参数，并按需用 Hook 覆盖辅助进程。
- 微信和运行中应用仅使用 WinDivert 按进程名接管 TCP 与 UDP，不再包含 TUN/sing-box 后端。
- Clash 订阅由 Easy-Net Lite 调用独立目录中的 Mihomo；Mihomo 不参与微信或通用 WinDivert 接管。

## 发布包结构

x64 完整包：

```text
Easy-Net-Hook-x64\
  Easy-Net-Lite.exe
  easy-net-hook.exe
  easy-net-hook.dll
  windivert\
    easy-net-windivert.exe
    ProxyBridgeCore.dll
    WinDivert.dll
    ...
  mihomo\
    mihomo.exe
    LICENSE.txt
    VERSION.txt
  THIRD-PARTY-LICENSES\
```

发布包不包含 `tun` 目录、`sing-box.exe` 或 `libcronet.dll`。不要把 `mihomo.exe` 移到程序根目录；Lite 默认只查找 `mihomo\mihomo.exe`，也可通过 `EASY_NET_MIHOMO` 指定其他路径。

Win32 包用于 32 位 Hook 目标，不包含只支持 x64 的 WinDivert 组件和 Mihomo。

## 构建

需要 Visual Studio 2022 C++ Build Tools、Windows SDK、CMake 和 Go：

```powershell
cd client-hook
.\scripts\build-windows.ps1 -Architecture x64 -Configuration Release
```

本地构建时如需同时下载 Mihomo：

```powershell
.\scripts\build-windows.ps1 -Architecture x64 -Configuration Release -WithMihomo
```

GitHub Actions 会自动为 x64 包下载固定版本的 Mihomo，并验证 SHA-256；详见 [GITHUB_BUILD_GUIDE.md](GITHUB_BUILD_GUIDE.md)。

## 推荐使用方式

日常直接运行 `Easy-Net-Lite.exe`：在“网络代理管理”添加代理并选择默认项，再在“应用代理管理”添加进程。第一次开启 WinDivert 时接受 UAC 提权；Lite 自身无需始终以管理员身份运行。

共享 WinDivert 会把多个应用规则合并到一个引擎中。引擎异常退出时 Lite 会显示状态并尝试重启；代理不可用时匹配流量保持阻断，避免静默直连泄漏。

## 命令行

### 微信

启动新的微信：

```powershell
.\easy-net-hook.exe --proxy 127.0.0.1:1082 --wechat --udp-mode auto --detach
```

接管已经运行的微信：

```powershell
.\easy-net-hook.exe --proxy 127.0.0.1:1082 --wechat-existing --udp-mode auto --detach
```

微信固定使用 WinDivert，匹配 `WeChat.exe`、`Weixin.exe`、`WeChatApp.exe`、`WeChatAppEx.exe`、`WeChatBrowser.exe` 等微信进程。接管只影响新连接，已建立连接需要重新打开页面、重新登录或重启应用后才能进入新路由。

### 通用 WinDivert

```powershell
.\easy-net-hook.exe `
  --proxy 127.0.0.1:1082 `
  --windivert `
  --windivert-processes "app.exe;helper.exe" `
  --udp-mode auto `
  --detach `
  -- "C:\Path\app.exe"
```

网络参数：

- `--udp-mode auto|proxy|block|direct`：`auto` 检测 SOCKS5 UDP ASSOCIATE，支持则代理，否则阻断。
- `--bypass CIDR`：指定直连网段，可重复或用逗号分隔。
- `--network-debug-log`：临时记录逐连接诊断信息；日志限制为 8 MiB。
- `--windivert-engine PATH`：指定 `easy-net-windivert.exe`。

`--dns` 不适用于 WinDivert 模式；WinDivert 保留 Windows 的系统 DNS 行为。

### 通用 Hook

```powershell
.\easy-net-hook.exe --proxy 127.0.0.1:1082 -- "C:\Path\app.exe" --app-arg
.\easy-net-hook.exe --proxy 127.0.0.1:1082 --pid 1234
```

Hook 模式主要覆盖 TCP。外部 UDP 默认阻断；只有明确接受泄漏风险时才使用 `--allow-udp-direct`。

### ChatGPT、Cursor 与浏览器

```powershell
.\easy-net-hook.exe --proxy 127.0.0.1:1082 --chatgpt-app --detach
.\easy-net-hook.exe --proxy 127.0.0.1:1082 --cursor --detach
.\easy-net-hook.exe --proxy 127.0.0.1:1082 --chrome --browser-isolated --detach
.\easy-net-hook.exe --proxy 127.0.0.1:1082 --edge --browser-isolated --detach
```

对应可选路径参数为 `--cursor-path`、`--browser-path`。Antigravity 使用 `--antigravity` 和可选的 `--antigravity-path`。

## 状态与日志

- 微信日志：`%LOCALAPPDATA%\EasyNetHook\WinDivert\wechat-windivert.log`
- 微信状态：`%LOCALAPPDATA%\EasyNetHook\WinDivert\wechat-status.json`
- 通用应用：`%LOCALAPPDATA%\EasyNetHook\WinDivert\Apps\<应用>\`
- Lite 共享引擎：Lite 配置目录中的 `shared-windivert-status.json` 与 `shared-windivert.log`

查看微信状态：

```powershell
.\easy-net-hook.exe --wechat-status
```

`healthy` 表示 WinDivert 正常且 SOCKS5 可达；`proxy-unavailable` 表示代理失联、匹配流量仍被阻断；`restarting` 或 `restart-failed` 表示引擎正在重启或重启失败。

## 当前边界

- WinDivert 需要 x64 Windows 和管理员授权，且主要按进程名匹配；同名进程都会命中。
- 接管运行中应用不会迁移已有 TCP/UDP 会话，只影响后续新连接。
- 应用内置 DoH、特殊网络驱动、内核协议或不经过常规 Winsock/WinDivert 路径的流量可能需要单独验证。
- Mihomo 只负责 Lite 的 Clash 订阅节点；停止 Mihomo 会影响 Clash 订阅，不影响 Easy-Net、SSH、外部代理或已经运行的 WinDivert 引擎。

## 许可证

Detours、ProxyBridge、WinDivert 和 Mihomo 的许可证随发布包分发。升级第三方依赖时应同步更新固定版本、SHA-256 和许可证文件。
