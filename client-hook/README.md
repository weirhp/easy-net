# Easy-Net Hook

Easy-Net Hook 是一个轻量 Windows 应用代理器。普通 Win32 程序通过 Microsoft Detours Hook Winsock API，把 TCP `connect`、`WSAConnect` 和 `ConnectEx` 改写为 SOCKS5 `CONNECT`；ChatGPT 和 Antigravity IDE 则使用更稳定的原生代理模式，不向 Electron/Chromium 沙箱注入 DLL。

它直接复用 Easy-Net Lite、SSH `-D` 或其他客户端提供的 SOCKS5 端口，例如 `127.0.0.1:1080`。它既能通过图形界面保存常用程序并快捷启动，也能通过命令行启动普通新进程、激活打包桌面应用或附加到已运行进程；不安装驱动，也不修改目标程序文件。

> 这仍不是 Proxifier 的完整替代品。请先阅读“当前边界”，尤其是运行中进程、UDP 和 DNS 部分。

## 构建

需要 Visual Studio 2022，并安装：

- Desktop development with C++。
- MSVC v143 工具集。
- Windows 10/11 SDK。
- CMake tools for Windows。

只安装命令行构建工具时，可以在管理员 PowerShell 中执行：

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

首次配置会从 Microsoft 官方仓库下载 Detours v4.0.1。输出目录包含：

```text
easy-net-hook.exe
easy-net-hook.dll
THIRD-PARTY-LICENSES\Detours-LICENSE.md
```

启动器、DLL 和目标程序的架构必须相同。64 位程序使用 x64 包，32 位程序使用 Win32 包。

## 使用

### 图形界面

直接双击 `easy-net-hook.exe`，或者执行：

```powershell
.\easy-net-hook.exe --gui
```

界面可以选择 ChatGPT、Antigravity IDE 或通用 Hook 模式，填写 SOCKS5 地址、程序路径、启动参数和可选 DNS。每次成功启动后会自动记录配置，之后可在左侧列表双击快捷启动，也可以删除单条记录或清空全部记录。

记录最多保留 30 条，保存在 `%LOCALAPPDATA%\EasyNetHook\launcher-history.tsv`。其中不保存代理用户名或密码。ChatGPT 和 Antigravity 仍使用各自隔离且可复用的用户目录，因此不会占用手动启动实例的配置目录。

### 通用程序

先启动本地 SOCKS5 服务，再通过启动器打开目标程序：

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

该模式用 Chromium 原生 SOCKS5 参数代理 Antigravity IDE 的网络服务，并把大小写形式的 `ALL_PROXY`、HTTP/HTTPS、WebSocket 和 `NO_PROXY` 环境变量传给其子进程。由扩展启动的 `language_server_windows_x64.exe` 会继承 `socks5h://` 代理配置；另有一个轻量 `easy-net-hook.exe` 监视器，只向 language server 加载兜底 Hook，把不遵守代理环境变量的外部 TCP 连接也送入 SOCKS5。监视器随 IDE 退出，并会处理 language server 的重启。

Antigravity 的 Electron 主进程、network service、renderer 和 GPU 进程都不加载 `easy-net-hook.dll`，因此不会触碰其 Chromium 沙箱；只有普通的 x64 language server 使用 Hook。该模式必须使用 x64 包，也不支持 SOCKS5 用户名密码。请使用本机免认证 SOCKS5 端口。默认情况下，兜底 Hook 的域名仍由 Windows DNS 解析；配置 `--dns IP[:PORT]` 后仅 language server 的兜底解析改用指定 DNS，Chromium URL 域名仍交给 SOCKS5 代理。

它使用 `%LOCALAPPDATA%\EasyNetHook\AntigravityProfile` 下按代理地址隔离且可复用的配置目录，可以和手动启动的 Antigravity 同时运行。

### ChatGPT Windows 客户端

推荐使用专用的原生代理模式：

```powershell
.\easy-net-hook.exe `
  --proxy 127.0.0.1:1080 `
  --chatgpt-app `
  --detach
```

`--chatgpt-app` 会自动查找已安装的 Microsoft Store/MSIX 版 ChatGPT，创建独立的 `%LOCALAPPDATA%\EasyNetHook\ChatGPTAppProfile` 用户目录，并用 Chromium 原生 SOCKS5 参数启动第二个实例。它还为 ChatGPT 启动的 `codex.exe` 后端设置 `ALL_PROXY`、HTTP/HTTPS 和 WebSocket 代理环境变量，确保界面请求与实际对话请求都进入 SOCKS5。域名通过 `socks5h://` 交给代理端解析，同时禁用 QUIC，避免 UDP 绕过。该模式不加载 `easy-net-hook.dll`，因此比 API Hook 更轻，也不会因为向 Chromium 沙箱或网络服务注入 DLL 而出现托盘图标存在但主窗口空白的问题。

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
--no-children       不向子进程加载 Hook
--pid PID           附加到一个已运行的同架构进程
--appx AUMID        正式激活打包桌面应用，然后附加返回的进程
--antigravity       原生代理启动 IDE，并只为 x64 language server 启用兜底 Hook
--antigravity-path  指定 Antigravity IDE.exe；通常可以自动查找
--chatgpt-app       不注入，使用独立配置启动 ChatGPT 客户端的原生 SOCKS5 模式
--chatgpt-web       不注入，使用独立 Edge/Chrome SOCKS5 会话打开 ChatGPT
--browser-path PATH 为网页模式指定 Edge/Chrome 可执行文件
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

### UDP 默认阻断

SOCKS5 UDP ASSOCIATE 尚未实现。已覆盖的 `connect`、`sendto`、`WSASendTo` 和 `WSASendMsg` 路径默认阻断外部 UDP。其他专用传输 API 仍可能超出覆盖范围，所以不能把本 MVP 当成严格的数据防泄漏产品。

只有明确接受 UDP 绕过时才使用 `--allow-udp-direct`。

### DNS 可选配置

默认不改变系统行为：目标程序的域名继续由 Windows 系统 DNS 解析。

配置 `--dns` 后，本版本会 Hook ANSI `getaddrinfo` 和 Unicode `GetAddrInfoW`，直接向指定的字面 IP 地址发送标准 DNS A/AAAA 查询。UDP 响应带 TC 截断标志时会使用 TCP 重试；每次查询超时为 3 秒。自定义结果使用独立内存分配，并配对 Hook `freeaddrinfo`/`FreeAddrInfoW`。

DNS 查询是目标进程到指定 DNS 服务的普通 UDP/TCP 流量，会绕过 SOCKS5 Hook，以免递归代理；它不是 DoH/DoT，因此默认不加密。配置自定义 DNS 时，尚未实现的异步 `GetAddrInfoExA/W` 会返回 `WSAEOPNOTSUPP`，不会回退到系统 DNS。当前仍未覆盖 `DnsQuery`、应用内置 DoH 和直接构造 DNS 报文的软件，这些路径由应用自身决定。要求严格无泄漏时应继续使用 Mihomo TUN 模式。

### 进程与兼容性

- `--chatgpt-app` 使用原生代理参数及继承环境，不注入 DLL。`--antigravity` 同样不向 Electron 进程注入，但会监视其后代并只 Hook `language_server_windows_x64.exe`，兜底处理忽略代理环境变量的外部 TCP。
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
  ├─ GUI + 本地历史记录（快捷启动常用配置）
  ├─ --chatgpt-app（原生 SOCKS5 参数 + 后端代理环境）
  ├─ --antigravity（IDE 原生代理 + language server 专用兜底 Hook）
  ├─ DetourCreateProcessWithDllExW（启动普通新进程）
  ├─ --appx + IApplicationActivationManager（激活打包应用）
  └─ --pid / --appx + LoadLibraryW（附加运行中进程）
       └─ 目标程序 + easy-net-hook.dll
            ├─ Hook connect / WSAConnect / ConnectEx
            ├─ 阻塞 TCP：原套接字直接连接 SOCKS5
            └─ 异步 TCP：临时回环中继连接 SOCKS5
```

Detours 以 MIT 许可证使用，构建产物会包含其许可证文件。
