# client-hook / client-lite macOS 技术方案

状态：方案评估，尚未开发。
范围：`client-hook`（Windows 应用级代理）与 `client-lite`（Go 桌面客户端）的 macOS 支持。

---

## 一、结论先行

| 项目 | macOS 可行性 | 结论 |
|------|-------------|------|
| `client-lite` | 已基本可用 | 已有 `.app` 构建、CI 双架构、托盘、Keychain。属于**补缺陷 + 补分发**，不是移植 |
| `client-hook` | **不可移植** | Detours Hook 与 WinDivert 两条技术路线在 macOS 上均被系统安全机制封死，无迂回空间 |

`client-hook` 的 ~7300 行 C++ 中，**只有 SOCKS5 协议构造、健康探测、Chromium 启动参数拼接这三部分逻辑可复用**，且这些逻辑简单到应当直接用 Go 在 `client-lite` 内重写，而不是移植 C++ 工程。macOS 不应存在 `easy-net-hook` 这个独立二进制。

macOS 上要实现「按应用代理」，必须换成完全不同的架构，见第三节。

---

## 二、client-hook 的两个硬性技术阻塞

### 2.1 阻塞点一：DLL 注入在 macOS 无等价实现

Windows 侧 `src/hook.cpp` 通过 Detours 挂接 `connect` / `WSAConnect` / `ConnectEx` / `getaddrinfo` 等 Winsock API，注入方式为 `DetourCreateProcessWithDllExW`（新进程）与 `CreateRemoteThread + LoadLibraryW`（附加 PID）。macOS 对应机制全部不可用：

| Windows 机制 | macOS 对应 | 阻塞原因 |
|-------------|-----------|---------|
| `DetourCreateProcessWithDllEx` | `DYLD_INSERT_LIBRARIES` | SIP 开启时，dyld 对「受保护可执行文件」直接**剥离全部 `DYLD_*` 环境变量**。目标应用（Chrome、Edge、Cursor、微信、ChatGPT）全部启用 Hardened Runtime，全部属于受保护范畴 |
| — | 关闭 SIP 后注入 | 仍受 Library Validation 拦截：注入的 dylib 必须与主程序**同一 Team ID 签名**或由 Apple 签名。第三方应用无法满足。且要求用户关 SIP 本身不可作为产品方案 |
| `CreateRemoteThread` 附加 PID | `task_for_pid` + 远程线程 | 对签名应用需 `com.apple.security.cs.debugger` entitlement；SIP 保护的应用完全禁止 attach |
| — | arm64e 目标 | 系统组件使用指针认证，arm64 dylib 无法注入 arm64e 进程 |

这是 Apple 有意设计的封锁，不存在"多花点工夫能做出来"的空间。因此 `hook.cpp`、`child_injection.h`、`cursor_takeover.h`、`config_ipc.h` 整条链路在 macOS 上归零。

### 2.2 阻塞点二：WinDivert 无对等物，官方替代需 Apple 授权

Windows 侧靠 `easy-net-windivert.exe` + `ProxyBridgeCore.dll` + `WinDivert64.sys` 内核驱动，在 IP 层按进程名劫持 TCP/UDP。macOS：

- **`pf` 没有 divert socket**。Apple 移除了 `ipfw`/`divert`，`pf` 只能过滤和 `route-to`，无法把包交给用户态程序改写。
- **唯一官方途径是 NetworkExtension**（`NETransparentProxyProvider` / `NEAppProxyProvider`），其门槛为：

| 要求 | 说明 |
|------|------|
| 打包为 System Extension | 不能是普通进程，需容器 App + 扩展双 bundle |
| Apple Developer Program | 付费账号，$99/年 |
| 特殊 entitlement | `com.apple.developer.networking.networkextension` = `app-proxy-provider-systemextension`（Developer ID 分发必须用 `-systemextension` 后缀值），需在开发者后台申请 Network Extension + System Extension capability 并生成 provisioning profile |
| 公证（notarization） | 未公证则要求终端用户关闭 SIP 才能加载 |
| 语言 | 扩展必须 Swift/ObjC，Go 主程序只能经 XPC 通信 |
| per-app 限制 | `NEAppProxyProvider` 的按应用规则在非 MDM 托管环境对普通应用不生效；真正可用的是 `NETransparentProxyProvider` + 按 signing identifier 匹配 |

对本项目（开源、用户自行构建、无签名分发）而言，这条路线会**把自构建用户完全挡在门外**。

---

## 三、推荐架构：macOS 三层代理模型

不移植 hook，改为在 `client-lite` 内用 Go 实现三层能力。覆盖面与 Windows 版接近，且**零 Apple 授权门槛**。

| 层 | 手段 | 覆盖目标 | 权限 |
|----|------|---------|------|
| L1 原生代理参数 | 启动时注入 Chromium 命令行参数 | Chrome、Edge、Cursor、Antigravity 等 Electron/Chromium 应用 | 普通用户 |
| L2 系统代理 | `networksetup -setsocksfirewallproxy` | 遵循系统代理的原生应用（NSURLSession 类），如 ChatGPT、部分微信流量 | 管理员密码（一次） |
| L3 TUN 全局 + 进程分流 | 复用已集成的 `mihomo`，开启 utun TUN + `find-process-mode` + `PROCESS-NAME` 规则 | 任意应用，包括微信、不遵循系统代理的应用 | root |

### 3.1 L1：Chromium 原生 SOCKS5（对应 Windows 的 `--chrome` / `--cursor` 等）

`client-hook/src/browser_proxy.h` 的核心 `NativeSocksArguments()` 本质是字符串拼接（`--proxy-server`、`--host-resolver-rules`、`--proxy-bypass-list`、`--disable-quic`），与平台无关。macOS 侧改为：

- Go 内实现同名逻辑，参数值完全复用；
- 启动方式由 `CreateProcessW` 改为 `open -na "/Applications/Xxx.app" --args ...`，或直接 `exec` `Contents/MacOS/<binary>`；
- 隔离 profile 目录：`--user-data-dir` 指向 `~/Library/Application Support/Easy-Net Lite/profiles/<app>/<proxy_key>`；
- 应用发现：`/Applications` + `~/Applications` + `mdfind kMDItemCFBundleIdentifier` 定位，替代 Windows 注册表/MSIX 发现。

这一层覆盖了 Windows 版实际使用中的主要场景，且实现成本低。

**需实测确认**：macOS 版 ChatGPT 桌面应用为原生实现（非 Electron）的可能性较高，若如此则 `--proxy-server` 无效，须降级到 L2 或 L3。Cursor / Antigravity 为 Electron，L1 有效。

### 3.2 L2：系统代理

`networksetup -setsocksfirewallproxy <服务名> 127.0.0.1 <端口>`，需遍历 `networksetup -listallnetworkservices` 对活动服务逐个设置。要点：

- 需管理员权限，通过 `osascript -e 'do shell script "..." with administrator privileges'` 提权；
- 必须保证 Lite 退出/崩溃时恢复原设置，否则用户断网。需要在配置目录持久化原始状态并在启动时做一致性校验；
- 与 L3 互斥，不能同时启用。

### 3.3 L3：mihomo TUN（对应 Windows 的 WinDivert）

复用度最高的一层——`internal/clashsub` 已经在管理 mihomo 进程生命周期。macOS 上 mihomo 支持 utun 设备（设备名必须以 `utun` 开头）与 `PROCESS-NAME` 规则（需 `find-process-mode: strict|always`）。

需新增：

- 生成带 `tun.enable: true` 的配置，出站指向 Lite 自己的本地混合代理端口（保持现有 WebSocket/SSH/China-IP 分流逻辑不变，mihomo 只做进程分流与 TUN 承载）；
- **root 权限管理**：mihomo 开 TUN 必须 root。可选方案：
  - a) `osascript` 每次提权启动（实现简单，体验差，每次弹密码）；
  - b) 安装 `LaunchDaemon` 特权 helper（一次授权，长期有效，但需要写 helper 与卸载逻辑）；
  - c) `chown root + chmod u+s` 给 mihomo（不推荐，安全性差）。
  建议先做 a，视反馈再做 b。
- 微信等原生应用只能靠这一层，与 Windows 版的 WinDivert 定位一致。

---

## 四、client-lite 现存 macOS 缺陷（真问题，需修）

| # | 问题 | 位置 | 影响 | 严重度 |
|---|------|------|------|--------|
| 1 | `ownedProcessRunning` 读 `/proc/<pid>/exe`，macOS 无 `/proc`，恒返回 false | `internal/clashsub/process_other.go:50` | Lite 重启后无法识别遗留 mihomo 进程 → 不清理、不复用、端口占用、僵尸进程堆积；`terminateOwnedProcess` 实际为 no-op | 高 |
| 2 | 无 codesign / notarization | `scripts/build-macos.sh`、CI | 用户下载 zip 双击被 Gatekeeper 拦截，需右键打开或 `xattr -d com.apple.quarantine` | 高 |
| 3 | mihomo 未打包，且查找路径在 `.app` 内语义不清（`exeDir` = `Contents/MacOS`） | `internal/clashsub/runner.go` `findMihomo` | 用户手动放入的 mihomo 带 quarantine 属性，首次执行被拦；路径约定无文档 | 中 |
| 4 | 无开机自启（LaunchAgent） | 全局缺失 | 与 Windows 版体验不一致 | 中 |
| 5 | `launch.Supported()` 硬编码 `GOOS == "windows"` | `internal/launch/service.go:132` | 布尔粒度过粗，无法表达「macOS 支持 L1/L3 但不支持 hook」 | 中（阻塞第三节） |
| 6 | 分架构包而非 universal binary | `build-macos.sh`、CI matrix | 用户容易下错架构包 | 低 |

已确认在 macOS 正常的部分：托盘（systray + `LSUIElement=true`）、Keychain（go-keyring）、配置权限（`permissions_other.go` chmod 0600）、原子替换（`os.Rename`）、单实例、`.app` 构建与 CI 双架构。

---

## 五、关键架构改动：能力矩阵替代布尔开关

现状是 `launch.Supported() bool` → `/api/state` 的 `features.appLaunches` → 前端 `syncTabs()` 整体隐藏 `#tab-apps`。这个粒度无法支撑 macOS 部分可用的现实。

建议改为：

```go
// internal/launch
type Capability string

const (
    CapChromiumNative Capability = "chromium-native" // 三端
    CapHookInject     Capability = "hook-inject"     // 仅 Windows
    CapPacketTakeover Capability = "packet-takeover" // Windows: WinDivert / macOS: mihomo TUN
    CapSystemProxy    Capability = "system-proxy"    // 仅 macOS
    CapProcessPicker  Capability = "process-picker"
    CapShortcut       Capability = "shortcut"
)

func Capabilities() []Capability // 平台实现：capabilities_windows.go / capabilities_darwin.go / capabilities_other.go
```

`/api/state` 下发 `features.launchCapabilities`，前端按 mode 逐项过滤入口，而非整体隐藏 Tab。新增 `internal/launch/runner_darwin.go` 实现 L1 + L3，`runner_other.go` 保持 stub。

这是本方案中唯一涉及现有代码结构的改动，属于必要项。

---

## 六、分阶段计划与工作量

| 阶段 | 内容 | 产出 | 估算 |
|------|------|------|------|
| P0 | 修 `process_other.go` 的 darwin 实现（`libproc` / `sysctl KERN_PROC_PID` 取进程路径）；补 mihomo 打包与路径约定文档；补 quarantine 清除提示 | macOS 现有功能可靠 | 1–2 天 |
| P1 | Developer ID 签名 + 公证接入 CI；universal binary（`lipo`）；LaunchAgent 开机自启 | 可正常分发的 macOS 包 | 2–3 天（不含账号申请） |
| P2 | `launch.Capabilities()` 能力矩阵重构 + 前端按 mode 过滤 | 架构就位 | 2 天 |
| P3 | L1 Chromium 原生代理启动（`runner_darwin.go` + 应用发现 + 隔离 profile） | Chrome/Edge/Cursor/Antigravity 可用 | 3–4 天 |
| P4 | L2 系统代理（含崩溃恢复保障） | 原生应用可用 | 2 天 |
| P5 | L3 mihomo TUN + 进程分流 + root 提权 | 微信等任意应用可用 | 4–6 天 |
| 可选 | NETransparentProxyProvider 系统扩展 | 正统 per-app | 3 周+，且需付费账号与 entitlement 审批 |

P0/P1 与 P2–P5 相互独立，可并行。

---

## 七、风险清单

| 风险 | 影响 | 缓解 |
|------|------|------|
| 无 Apple Developer ID 账号 | P1 无法完成，用户须手动绕过 Gatekeeper | 短期在 README 提供 `xattr` 说明；长期申请账号 |
| L2 系统代理未恢复导致用户断网 | 严重可用性事故 | 持久化原始设置 + 启动时校验 + 托盘提供「强制恢复」 |
| L3 需要 root，弹密码影响体验 | 用户流失 | 先 `osascript`，后续做 LaunchDaemon helper |
| macOS 版 ChatGPT / 微信 为原生应用，L1 无效 | 功能覆盖低于 Windows | 实测确认后在文档明确标注各应用适用层级 |
| mihomo TUN 与系统 VPN / 公司 MDM 冲突 | 部分环境不可用 | `auto-detect-interface` + 文档说明；保留 L1/L2 作为退路 |
| macOS 三版本（12/13/14/15+）行为差异 | 兼容性问题 | CI 至少覆盖两个主版本做冒烟测试 |
| Gatekeeper 对用户自放 mihomo 的 quarantine | 启动失败且错误信息不明确 | `findMihomo` 检测 `com.apple.quarantine` 并给出明确处置提示 |

---

## 八、明确不做的事

1. 不移植 `client-hook` 的 C++ 工程到 macOS，不产出 macOS 版 `easy-net-hook` 二进制。
2. 不实现基于 `DYLD_INSERT_LIBRARIES` 的注入（对目标应用无效，且要求关 SIP）。
3. 不实现需要关闭 SIP 的任何方案。
4. 第一期不做 NetworkExtension 系统扩展。
5. 不改动 `client-lite` 现有的 WebSocket / SSH / 分享码 / China-IP 分流逻辑——这些已跨平台且工作正常。
