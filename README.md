# Easy-Net WebSocket 代理中继

1. 请合法合理使用！
2. 使用该工具上网，可能会被访问的站点限制。出现任何问题，本人概不负责。

## 轻量桌面客户端

Windows/macOS WebSocket + SSH 客户端见 [`client-lite`](client-lite/README.md)。WebSocket 模式支持 SOCKS5 TCP、UDP ASSOCIATE、DNS 和 QUIC 数据报；SSH 模式支持 TCP。每个配置可选择让局域网、VPN 私网、回环和链路本地目标使用本机路由直连。

## Windows 应用级 Hook 代理（实验性）

通过 Winsock API Hook 将新启动、`--appx` 包激活或 `--pid` 附加进程的 TCP 连接转发到 SOCKS5，见 [`client-hook`](client-hook/README.md)。请先启动 Easy-Net Lite，在管理页「应用」标签启动 ChatGPT、Chrome、Edge、Cursor、微信等，也可以使用共享 WinDivert 接管已经运行的普通应用；Hook 本身不再作为独立 GUI 客户端。命令行仍支持阻塞 `connect`、非阻塞连接、`ConnectEx` 轻量回环中继和可选 DNS；遇到 AppContainer/CIG 时可用无需注入的 Chromium SOCKS5 模式。微信固定使用 x64 WinDivert 按进程代理 TCP 和 UDP/QUIC；基础 Hook 模式的 SOCKS5 UDP 仍未实现并默认阻断。Easy-Net Lite 的本地 SOCKS5/HTTP 配置可按 APNIC 中国大陆 IP 地址表分流，公网域名的分类通过当前代理通道访问加密 DNS，不再信任 Windows DNS；Clash 订阅同样使用代理内 DoH，并由独立 `mihomo\mihomo.exe` 运行。
