# Easy-Net Hook

Easy-Net Hook 是一个 Windows 概念验证版应用代理器。它通过 Microsoft Detours 在用户主动启动的目标进程内 Hook Winsock API，把阻塞式 TCP `connect`/`WSAConnect` 改写为 SOCKS5 `CONNECT`。

它直接复用 Easy-Net Lite、SSH `-D` 或其他客户端提供的 SOCKS5 端口，例如 `127.0.0.1:1080`。它不注入已运行的进程，不安装驱动、不提权，也不修改目标程序文件。

> 这是边界刻意收紧的 MVP，不是 Proxifier 的完整替代品。请先阅读“当前边界”，尤其是异步连接和 DNS 部分。

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

默认行为：

- 向目标进程及其通过 `CreateProcessA/W` 创建的子进程加载 Hook。
- 外部阻塞式 TCP 连接通过 SOCKS5。
- 回环地址连接直接放行，保证目标程序仍可访问本机服务。
- 未配置 `--dns` 时使用 Windows 系统 DNS；配置后 Hook `getaddrinfo` 和 `GetAddrInfoW`，向指定 DNS 服务查询 A/AAAA。
- 不支持的异步连接和 UDP 会失败，不会自动降级为直连。
- 启动器等待目标退出，并返回目标程序的退出码。

可用参数：

```text
--no-children       不向子进程加载 Hook
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

### 仅支持阻塞式 TCP

当前版本支持 `connect` 和常规参数形式的 `WSAConnect`。非阻塞套接字以及通过 `ConnectEx` 发起的异步连接会返回 `WSAEOPNOTSUPP`。如果应用绕过了非阻塞状态跟踪，而连接代理时才暴露异步状态，Hook 会关闭该套接字以阻止后续数据误发。现代 Chromium/Electron、部分游戏和高并发客户端经常使用异步网络 API，因此可能无法使用本版本。

不能通过“先连 SOCKS5，再悄悄把异步状态还给应用”的简单方式解决这个问题。完整实现需要代理 `ConnectEx`、Overlapped I/O、事件通知、完成端口和取消语义，是下一阶段的核心工作。

### UDP 默认阻断

SOCKS5 UDP ASSOCIATE 尚未实现。已覆盖的 `connect`、`sendto`、`WSASendTo` 和 `WSASendMsg` 路径默认阻断外部 UDP。其他专用传输 API 仍可能超出覆盖范围，所以不能把本 MVP 当成严格的数据防泄漏产品。

只有明确接受 UDP 绕过时才使用 `--allow-udp-direct`。

### DNS 可选配置

默认不改变系统行为：目标程序的域名继续由 Windows 系统 DNS 解析。

配置 `--dns` 后，本版本会 Hook ANSI `getaddrinfo` 和 Unicode `GetAddrInfoW`，直接向指定的字面 IP 地址发送标准 DNS A/AAAA 查询。UDP 响应带 TC 截断标志时会使用 TCP 重试；每次查询超时为 3 秒。自定义结果使用独立内存分配，并配对 Hook `freeaddrinfo`/`FreeAddrInfoW`。

DNS 查询是目标进程到指定 DNS 服务的普通 UDP/TCP 流量，会绕过 SOCKS5 Hook，以免递归代理；它不是 DoH/DoT，因此默认不加密。配置自定义 DNS 时，尚未实现的异步 `GetAddrInfoExA/W` 会返回 `WSAEOPNOTSUPP`，不会回退到系统 DNS。当前仍未覆盖 `DnsQuery`、应用内置 DoH 和直接构造 DNS 报文的软件，这些路径由应用自身决定。要求严格无泄漏时应继续使用 Mihomo TUN 模式。

### 进程与兼容性

- 只能代理由启动器新建的进程，不附加到已经运行的进程。
- 子进程若传入完全自定义的环境块，可能不会继承代理配置。
- 64 位父进程启动 32 位子进程（或反向）时，子进程自动注入会失败；完整产品需要同时部署两种架构并使用辅助注入器。
- 未覆盖 `CreateProcessAsUser`、Windows 服务、UWP/商店应用、受保护进程以及内核态网络。
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
  └─ DetourCreateProcessWithDllExW
       └─ 目标程序 + easy-net-hook.dll
            ├─ Hook connect / WSAConnect
            ├─ 连接本地 SOCKS5
            ├─ 执行认证与 CONNECT 握手
            └─ 原套接字继续承载应用 TCP 数据
```

Detours 以 MIT 许可证使用，构建产物会包含其许可证文件。
