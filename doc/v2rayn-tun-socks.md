# v2rayN TUN 集成 Easy-Net SOCKS5 记录

本文记录 Easy-Net 本地 SOCKS5 接入 v2rayN TUN 模式时的现象、原因和最终可用配置。

## 结论

Easy-Net 本地 SOCKS5 可以被 v2rayN 使用，但在 v2rayN 中开启 TUN 时，不建议使用 Xray 作为 Socks 节点内核。

最终可用方案：

- v2rayN 节点类型：SOCKS
- SOCKS 地址：`127.0.0.1`
- SOCKS 端口：Easy-Net 本地端口，例如 `1087`
- Core 类型设置中，`Socks` 使用 `sing_box`
- TUN 模式开启
- TUN 严格路由关闭
- Easy-Net SOCKS 节点 UDP 保持关闭
- Easy-Net 可在“优选 IP（选填）”中填写中继服务器解析出的 IP，减少 TUN 下 DNS 环路风险

## 问题现象

浏览器直接设置代理时，`127.0.0.1:1087` 可以正常上网。

v2rayN 不开启 TUN 时，导入 Easy-Net SOCKS 节点后也可以正常使用。

但 v2rayN 开启 TUN 后，访问 GitHub、Google 等站点失败，日志中可能出现：

```text
当前延迟: -1 ms，none
app/dns: failed to retrieve response ... read/write on closed pipe
from DNS accepted https://cloudflare-dns.com/dns-query [dns-module -> proxy]
from DNS accepted udp:8.8.8.8:53 [dns-module -> proxy]
socks5: request rejected, code=1
```

## 根因

Easy-Net 当前实现的是 TCP SOCKS5 隧道：

- 本地 SOCKS5 只支持 `CONNECT`
- 服务端通过 TCP `net.connect(port, host)` 连接目标
- 不支持 UDP ASSOCIATE
- 不支持 UDP DNS 代理

v2rayN 使用 Xray TUN 时，DNS 模块、TUN 流量和部分 UDP 查询可能会被送入 `proxy` 出站。对于 Easy-Net 这种 TCP-only SOCKS 节点，这会导致 DNS 或 UDP 链路失败。

因此普通代理模式可用，但 Xray TUN 下不稳定。

## 最终配置

### 1. v2rayN 节点

添加或导入 SOCKS 节点：

```text
类型: SOCKS
地址: 127.0.0.1
端口: 1087
UDP: 关闭
```

如果使用 Easy-Net 订阅，可以使用：

```text
http://127.0.0.1:18080/sub/v2rayn.txt
```

### 2. Core 类型设置

在 v2rayN 设置中打开：

```text
Core 类型设置
```

将：

```text
Socks: Xray
```

改成：

```text
Socks: sing_box
```

这是本次问题的关键修复点。

### 3. TUN 模式设置

建议设置：

```text
自动路由: 开启
严格路由: 关闭
协议栈: gvisor 或默认值
IPv6: 关闭
```

严格路由开启时，更容易把本地内核进程、DNS 或 Easy-Net 中继链路卷入 TUN 路由，排查难度更高。

### 4. 进程直连

为避免代理链路自我捕获，建议将以下进程加入直连：

```text
xray.exe
v2rayN.exe
easy-net-manager.exe
easy-net-manager-silent.exe
```

如果使用 Mihomo 或其它本地代理内核，也建议加入：

```text
mihomo.exe
sing-box.exe
```

### 5. WebSocket 中继地址直连

Easy-Net 的 WebSocket 中继地址对应域名必须直连，避免代理套代理或形成环路。

示例：

```text
domain:px.687878.xyz
domain:p2026.687878.xyz
domain:mail.renrendianzhang.com
domain:cloudfront.net
```

### 6. 优选 IP

如果 Easy-Net 配置中 `workerHost` 是域名，建议填写 `endpointIP`。

示例：

```json
{
  "name": "Easy-Net 9025",
  "workerHost": "mail.renrendianzhang.com",
  "localPort": 1087,
  "endpointIP": "43.130.252.225"
}
```

这样 Easy-Net 连接中继时不需要再经过本机 DNS 解析，可以降低 TUN 接管 DNS 后产生环路的概率。

## 验证方式

先确认 Easy-Net 本地 SOCKS 本身可用：

```powershell
curl.exe -I --socks5-hostname 127.0.0.1:1087 https://www.google.com
```

返回类似下面内容说明 SOCKS 节点正常：

```text
HTTP/1.1 200 OK
```

然后在 v2rayN 中：

1. 选择 Easy-Net SOCKS 节点。
2. 确认 `Socks` Core 类型为 `sing_box`。
3. 开启 TUN。
4. 重启服务。
5. 测试 GitHub、Google 或其它需要代理的网站。

## 不推荐配置

以下组合容易失败：

```text
Easy-Net SOCKS + v2rayN + Xray Socks Core + TUN
```

原因是 Xray TUN 的 DNS/UDP 行为会触发 Easy-Net TCP-only SOCKS 的限制。

如果必须使用 Xray Core，建议不要开启 v2rayN TUN，只使用普通系统代理或浏览器手动代理。

## 其它注意事项

- 不要把所有 UDP 流量设置为直连，否则浏览器 QUIC/HTTP3 可能绕过代理。
- Easy-Net 节点在 Clash/Mihomo 中应设置 `udp: false`。
- 如果浏览器访问异常，可以临时关闭 QUIC，让流量回落到 TCP 443。
- v2rayN 当前日志中如果看到 `dns-module -> proxy` 后紧跟 `read/write on closed pipe`，优先检查 SOCKS Core 类型和 TUN DNS 设置。
