# Easy-Net Hook

Easy-Net Hook 是一个轻量 Windows 应用代理器。普通 Win32 程序通过 Microsoft Detours Hook Winsock API，把 TCP `connect`、`WSAConnect` 和 `ConnectEx` 改写为 SOCKS5 `CONNECT`；ChatGPT 和 Antigravity IDE 使用更稳定的原生代理模式；微信可选择按进程 TUN 或 WinDivert 模式覆盖 TCP、UDP/QUIC 和辅助进程。

日常使用请先启动 Easy-Net Lite（托盘 + `http://127.0.0.1:18081`）。在 Lite 的「代理」页导入分享码或添加配置，在「应用」页启动 ChatGPT、Cursor、微信或通用程序。双击 `easy-net-hook.exe` 会打开 Lite 的应用页，而不是旧的 Win32 窗口。命令行仍可对单个进程使用 `--proxy 127.0.0.1:1080`。仅 WinDivert 模式会在运行期间加载随包提供的签名驱动，其他模式不加载该驱动。

> 这仍不是 Proxifier 的完整替代品。请先阅读“当前边界”，尤其是运行中进程、UDP 和 DNS 部分。

## 构建

需要 Visual Studio 2022，并安装：

- Desktop development with C++。
- MSVC v143 工具集。
- Windows 10/11 SDK。
- CMake tools for Windows。
- Go 1.26（仅在仍需编译过渡组件 `easy-net-engine.exe` 时需要；应用启动已改由 Easy-Net Lite 提供本地 SOCKS5）。

构建 x64 或 Win32 时如果本机已安装 Go，还会同时编译对应架构的 `easy-net-engine.exe`。只安装命令行构建工具时，可以在管理员 PowerShell 中执行：

```powershell
winget install --id Microsoft.VisualStudio.2022.BuildTools -e `
  --override "--wait --passive --add Microsoft.VisualStudio.Workload.VCTools --includeRecommended" `
  --accept-source-agreements --accept-package-agreements
```

安装完成后重新打开 PowerShell。构建脚本会先查找 PATH 中的 CMake，再自动查找 Visual Studio 自带的 CMake，因此不要求手动运行 `vcvars64.bat`。

构建 x64：

```powershell
cd D:\work\me-pro\easy-net\client-hook
.\scripts\build-windows.ps1 -Architecture x64
```

构建 32 位版本：

```powershell
cd D:\work\me-pro\easy-net\client-hook
.\scripts\build-windows.ps1 -Architecture Win32
```

x64 Release 输出目录：

```text
D:\work\me-pro\easy-net\client-hook\build-x64\Release
```

首次配置会下载固定版本的 Detours；x64 还会下载固定提交的 ProxyBridge 源码和经过 SHA-256 校验的 WinDivert 2.2.2 SDK。输出目录包含：

```text
easy-net-hook.exe
easy-net-hook.dll
Easy-Net-Lite.exe
easy-net-engine.exe
THIRD-PARTY-LICENSES\Detours-LICENSE.md
windivert\easy-net-windivert.exe  （仅 x64）
windivert\ProxyBridgeCore.dll     （仅 x64）
windivert\WinDivert.dll           （仅 x64）
windivert\WinDivert64.sys         （仅 x64）
```

启动器、DLL 和目标程序的架构必须相同。64 位程序使用 x64 包，32 位程序使用 Win32 包。

## 使用

### 图形界面

直接双击 `easy-net-hook.exe`，或者执行：

```powershell
.\easy-net-hook.exe --gui
```

会打开 Easy-Net Lite 管理页的「应用」标签（`http://127.0.0.1:18081/#apps`）。如果 Lite 还没运行，会尝试启动同一目录中的 `Easy-Net-Lite.exe`。GitHub Actions 生成的 Hook 组合包已包含完整 Lite，解压后不要拆分这些文件。

旧的 Win32 启动器窗口仍可用：

```powershell
.\easy-net-hook.exe --legacy-gui
```

新的启动入口、桌面快捷方式和分享码导入请使用 Lite 网页。旧的 `%LOCALAPPDATA%\EasyNetHook\launcher-entries.tsv` 会在 Lite 首次启动时尝试迁移到 `%AppData%\Easy-Net Lite\launches.json`。

### 本地代理

请在 Easy-Net Lite 中导入 `ENL1.` 分享码或手动添加 WebSocket/SSH 配置。应用入口只选择这些配置，启动时由 Lite 打开对应的回环 SOCKS5，再拉起 Hook。不要再把分享码写进 Hook 命令行，除非你明确使用 `--share-code` 的旧路径。

过渡组件 `easy-net-engine.exe` 不是长期控制面；常驻进程应始终是 Easy-Net Lite（`:18081`）。

「桌面快捷方式」由 Lite 应用页创建，目标为 `Easy-Net-Lite.exe --launch-entry <ID>`。旧快捷方式若仍指向 `easy-net-hook.exe --launch-entry`，会先询问 Lite；找不到对应入口时才回退到原来的 tsv。

命令行同样可以在已经有本地 SOCKS5 时直接启动应用：

```powershell
.\easy-net-hook.exe --proxy 127.0.0.1:1080 --chatgpt-app --detach
```

`--share-code` 仍可用于一次性导入并启动，但日常请改在 Lite 的「代理」页导入。分享码包含代理认证信息，出现在进程命令行中时请注意本机是否有人能看到。

ChatGPT、Cursor、微信等场景的行为与以前相同：ChatGPT 使用隔离用户目录；Antigravity 默认复用桌面登录状态，也可使用独立配置；微信可接管已运行进程。旧 Win32 窗口里的入口文件仍是 `%LOCALAPPDATA%\EasyNetHook\launcher-entries.tsv`，仅作为迁移来源和 `--legacy-gui` / 旧快捷方式回退。

### 通用 Hook 与通用 WinDivert

Lite 应用页提供两种通用场景：

- 通用 Hook：将 `easy-net-hook.dll` 注入新进程，覆盖 Winsock TCP，可按需继承到子进程。它较轻量，但不能绕过 AppContainer/CIG，也不代理 UDP。
- 通用 WinDivert：无需 DLL 注入，按 EXE 进程名代理 TCP+UDP。它只在 x64-TUN 包可用，首次创建引擎时需要 UAC 管理员授权。从 Easy-Net Lite 启动时，所有通用 WinDivert 入口会合并为一份多应用、多代理规则并共用一个引擎；后续应用以普通权限启动，不再重复请求 UAC。应用退出不会停止共享引擎，退出 Lite 后才会释放。引擎异常退出会自动重启，入口配置变化时自动重载；SOCKS5 失联时保持 fail-closed，阻止匹配流量直连泄漏。

通用 WinDivert 命令行示例：

```powershell
.\easy-net-hook.exe --proxy 127.0.0.1:10808 --windivert `
  --windivert-processes "app.exe;helper.exe" --tun-udp auto --detach `
  -- "D:\Apps\app.exe"
```

规则会影响当前系统上所有同名进程。单实例程序可能把新启动请求交给旧进程；监视器会按配置的主/辅助进程名继续跟踪，直到这些进程全部退出。

### 微信（TUN）

微信同时使用 TCP、UDP/QUIC 和多个辅助进程，推荐使用 x64 构建产物中的 `Easy-Net-Hook-x64-TUN` 包。它额外包含独立的 sing-box TUN 引擎；基础 x64/Win32 包仍保持轻量，不捆绑约 55 MB 的可选组件。

启动前从托盘完全退出微信，然后执行：

```powershell
.\easy-net-hook.exe `
  --proxy 127.0.0.1:10808 `
  --wechat `
  --detach
```

程序会自动查找 `Weixin.exe` 或 `WeChat.exe`，找不到时可以明确指定：

```powershell
.\easy-net-hook.exe `
  --proxy 127.0.0.1:10808 `
  --wechat `
  --wechat-path "D:\soft\Tencent\Weixin\Weixin.exe" `
  --detach
```

如果微信已经运行，使用接管模式；该模式不启动第二个实例，也不会向微信注入 DLL：

```powershell
.\easy-net-hook.exe `
  --proxy 127.0.0.1:1082 `
  --wechat-existing `
  --detach
```

接管后只有新建连接能够可靠地进入 TUN。建议在微信内切换页面、重新打开小程序或重新登录以触发重连。`--wechat-existing` 不接受 `--wechat-path` 和 `--` 后的启动参数；未找到正在运行的 `Weixin.exe`、`WeChat.exe` 或 `xwechat.exe` 时会直接报错。微信完全退出后，生命周期监视器同样会停止 TUN。

TUN 需要管理员权限，启动器会触发一次 Windows UAC。TUN 运行期间，微信及 `WeChatApp.exe`、`WeChatAppEx.exe`、`WeChatBrowser.exe`、`WeChatOCR.exe`、`WeChatPlayer.exe` 等辅助进程匹配 SOCKS5 出站，其他程序通过同一 TUN 分类后直连。微信完全退出后，生命周期监视器会自动停止 TUN 引擎并删除路由。

为降低服务器上的 CPU 占用，TUN 默认使用 Windows `system` 网络栈，并在路由层绕过常见内网、链路本地和 CGNAT 网段。其他高流量程序即使最终选择 `direct`，如果目标不在绕过列表中，数据包仍会先进入 TUN。可以为 RustDesk 等程序的固定服务器地址增加绕过 CIDR：

```powershell
.\easy-net-hook.exe `
  --proxy 127.0.0.1:1082 `
  --wechat-existing `
  --tun-stack system `
  --tun-bypass 1.116.229.59/32 `
  --detach
```

`--tun-bypass` 可重复使用，也接受逗号分隔的多个 IPv4/IPv6 CIDR。绕过项直接使用 Windows 原网络，不经过 sing-box 或 SOCKS5；只应配置明确需要直连的目标。兼容性排查时可用 `--tun-stack mixed` 或 `--tun-stack gvisor` 恢复其他网络栈。

UDP 模式默认是 `auto`：

- SOCKS5 支持 `UDP ASSOCIATE` 时，微信 UDP/QUIC 通过代理。
- SOCKS5 不支持 UDP 时，微信 UDP 会被阻断以避免泄漏；能够回退的业务改走 TCP。
- `--tun-udp proxy` 强制尝试代理 UDP。
- `--tun-udp block` 始终阻断 UDP。
- `--tun-udp direct` 允许 UDP 直连，会产生代理泄漏，仅用于明确接受该风险的语音/视频场景。

Easy-Net Lite `0.2.0` 与协议 v3 服务端已经支持 `UDP ASSOCIATE`，所以连接更新后的 1082 等端口时，`auto` 会选择代理 UDP。旧版 Lite、SSH 模式以及未启用 UDP 转发的 v2rayN/Clash 端口会让 `auto` 选择阻断 UDP。可以从 `%LOCALAPPDATA%\EasyNetHook\Tun\wechat-tun.log` 查看 TUN 警告和错误。

TUN 日志默认使用 `warn`，并由生命周期监视器限制在 8 MiB；达到上限后会原地清空并继续记录，不会无限占用磁盘。需要临时观察每条连接时可以添加 `--tun-debug-log` 将级别切换为 `info`，文件大小上限仍然有效。升级后必须停止旧的 sing-box/TUN 并重新启动微信模式，旧进程不会自动加载新的日志策略。

#### 微信 WinDivert（低开销可选后端）

当 TUN 因捕获整机公网流量导致 sing-box CPU 偏高，x64-TUN 包还可以使用 WinDivert 后端：

```powershell
.\easy-net-hook.exe `
  --proxy 127.0.0.1:1082 `
  --wechat `
  --wechat-backend windivert `
  --tun-udp auto `
  --detach
```

接管已运行微信：

```powershell
.\easy-net-hook.exe --proxy 127.0.0.1:1082 --wechat-existing `
  --wechat-backend windivert --tun-udp auto --detach
```

该模式不创建虚拟网卡。WinDivert 在网络层捕获 TCP/UDP 包，配套组件维护 PID/五元组映射，只把微信及其辅助进程重定向到本地 TCP/UDP 中继，再转换为 SOCKS5 `CONNECT` 或 `UDP ASSOCIATE`；不匹配的 Easy-Net Lite WSS 连接会原样放行，不会再次进入 SOCKS5。它仍需要管理员权限，接管已运行进程时也只保证新连接生效。

`--tun-bypass` 和 `--tun-udp` 同时适用于两个微信后端。WinDivert 配置与有界日志位于 `%LOCALAPPDATA%\EasyNetHook\WinDivert`；默认不记录逐连接事件，添加 `--tun-debug-log` 才会启用，日志仍限制为 8 MiB。WinDivert 后端基于固定提交的 MIT 许可 ProxyBridge 和 WinDivert 2.2.2，许可证随包分发。

WinDivert 后端保留 Windows 系统 DNS，目前不应用 `--dns`；需要指定 DNS 时继续使用 TUN 后端。

WinDivert 模式包含独立守护器：每 250 毫秒检查引擎进程，每 10 秒检查一次 SOCKS5 协议握手。引擎异常退出后按照 1、2、4、8、16、32 秒上限的退避策略持续重启；代理端口断开时不把微信规则回退到 DIRECT，而是让匹配的 TCP/UDP 连接失败，代理恢复后自动继续。

查询运行状态：

```powershell
.\easy-net-hook.exe --wechat-status
$LASTEXITCODE
```

状态保存在 `%LOCALAPPDATA%\EasyNetHook\WinDivert\wechat-status.json`。`state` 为 `healthy` 时查询命令返回 `0`；`proxy-unavailable`、`restarting`、`restart-failed` 或没有状态文件时返回 `7`。文件同时记录当前引擎 PID、累计重启次数、代理地址、更新时间和 `fail_closed` 状态。

验证自动重启：

```powershell
Stop-Process -Name easy-net-windivert -Force
Start-Sleep 3
.\easy-net-hook.exe --wechat-status
Get-Process easy-net-windivert
```

验证代理断线阻断时，可以暂时退出 Easy-Net Lite，等待约 20 秒后查询状态。此时应显示 `proxy-unavailable` 和 `fail_closed: true`，微信的新建外网连接应失败；重新启动 Lite 后状态会恢复为 `healthy`。引擎进程本身崩溃到守护器完成重启之间无法承诺严格零窗口防泄漏，状态文件在这段时间会明确显示 `fail_closed: false`；要做到内核级零窗口，需要另外实现 WFP callout 驱动。

#### 验证微信确实进入 TUN

启动或接管后，依次检查：

```powershell
# 1. TUN 引擎和网卡已经存在
Get-Process sing-box -ErrorAction Stop
Get-NetAdapter -Name "easy-net-wechat" -ErrorAction Stop

# 2. 配置确实包含微信进程和 socks-out
Get-Content "$env:LOCALAPPDATA\EasyNetHook\Tun\wechat.json" |
  Select-String 'Weixin.exe|WeChat.exe|socks-out'

# 3. 启动时临时添加 --tun-debug-log，再观察路由日志
Get-Content "$env:LOCALAPPDATA\EasyNetHook\Tun\wechat-tun.log" -Wait
```

诊断日志中出现由 `Weixin.exe` 或微信辅助进程发起、并选择 `socks-out` 的新连接，是按进程规则生效的直接证据。进一步可在 Easy-Net 服务端用户流量统计中比较操作前后的字节数；只看到 TUN 网卡并不能单独证明微信流量已经经过 SOCKS5。排查完成后去掉 `--tun-debug-log` 并重新启动，避免不必要的磁盘写入。

可选 DNS 仍然可用：

```powershell
.\easy-net-hook.exe --proxy 127.0.0.1:10808 --wechat --dns 223.5.5.5:53 --detach
```

不配置时使用 Windows 系统 DNS。配置后，TUN 运行期间捕获到的 DNS 请求统一交给指定服务；Windows DNS 客户端通常由系统服务代查，无法可靠地只按微信进程区分，因此该设置可能同时影响这段时间内其他应用的新 DNS 查询。

如果使用未捆绑 TUN 的轻量包，可以自行放置官方 `sing-box.exe` 到 `tun` 子目录，或者使用 `--tun-engine PATH`。本地构建集成包：

```powershell
.\scripts\build-windows.ps1 -Architecture x64 -WithTunEngine
```

GitHub Actions 同时生成 `Easy-Net-Hook-x64`（轻量）、`Easy-Net-Hook-Win32`（轻量）和 `Easy-Net-Hook-x64-TUN` 三种产物。捆绑的 sing-box 作为独立程序分发，版本、来源和 GPLv3 许可证保存在 `tun` 目录。

### 通用程序

先启动本地 SOCKS5 服务，或先在图形界面导入分享码创建内置代理，再通过启动器打开目标程序：

```powershell
.\easy-net-hook.exe --proxy 127.0.0.1:1080 -- "C:\Path\To\app.exe" --app-option
```

SOCKS5 用户名密码：

```powershell
.\easy-net-hook.exe `
  --proxy 127.0.0.1:1080 `
  --username proxy-user `
  --password proxy-password `
  -- "C:\Path\To\app.exe"
```

指定 DNS 服务（不配置时仍使用 Windows 系统 DNS）：

```powershell
.\easy-net-hook.exe `
  --proxy 127.0.0.1:1080 `
  --dns 223.5.5.5:53 `
  -- "C:\Path\To\app.exe"
```

IPv6 DNS 地址带端口时使用方括号：

```text
--dns [2001:4860:4860::8888]:53
```

附加到已运行的进程：

```powershell
.\easy-net-hook.exe `
  --proxy 127.0.0.1:1080 `
  --pid 12345
```

只能附加相同架构的进程：x64 包对应 x64 目标，Win32 包对应 32 位目标。目标权限高于启动器时，需要以管理员身份运行启动器。注入只影响之后创建的新连接，注入前已经建立的 TCP、HTTP/2 或 WebSocket 连接不会迁移到代理。

### Antigravity IDE

推荐使用 Antigravity 专用模式：

```powershell
.\easy-net-hook.exe `
  --proxy 127.0.0.1:1082 `
  --antigravity `
  --detach
```

默认模式不传 `--user-data-dir`，因此会使用桌面快捷方式启动时相同的登录状态、设置和最近工作区。启动前必须从托盘完全退出原有 Antigravity；否则 Electron 会把新命令转交给已经运行、但没有继承代理的旧实例，启动器会检测这种情况并停止。

启动器会优先从当前运行实例获取安装路径，也会查找常用的用户级和系统级安装目录。找不到时可以明确指定：

```powershell
.\easy-net-hook.exe `
  --proxy 127.0.0.1:1082 `
  --antigravity `
  --antigravity-path "D:\soft\Antigravity IDE\Antigravity IDE.exe" `
  --detach
```

在 `--` 后可以继续传递工作区或其他 IDE 参数：

```powershell
.\easy-net-hook.exe `
  --proxy 127.0.0.1:1082 `
  --antigravity `
  --detach `
  -- "D:\work\my-project"
```

如果需要和桌面版并行运行，或不希望改动正常配置，可以使用独立配置：

```powershell
.\easy-net-hook.exe `
  --proxy 127.0.0.1:1082 `
  --antigravity `
  --antigravity-isolated `
  --detach
```

独立配置保存在 `%LOCALAPPDATA%\EasyNetHook\AntigravityProfile`，首次使用需要单独登录。

该模式用 Chromium 原生 SOCKS5 参数代理 Antigravity IDE 的网络服务，并把大小写形式的 `ALL_PROXY`、HTTP/HTTPS、WebSocket 和 `NO_PROXY` 环境变量传给其子进程。代理环境使用兼容性更广的 `socks5://` URL，并把 localhost、IPv4/IPv6 回环地址保留为直连，避免登录回调和 IDE 本地 RPC 被代理。由扩展启动的 `language_server_windows_x64.exe` 会继承这些设置；另有一个轻量 `easy-net-hook.exe` 监视器，只向 language server 加载兜底 Hook，把不遵守代理环境变量的外部 TCP 连接也送入 SOCKS5。监视器随 IDE 退出，并会处理 language server 的重启。

Antigravity 的 Electron 主进程、network service、renderer 和 GPU 进程都不加载 `easy-net-hook.dll`，因此不会触碰其 Chromium 沙箱；只有普通的 x64 language server 使用 Hook。该模式必须使用 x64 包，也不支持 SOCKS5 用户名密码。请使用本机免认证 SOCKS5 端口。默认情况下，兜底 Hook 的域名仍由 Windows DNS 解析；配置 `--dns IP[:PORT]` 后仅 language server 的兜底解析改用指定 DNS，Chromium URL 域名仍交给 SOCKS5 代理。

### Cursor

Cursor 使用混合代理模式：Electron/Chromium 页面走原生 SOCKS5；AI、Opus 和扩展宿主所在的无沙箱 `node.mojom.NodeService` 通过 Hook 兜底。renderer、GPU、crashpad 等沙箱进程不会注入，因此不需要开启系统全局 TUN：

```powershell
.\easy-net-hook.exe `
  --proxy 127.0.0.1:1082 `
  --cursor `
  --cursor-path "D:\soft\cursor\Cursor.exe" `
  --detach
```

程序会优先使用当前运行的 `Cursor.exe` 路径，并查找常见的用户级和系统级安装目录；便携版或自定义安装目录首次使用时通过界面“浏览”或 `--cursor-path` 指定。默认复用 `%APPDATA%\Cursor`，因此保留正常登录、扩展和设置。Chromium URL 使用 SOCKS5、代理端解析域名并禁用 QUIC；扩展宿主和其他子进程会继承 HTTP/HTTPS、WebSocket、`ALL_PROXY` 与 `NO_PROXY` 环境变量。由于部分 Cursor AI 请求会绕过这些环境变量，启动器还会从进程创建阶段只向无沙箱 Node service 递归加载 `easy-net-hook.dll`，从而在首次建连前覆盖这部分 TCP。

如果已有 Cursor 不是由相同 Easy-Net 代理启动，默认模式会停止并提示先完全退出，避免新窗口被转交给原有直连实例。由当前版本 Easy-Net 启动后，后台生命周期标记会一直跟随 Cursor 主进程；之后使用相同代理再次点击入口，可以直接打开新窗口。若要与当前桌面 Cursor 并行运行，可使用独立配置：

```powershell
.\easy-net-hook.exe `
  --proxy 127.0.0.1:1082 `
  --cursor `
  --cursor-isolated `
  --detach
```

独立配置位于 `%LOCALAPPDATA%\EasyNetHook\CursorProfile`，需要单独登录。Cursor 原生模式不支持 SOCKS5 用户名密码；自定义 `--dns` 不适用于此模式，因为 Chromium 域名已经交给 SOCKS5 代理解析。

### ChatGPT Windows 客户端

推荐使用专用的原生代理模式：

```powershell
.\easy-net-hook.exe `
  --proxy 127.0.0.1:1080 `
  --chatgpt-app `
  --detach
```

`--chatgpt-app` 会自动查找已安装的 Microsoft Store/MSIX 版 ChatGPT，创建独立的 `%LOCALAPPDATA%\EasyNetHook\ChatGPTAppProfile` 用户目录，并用 Chromium 原生 SOCKS5 参数启动第二个实例。它还为 ChatGPT 启动的 `codex.exe` 后端设置 `ALL_PROXY`、HTTP/HTTPS 和 WebSocket 代理环境变量，确保界面请求与实际对话请求都进入 SOCKS5。后端环境继续使用 `socks5h://`，Chromium 也通过代理解析 URL 域名并禁用 QUIC，避免 UDP 绕过。该模式不加载 `easy-net-hook.dll`，因此比 API Hook 更轻，也不会因为向 Chromium 沙箱或网络服务注入 DLL 而出现托盘图标存在但主窗口空白的问题。

原来手动启动的 ChatGPT 可以继续运行。专用模式使用隔离配置目录，所以首次启动的登录状态和原实例相互独立。

Chromium 的 SOCKS5 模式不支持用户名密码，因此 `--chatgpt-app` 只能连接免认证端口；建议将认证和上游连接放在 `127.0.0.1` 上的本地代理程序中完成。该模式通过代理端解析 URL 域名，`--dns` 会被忽略。

以下 `--appx` 方式仍可用于其他普通打包桌面应用，但不推荐用于 ChatGPT。ChatGPT 的 Chromium 网络服务与当前 DLL Hook 的异步套接字行为不完全兼容，可能只出现托盘图标和空白窗口：

```powershell
$chatgptAppId = (Get-StartApps | Where-Object Name -eq "ChatGPT" | Select-Object -First 1).AppID
if (-not $chatgptAppId) { throw "ChatGPT AppID was not found." }

.\easy-net-hook.exe `
  --proxy 127.0.0.1:1080 `
  --appx $chatgptAppId `
  --detach
```

`--appx` 使用 Windows `IApplicationActivationManager` 发出正式的 Launch 激活，然后向 API 返回的应用进程加载 Hook。若 ChatGPT 已经在运行，Windows 可能复用旧实例；此时已经建立的连接不会迁移到代理，所以首次测试前应从托盘彻底退出。

也可以在任务管理器“详细信息”页查找 ChatGPT 的 PID，或者用 PowerShell：

```powershell
Get-Process *ChatGPT* | Select-Object Id, ProcessName, Path
```

对每个 x64 ChatGPT 进程执行一次附加：

```powershell
Get-Process *ChatGPT* | ForEach-Object {
  .\easy-net-hook.exe --proxy 127.0.0.1:1080 --pid $_.Id
}
```

为了避免登录长连接在注入前已经直连，测试时可先断开网络、使用 `--appx` 启动或完成上述附加，再恢复网络。若应用已经联网，附加后需要在应用内触发重新登录或重新连接；完全退出进程会同时卸载 Hook。

默认不配置 `--dns`，让附加模式中的 ChatGPT 继续使用 Windows 系统 DNS。若需要指定 DNS，可在每次附加时添加 `--dns 223.5.5.5:53`。ChatGPT 更新后进程结构可能变化；如果出现 `msedgewebview2.exe` 等独立网络子进程，需要对实际发起连接的同架构进程单独附加。

ChatGPT/Chromium 可能优先尝试 QUIC。外部 UDP 默认被拒绝后通常会回退 TCP；如果客户端版本没有回退，就仍然无法联网。不要为了“能打开”直接使用 `--allow-udp-direct`，否则 QUIC 会绕过 SOCKS5。

#### AppContainer/CIG 安全回退

`--pid` 会在注入前检测 AppContainer、仅允许 Microsoft/Store 签名 DLL 的策略，以及禁止 Detours 动态跳板的策略。检测到这些保护时不会尝试绕过，而是停止并提示使用网页模式：

```powershell
.\easy-net-hook.exe `
  --proxy 127.0.0.1:1080 `
  --chatgpt-web `
  --detach
```

网页模式不需要 `easy-net-hook.dll`，会自动查找 Edge 或 Chrome，并使用独立的 `%LOCALAPPDATA%\EasyNetHook\ChatGPTProfile` 用户目录打开 `https://chatgpt.com/`。它通过 Chromium 原生 `--proxy-server=socks5://...` 代理 URL 请求，强制 URL 域名交给 SOCKS5 代理解析，同时禁用 QUIC 和常见后台联网。

指定浏览器路径：

```powershell
.\easy-net-hook.exe `
  --proxy 127.0.0.1:1080 `
  --chatgpt-web `
  --browser-path "C:\Program Files\Google\Chrome\Application\chrome.exe"
```

Chromium 的 SOCKS5 模式不支持用户名密码，因此网页模式只能连接本机免认证 SOCKS5 端口。网页模式使用代理端 DNS，`--dns` 会被忽略；它只提供 ChatGPT 网页功能，不包含 Windows 客户端的桌面集成功能。

默认行为：

- 向目标进程及其通过 `CreateProcessA/W` 创建的普通子进程加载 Hook。对于 Chromium/Electron，自动跳过 renderer、GPU、crashpad、storage 等子进程，只注入 `network.mojom.NetworkService`，避免沙箱渲染器因 DLL 注入而出现空白窗口。
- 外部阻塞式 TCP 连接直接在原套接字中完成 SOCKS5 握手。
- 非阻塞 `connect` 和 `ConnectEx` 使用按连接创建的本地回环中继，保留事件和 IOCP 完成语义。
- 回环地址连接直接放行，保证目标程序仍可访问本机服务。
- 未配置 `--dns` 时使用 Windows 系统 DNS；配置后 Hook `getaddrinfo` 和 `GetAddrInfoW`，向指定 DNS 服务查询 A/AAAA。
- UDP 默认失败，不会自动降级为直连。
- 启动器等待目标退出，并返回目标程序的退出码。

可用参数：

```text
--gui               打开图形启动器；不带参数运行时也是此行为
--share-code CODE   导入 Easy-Net 分享码并启动内置 SOCKS5
--engine-profile ID 启动已经导入的内置代理配置
--no-children       不向子进程加载 Hook
--pid PID           附加到一个已运行的同架构进程
--appx AUMID        正式激活打包桌面应用，然后附加返回的进程
--antigravity       原生代理启动 IDE，并只为 x64 language server 启用兜底 Hook
--antigravity-path  指定 Antigravity IDE.exe；通常可以自动查找
--antigravity-isolated 使用独立用户目录；默认复用桌面版登录状态
--cursor            原生 SOCKS5 启动 Cursor，并支持同代理多窗口
--cursor-path PATH  指定 Cursor.exe；通常可以自动查找
--cursor-isolated   使用独立 Cursor 用户目录，可与桌面实例并行
--chatgpt-app       不注入，使用独立配置启动 ChatGPT 客户端的原生 SOCKS5 模式
--chatgpt-web       不注入，使用独立 Edge/Chrome SOCKS5 会话打开 ChatGPT
--browser-path PATH 为网页模式指定 Edge/Chrome 可执行文件
--wechat            使用可选 TUN/WinDivert 后端启动微信
--wechat-existing   为已经运行的微信启动按进程路由，不创建新实例
--wechat-path PATH  指定 WeChat.exe/Weixin.exe；通常可以自动查找
--wechat-backend M  微信后端：tun/windivert；默认 tun
--tun-engine PATH   指定 sing-box.exe；默认查找 tun 子目录和 PATH
--windivert-engine  指定 easy-net-windivert.exe
--wechat-status     查询 WinDivert 守护器状态；健康返回 0，否则返回 7
--tun-udp MODE      微信 UDP 策略：auto/proxy/block/direct
--tun-stack MODE    TUN 栈：system/mixed/gvisor；默认 system
--tun-bypass CIDR   让目标 CIDR 绕过 TUN；可重复或使用逗号分隔
--tun-debug-log     临时记录每条 TUN 连接；日志仍限制为 8 MiB
--dns IP[:PORT]     指定普通 DNS 服务；端口默认 53
--allow-udp-direct  允许 UDP 直连；这会产生代理泄漏风险
--detach            启动成功后立即退出启动器
```

SOCKS5 地址目前必须使用字面 IP：

```text
127.0.0.1:1080
[::1]:1080
```

## 当前边界

### 异步 TCP

当前版本支持阻塞和非阻塞 `connect`、常规参数形式的 `WSAConnect`，以及通过 `WSAIoctl` 获取的 `ConnectEx`。异步路径不会伪造 `OVERLAPPED` 或完成端口事件，而是让原始 Winsock 调用连接到一个临时回环监听端口，再由一个轻量工作线程连接 SOCKS5 并双向转发。因此应用仍从 Windows 收到原生的事件/IOCP 完成。

阻塞连接不创建中继线程；每个存活的异步 TCP 连接会占用两个中继套接字和一个 128 KiB 保留栈的工作线程。这适合 ChatGPT、浏览器等连接数较少且连接复用率高的客户端，不适合成千上万短连接的压力代理。SOCKS5 目标连接失败时，本地连接可能已经完成，应用随后会收到连接重置；这与直接连接的报错时序不完全相同。

### Hook 模式的 UDP 默认阻断

Hook DLL 本身尚未实现 SOCKS5 UDP ASSOCIATE。已覆盖的 `connect`、`sendto`、`WSASendTo` 和 `WSASendMsg` 路径默认阻断外部 UDP。微信专用 TUN 和 WinDivert 模式不受此限制；它们在上游 SOCKS5 支持 UDP ASSOCIATE 时可以代理 UDP。其他专用传输 API 仍可能超出 Hook 覆盖范围，所以不能把通用 Hook 当成严格的数据防泄漏产品。

只有明确接受 UDP 绕过时才使用 `--allow-udp-direct`。

### DNS 可选配置

默认不改变系统行为：目标程序的域名继续由 Windows 系统 DNS 解析。

配置 `--dns` 后，本版本会 Hook ANSI `getaddrinfo` 和 Unicode `GetAddrInfoW`，直接向指定的字面 IP 地址发送标准 DNS A/AAAA 查询。UDP 响应带 TC 截断标志时会使用 TCP 重试；每次查询超时为 3 秒。自定义结果使用独立内存分配，并配对 Hook `freeaddrinfo`/`FreeAddrInfoW`。

DNS 查询是目标进程到指定 DNS 服务的普通 UDP/TCP 流量，会绕过 SOCKS5 Hook，以免递归代理；它不是 DoH/DoT，因此默认不加密。配置自定义 DNS 时，尚未实现的异步 `GetAddrInfoExA/W` 会返回 `WSAEOPNOTSUPP`，不会回退到系统 DNS。当前仍未覆盖 `DnsQuery`、应用内置 DoH 和直接构造 DNS 报文的软件，这些路径由应用自身决定。要求严格无泄漏时应继续使用 Mihomo TUN 模式。

### 进程与兼容性

- `--chatgpt-app` 使用原生代理参数及继承环境，不注入 DLL。`--cursor` 同时使用原生代理和定向 Hook：根进程负责把 Hook 传递给 Chromium network service 与无沙箱 Node service，renderer、GPU、crashpad 等沙箱进程不会加载；Cursor 默认复用正常登录配置，并用轻量生命周期标记安全识别相同代理的多窗口启动。`--antigravity` 默认复用正常登录配置，同样不向 Electron 进程注入，但会监视其后代并只 Hook `language_server_windows_x64.exe`，兜底处理忽略代理环境变量的外部 TCP。
- `--wechat` 和 `--wechat-existing` 不注入 DLL，可使用 TUN 或 WinDivert 捕获流量并按进程名分流；它们要求 x64 和管理员权限。接管模式只保证新建连接进入新路由。
- `--pid` 采用标准远程 `LoadLibraryW` 注入，只影响注入后新建的连接；已经建立的连接仍保持原路径。
- Chromium/Electron 子进程采用网络服务定向注入；其 renderer、GPU、crashpad 和非网络 utility 子进程不会加载 Hook。`--no-children` 会连网络服务也一并跳过，因此不适合需要代理 Chromium/Electron 流量的场景。
- `--appx` 先通过 Windows 包激活 API 启动或唤醒应用，再采用与 `--pid` 相同的方式注入。激活到注入之间存在很短的时间窗口，极早建立的连接可能需要在应用内重新连接。
- 附加目标和启动器必须同为 x86 或同为 x64。ARM64 目标目前不支持。
- `--pid` 会预检 AppContainer、二进制签名策略和动态代码策略。受保护进程不会继续注入，ChatGPT 可改用 `--chatgpt-web`。
- 子进程若传入完全自定义的环境块，可能不会继承代理配置。
- 64 位父进程启动 32 位子进程（或反向）时，子进程自动注入会失败；完整产品需要同时部署两种架构并使用辅助注入器。
- `--appx` 支持普通全信任打包桌面应用的 Launch 激活；仍未覆盖 `CreateProcessAsUser`、Windows 服务、受保护/AppContainer 进程以及内核态网络。
- 反作弊、EDR 和安全软件可能阻止或检测 API Hook。
- 目标程序不应再单独配置同一个 SOCKS5，否则可能形成双重代理。

## 安全说明

- SOCKS5 本身不加密客户端到代理的链路。Easy-Net WebSocket/SSH 隧道或应用自身的 TLS 是否加密，要分别判断。
- `--dns` 使用传统明文 DNS。需要加密 DNS 时，建议把地址指向本机 DoH/DoT 转发器，或者后续单独增加 `--doh` 支持。
- `--password` 会短暂出现在启动器命令行，并通过子进程环境传递。只建议连接本机免认证 SOCKS5；生产版应改用 Windows Credential Manager 和受控 IPC。
- 使用代理必须遵守网络、软件服务和所在地区的规则。

## 技术结构

```text
easy-net-hook.exe
  ├─ 默认打开 Easy-Net Lite「应用」页（--legacy-gui 仍可打开旧窗口）
  ├─ --chatgpt-app（原生 SOCKS5 参数 + 后端代理环境）
  ├─ --antigravity（默认配置/可选隔离配置 + IDE 原生代理 + LS 兜底 Hook）
  ├─ --cursor（原生 SOCKS5 + Node service 定向 Hook + 同代理多窗口识别）
  ├─ --wechat / --wechat-existing（可选 sing-box TUN + 微信进程规则 + UDP 能力检测）
  ├─ DetourCreateProcessWithDllExW（启动普通新进程）
  ├─ --appx + IApplicationActivationManager（激活打包应用）
  └─ --pid / --appx + LoadLibraryW（附加运行中进程）
       └─ 目标程序 + easy-net-hook.dll
            ├─ Hook connect / WSAConnect / ConnectEx
            ├─ 阻塞 TCP：原套接字直接连接 SOCKS5
            └─ 异步 TCP：临时回环中继连接 SOCKS5
```

Detours 以 MIT 许可证使用，构建产物会包含其许可证文件。
