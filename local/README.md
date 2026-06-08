# Easy-Net Local Manager

本地管理器用于统一管理：

- 多个 Easy-Net 本地 SOCKS5 监听实例。
- Mihomo 内核下载、配置生成、启动、停止和重启。
- 外部 SOCKS5 节点。
- Mihomo relay 链式代理和每条链的本地 SOCKS5 listener。
- TUN 模式下按进程名强制分流。

## 快速启动

```powershell
.\scripts\build.bat
.\dist\easy-net-manager.exe
```

打开：

```text
http://127.0.0.1:18080
```

Windows 下程序会常驻系统托盘。关闭浏览器标签页只会收起管理界面，不会停止本地代理；需要停止 Mihomo、停止所有本地 SOCKS5 并退出程序时，请使用：

- 托盘菜单中的 `退出程序`。
- 管理界面右上角的 `退出程序`。

托盘菜单中的 `打开管理界面` 可以重新打开浏览器页面。

目录结构：

```text
src\       源代码和 ui.html
scripts\   构建脚本
dist\      编译输出
```

管理界面的 HTML 放在 `src\ui.html`。程序运行时会优先读取工作目录下的 `ui.html` 或 `src\ui.html`，如果文件不存在，会使用编译进二进制里的兜底版本。

也可以直接运行：

```powershell
.\scripts\build.bat
```

## 配置文件

配置文件仍然是 `local-config.json`。如果检测到旧版格式：

```json
{
  "workerHost": "your-server-domain.com",
  "localPort": 1080,
  "secret": "easy-net-secret-key-12345"
}
```

管理器启动时会自动迁移成新版多实例格式。

## Mihomo

界面中的 `下载 Mihomo` 会从 MetaCubeX/mihomo 最新 release 下载 Windows AMD64 zip，并解压为：

```text
local\mihomo\mihomo.exe
```

生成的配置默认写到：

```text
local\mihomo\config.yaml
```

勾选 `保存后自动启动` 后，每次保存配置都会尝试启动 Mihomo。TUN 模式通常需要管理员权限，如果启动失败，请用管理员权限运行 `easy-net-manager.exe`。

Mihomo 区域提供三个控制入口：

- `打开 API`：打开 `http://127.0.0.1:<controllerPort>/version`。
- `打开面板`：打开 `http://127.0.0.1:<controllerPort>/ui`。
- `更新面板`：调用 Mihomo `/upgrade/ui` 下载或更新 MetaCubeXD 面板资源。

生成的 Mihomo 配置会自动包含：

```yaml
external-ui: ui
external-ui-name: xd
external-ui-url: "https://github.com/MetaCubeX/metacubexd/archive/refs/heads/gh-pages.zip"
```

## 多 SOCKS5

在 `Easy-Net 本地 SOCKS5` 区域可以新增多个本地监听，例如：

```text
127.0.0.1:1080
127.0.0.1:1081
127.0.0.1:1082
```

每个实例可以单独启动或停止。启用的实例会被写入 Mihomo 的 `proxies`。

## 订阅地址

管理器会根据当前运行/启用的 SOCKS5 生成本地订阅地址：

```text
Clash / Clash Verge: http://127.0.0.1:18080/sub/clash.yaml
v2rayN:              http://127.0.0.1:18080/sub/v2rayn.txt
SOCKS 明文链接:      http://127.0.0.1:18080/sub/socks.txt
```

节点来源：

- 已运行的 Easy-Net 本地 SOCKS5。
- 已启用的外部 SOCKS5。
- Mihomo 正在运行时，已启用的链式代理本地 listener。

`/sub/clash.yaml` 返回 Clash YAML 订阅；`/sub/v2rayn.txt` 返回 base64 编码后的 `socks://base64(Configuration)` 分享链接列表；`/sub/socks.txt` 返回明文 SOCKS 分享链接，方便排查。

## 外部 SOCKS5

在 `外部 SOCKS5` 区域可以添加其它本地或远程 SOCKS5，例如：

```text
127.0.0.1:2080
10.0.0.2:1080
```

支持用户名、密码和 UDP 开关。

## 链式代理

在 `链式代理` 区域创建链，`链路` 字段填写节点名称，按顺序用逗号分隔：

```text
Easy-Net 1080, Upstream SOCKS5
```

管理器会生成 Mihomo `relay` 策略组，并可为该链开放一个本地 SOCKS5 listener，例如：

```text
127.0.0.1:12080
```

注意：Mihomo 的 `relay` 已有逐步废弃趋势，后续更推荐使用 `dialer-proxy` 方式表达链路。当前版本先保留 `relay + listeners`，方便快速落地。

## 按进程强制走代理

在 `进程规则` 中按行填写：

```text
Telegram.exe,PROXY
Discord.exe,PROXY
chrome.exe,DIRECT
```

保存后会生成到 Mihomo `rules` 中。默认 `MATCH,DIRECT`，因此只有配置的进程会被强制走 `PROXY` 组。
