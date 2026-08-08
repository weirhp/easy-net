# Easy-Net WebSocket 代理中继

1. 请合法合理使用！
2. 使用该工具上网，可能会被访问的站点限制。出现任何问题，本人概不负责。

## 轻量桌面客户端

Windows/macOS WebSocket + SSH 客户端见 [`client-lite`](client-lite/README.md)。WebSocket 模式支持 SOCKS5 TCP、UDP ASSOCIATE、DNS 和 QUIC 数据报；SSH 模式支持 TCP。

## Windows 应用级 Hook 代理（实验性）

通过 Winsock API Hook 将新启动、`--appx` 包激活或 `--pid` 附加进程的 TCP 连接转发到 SOCKS5，见 [`client-hook`](client-hook/README.md)。该组件支持阻塞 `connect`、非阻塞连接、`ConnectEx` 轻量回环中继和可选 DNS；遇到 AppContainer/CIG 时可用无需注入的 Chromium SOCKS5 模式。微信可以使用额外的 x64-TUN 包按进程代理 TCP 和 UDP/QUIC；基础 Hook 模式的 SOCKS5 UDP 仍未实现并默认阻断。
