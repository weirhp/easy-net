# 基于 GitHub Actions 构建 Easy-Net Hook

本文是给维护此仓库的 AI 或开发者使用的构建操作说明。目标是通过 GitHub Actions 生成可下载的 Windows 组合包，而不是在本机安装 Visual Studio 后手工编译。

## 构建内容

工作流文件为 [`.github/workflows/client-hook-build.yml`](../.github/workflows/client-hook-build.yml)，工作流名称为 **Build Easy-Net Hook**。

每次构建会同时编译并测试：

- `easy-net-hook.exe`：启动器、应用 Hook 和 WinDivert 管理入口。
- `easy-net-hook.dll`：普通 Win32 程序的 Winsock Hook DLL。
- `Easy-Net-Lite.exe`：常驻托盘的代理与应用管理程序；由同一个工作流从 `client-lite` 源码编译。
- x64 专用 WinDivert 组件：`easy-net-windivert.exe`、`ProxyBridgeCore.dll`、`WinDivert.dll` 和驱动文件。

因此，修改 `client-lite` 后也必须重新构建并下载 Hook 组合包，不能只替换旧的 Lite EXE。

## 自动触发规则

向 `main` 推送以下任一目录或工作流文件的改动，会自动触发构建：

- `client-hook/**`
- `client-lite/**`
- `.github/workflows/client-hook-build.yml`

也可以在 GitHub 页面手动运行：仓库的 **Actions** → **Build Easy-Net Hook** → **Run workflow** → 选择 `main`。

推荐的提交和推送流程：

```powershell
git status
git add client-hook client-lite .github/workflows/client-hook-build.yml
git commit -m "feat: describe the change briefly"
git push origin main
```

推送前必须先检查 `git status`，不要把 `build/`、`dist/`、日志、账号配置或本机生成的临时文件加入提交。

## GitHub Actions 构建流程

工作流使用 `windows-latest`，按以下顺序执行：

1. 检出代码，并从 `client-lite/go.mod` 安装对应 Go 版本。
2. 使用 CMake 配置 `client-hook`。
3. 分别构建 `x64` 和 `Win32` Release。
4. 在每个架构上执行 `ctest`。
5. 打包 Hook、DLL、完整 Lite 程序和第三方许可证。
6. x64 额外下载并打包 sing-box TUN 引擎与 WinDivert 组件。

构建产物有效期为 14 天。下载前应确认工作流运行状态为绿色 **Success**；失败时先查看失败步骤的完整日志，尤其是 **Configure**、**Build** 或 **Test**。

## 下载与使用产物

打开对应的 Actions 运行记录，在页面底部 **Artifacts** 下载需要的压缩包：

| 产物 | 用途 |
| --- | --- |
| `Easy-Net-Hook-x64` | 64 位 Windows 程序；不包含 sing-box TUN 引擎。 |
| `Easy-Net-Hook-Win32` | 32 位 Windows 程序；不能用于注入 64 位目标进程。 |
| `Easy-Net-Hook-x64-TUN` | 推荐的 64 位完整包；包含 WinDivert、sing-box TUN 引擎和 Lite。 |

解压后必须保留目录结构，尤其不要拆开以下文件和目录：

```text
easy-net-hook.exe
easy-net-hook.dll
Easy-Net-Lite.exe
windivert\                 # x64 TUN 包
tun\                       # x64 TUN 包
THIRD-PARTY-LICENSES\
```

日常使用启动 `Easy-Net-Lite.exe`；双击 `easy-net-hook.exe` 也会尝试打开同目录 Lite 的“应用代理管理”页面。不要把 DLL 或 WinDivert 目录单独复制到其他位置。

## AI 修改后的最小验证

在提交前，至少执行：

```powershell
cd client-lite
go test ./...
go vet ./...
cd ..
git diff --check
```

如果本机具备 Visual Studio 2022、Windows SDK、CMake 和 Go，也可以额外执行：

```powershell
cd client-hook
.\scripts\build-windows.ps1 -Architecture x64 -Configuration Release -WithTunEngine
```

但本机构建成功不能替代 GitHub Actions：发布前仍以 Actions 的 x64 和 Win32 测试结果为准。

## 常见问题

### 修改 Lite 后下载的 Hook 包还是旧界面

确认本次提交包含 `client-lite/**`，并下载本次 Actions 运行生成的包。Hook 的 CMake 配置会把 Lite 编译进组合包；只替换 `easy-net-hook.exe` 不会更新 Lite。

### 想让变更触发构建，但没有修改源码

在 Actions 页面手动运行 **Build Easy-Net Hook**。不要为了触发构建提交无意义的空白改动。

### x64 包和 Win32 包怎么选

绝大多数现代 Windows 应用使用 `Easy-Net-Hook-x64-TUN`。只有目标应用本身是 32 位时，才使用 `Easy-Net-Hook-Win32`；启动器、DLL 与被注入应用必须是同一架构。

### 构建失败后应如何处理

先读取失败步骤日志，修复根因后提交新的修复。不要通过删除测试、忽略 `ctest` 失败或重新运行同一失败版本来掩盖问题。若依赖下载失败，优先检查 GitHub Actions 网络错误和上游 Release 可用性，再决定是否重试。
