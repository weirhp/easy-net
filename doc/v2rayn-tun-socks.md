# v2rayN TUN 集成 Easy-Net SOCKS5

本文说明如何将 Easy-Net Lite 的本地 SOCKS5 端口作为 v2rayN TUN 的上游节点。

## 版本要求

- Easy-Net Lite `0.2.0` 或更高版本。
- 服务端必须同步更新，支持 `X-Easy-Net-Protocol: 3`。
- Lite 配置必须使用 WebSocket 传输。SSH 动态转发仍只支持 TCP。

Lite `0.2.0` 增加了标准 SOCKS5 `UDP ASSOCIATE`，UDP DNS、QUIC 等数据报会通过 WebSocket 转发到服务端，再由服务端访问目标地址。Lite 与旧服务端混用时，TCP 仍可工作，但 UDP 会返回明确的“不支持”错误。

## 推荐配置

### 1. v2rayN 节点

添加或导入 SOCKS 节点：

```text
类型: SOCKS
地址: 127.0.0.1
端口: Easy-Net 本地端口，例如 1087
UDP: 开启
```

如果使用 Easy-Net 订阅：

```text
http://127.0.0.1:18080/sub/v2rayn.txt
```

### 2. Core 类型

推荐在 v2rayN 的“Core 类型设置”中使用：

```text
Socks: sing_box
```

新版 Easy-Net 使用标准 SOCKS5 UDP ASSOCIATE，理论上也可由其他兼容内核使用；当前项目主要按 `sing-box` 路径验证，因此排查问题时优先使用该内核。

### 3. TUN 设置

建议从以下配置开始：

```text
自动路由: 开启
严格路由: 关闭
协议栈: gvisor 或默认值
IPv6: 按本机网络情况设置
```

严格路由开启时，更容易把本地内核进程、DNS 或 Easy-Net 中继连接卷入 TUN 路由。

### 4. 防止代理环路

建议将以下程序加入直连规则：

```text
v2rayN.exe
easy-net-manager.exe
easy-net-manager-silent.exe
sing-box.exe
xray.exe
mihomo.exe
```

Easy-Net WebSocket 中继域名也应直连，以免中继连接再次进入自身代理。若配置中的 `workerHost` 是域名，可设置 `endpointIP`，减少 TUN 接管 DNS 后产生解析环路的概率：

```json
{
  "name": "Easy-Net 9025",
  "workerHost": "mail.example.com",
  "localPort": 1087,
  "endpointIP": "203.0.113.10"
}
```

## 验证

先测试 TCP：

```powershell
curl.exe -I --socks5-hostname 127.0.0.1:1087 https://www.google.com
```

再启动 v2rayN TUN 并查看日志。UDP 正常时，DNS 查询和 QUIC 不应再出现 SOCKS5 `request rejected`。如果 TCP 正常而 UDP 失败，依次确认：

1. Lite 版本至少为 `0.2.0`。
2. 服务端已更新且握手响应协议版本为 `3`。
3. 节点的 UDP 开关已开启。
4. 配置使用 WebSocket 而不是 SSH。
5. 服务端或云防火墙没有限制目标 UDP 流量。

## 协议限制

- 不支持 SOCKS5 UDP 分片；`FRAG != 0` 的数据报会被丢弃。
- 单个 UDP 负载最大 65507 字节。
- UDP 关联的生命周期跟随 SOCKS5 TCP 控制连接；控制连接关闭后，本地 UDP 端口也会释放。
- UDP 使用服务端出口地址访问目标，不会把本机 UDP 端口直接暴露到公网。

## 兼容旧版本

旧版 Lite/服务端只支持 SOCKS5 `CONNECT`。如果暂时无法同时升级两端，应在 v2rayN 中关闭该节点的 UDP，并让浏览器关闭 QUIC、回落到 TCP；不要只升级一端后把节点标记为支持 UDP。
