# OmniGate Proxy - 通用网页代理服务

本项目利用 Cloudflare Workers 的全球 CDN 节点搭建反向代理，实现对 `google.com`、`github.com`、`youtube.com` 以及其他网站的稳定访问。

它不仅提供一个精心设计的暗黑毛玻璃风格的入口门户主页，还会动态重写网页中的链接以确保您的点击和后续资源加载（包括视频流切片）继续走代理通道。

---

## 项目结构
*   `index.js` - 运行在 Cloudflare Workers 上的反向代理核心逻辑代码（包含门户 UI 模板）。
*   `wrangler.toml` - Cloudflare Workers 部署的配置文件。

---

## 部署教程

### 准备工作
1. 注册一个 [Cloudflare 账号](https://dash.cloudflare.com/)（完全免费）。
2. 在您的本地计算机上安装 [Node.js](https://nodejs.org/)（如已安装可跳过）。

### 部署步骤

1.  **打开终端并切换到项目目录**：
    ```bash
    cd proxy-google-cf
    ```

2.  **登录 Cloudflare 账号**：
    运行以下命令进行登录授权。执行后浏览器会自动打开，点击“批准/Authorize”即可完成登录：
    ```bash
    npx wrangler login
    ```

3.  **发布服务**：
    运行以下部署命令：
    ```bash
    npx wrangler deploy
    ```
    部署成功后，终端将输出类似如下的默认域名地址：
    ```text
    Published omnigate-proxy (6.12s)
      https://omnigate-proxy.<your-subdomain>.workers.dev
    ```

---

## 安全访问与默认账号 (SuperBlog 登录)

为了保护您的私有代理网关不被他人滥用，网页端已启用安全登录保护（SuperBlog 伪装入口）：

* **默认管理员用户名**：`admin`
* **默认管理员密码**：`superblog123`
* *提示：您可以在 [config.json](file:///d:/work/me-pro/local-work/proxy-google-cf/config.json) 文件中的 `adminUser` 和 `adminPass` 字段自由修改您的账号和密码。修改后重新运行 `npx wrangler deploy` 部署即可生效。*

---

## 关键：绑定自定义域名（非常重要）

> [!WARNING]
> 由于 `*.workers.dev` 域名在部分地区可能遭到 DNS 污染或封锁，直接访问默认域名可能会报错或连接失败。
> **强烈建议绑定您自己的自定义域名**以获得最佳、最稳定的使用体验。

### 绑定自定义域名步骤：

1. 在您的 Cloudflare 控制面板中，添加并解析您的自定义域名（例如 `yourdomain.com`）。
2. 进入 Cloudflare 控制台 -> **Compute (Workers & Pages)** -> 点击您的项目 `omnigate-proxy`。
3. 切换到 **Settings (设置)** 选项卡 -> **Triggers (触发器)**。
4. 在 **Custom Domains (自定义域名)** 区域，点击 **Add Custom Domain (添加自定义域名)**。
5. 输入您准备使用的二级域名（例如 `proxy.yourdomain.com`），点击保存。Cloudflare 会自动为您配置 DNS 记录并申请 SSL 证书。
6. *提示：由于本项目采用的是“路径前缀代理机制”，您**只需要绑定这一个单域名**即可代理所有目标站点，无需配置复杂的通配符 (Wildcard) 域名或多个域名。*

---

## 常见站点使用说明

1.  **Google 搜索**：
    *   主页集成了自动重定向至 Google 搜索的快捷卡片。
    *   内置 `NCR (No Country Redirect)` 机制，防止访问时被重定向到 `google.com.hk`（该香港域名在境内连接极其不稳定）。
2.  **GitHub 社区**：
    *   支持流畅浏览仓库、提 issue、看 wiki、下载 releases 资源等。
    *   内置对 `raw.githubusercontent.com` 以及头像域名的代理转发。
3.  **YouTube 视频播放说明**：
    *   我们的脚本支持捕获并转发视频流媒体域名（`*.googlevideo.com`），可以实现视频的加载和播放。
    *   **限制**：Cloudflare Workers 免费版每日有 **10 万次请求限额**。由于 YouTube 视频采用分片加载，播放一部视频可能会消耗数百次请求。如果频繁观看高清视频，该免费额度容易耗尽。建议将其主要用于网页端搜索、频道浏览或短视频。

---

## 进阶：使用 AWS CloudFront 加速访问 (解决大陆连接延迟)

如果您在国内访问 Cloudflare Worker 觉得卡顿或延迟较高，可以使用 **AWS CloudFront (CDN)** 来中继代理并加速您的服务（CloudFront 提供每月 **1 TB** 的永久免费出站流量）。

### 1. AWS CloudFront 配置步骤

1. 登录 [AWS 控制台](https://console.aws.amazon.com/)，在服务中搜索并进入 **CloudFront**。
2. 点击 **Create Distribution** (创建分配)。
3. 在 **Specify origin (指定源站)** 页面：
   * **Origin type**：选择 **`Other`**。
   * **Origin domain**：填写您的 Cloudflare Worker 域名（例如：`omnigate-proxy.weirhp.workers.dev`）。
   * **Protocol**：选择 **`HTTPS only`**。
4. 在 **Settings** 和 **Cache settings** 页面：
   * **缓存策略 (Cache policy)**：选择 **`CachingDisabled`** (禁用缓存，代理服务必须实时回源)。
   * **源请求策略 (Origin request policy)**：选择 **`AllViewerExceptHostHeader`** (放行 WebSocket 并保持源站 Host 标头)。
5. 其他选项保持默认，点击创建。等待部署完成（约 3 分钟）后，复制 AWS 分配给您的 CloudFront 域名 (如 `dxxxxx.cloudfront.net`)。

### 2. 本地 SOCKS5 客户端配置

打开本地代理客户端的配置文件 [local-config.json](file:///d:/work/me-pro/local-work/proxy-local/local-config.json)，将 `workerHost` 改为您的 **CloudFront 域名**：

```json
{
  "workerHost": "dxxxxx.cloudfront.net",
  "localPort": 1080,
  "secret": "omnigate-secret-key-12345"
}
```

保存并重启本地客户端即可享受亚太高速节点带来的超低延迟加速体验！

### 3. 常见问题：AWS CloudFront 403 错误（空 User-Agent 阻断）

> [!WARNING]
> **现象**：当本地客户端配置文件 `local-config.json` 使用了 AWS CloudFront 域名时，客户端日志中频现 `Unexpected server response: 403` 错误，导致代理不可用，但直连 Cloudflare Worker 域名却正常。
>
> **原因**：AWS CloudFront 及其关联的安全防护策略（WAF）默认会拒绝未携带或携带非浏览器标识（例如 Node.js `ws` 库的默认空标识）的 WebSocket 握手请求。
>
> **解决办法**：我们已在本地客户端代码 [local-client.js](file:///d:/work/me-pro/local-work/proxy-local/local-client.js) 的 WebSocket 连接参数中补充了标准的浏览器 `User-Agent` 报文头：
>
> ```javascript
> ws = new WebSocket(wsUrl, {
>   headers: {
>     'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36'
>   }
> });
> ```
>
> 请确保您的 [local-client.js](file:///d:/work/me-pro/local-work/proxy-local/local-client.js) 为最新版本，并重启客户端以使设置生效。
