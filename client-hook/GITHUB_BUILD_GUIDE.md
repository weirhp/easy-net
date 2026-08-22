# 基于 GitHub Actions 构建 Easy-Net Hook

工作流：`.github/workflows/client-hook-build.yml`，名称为 **Build Easy-Net Hook**。

## 自动构建内容

工作流在 `windows-latest` 上构建并测试 x64 与 Win32：

- `easy-net-hook.exe`：应用代理启动器和 WinDivert 管理入口。
- `easy-net-hook.dll`：普通 Win32 应用使用的 Winsock Hook DLL。
- `Easy-Net-Lite.exe`：常驻托盘的代理与应用管理程序。
- x64 `windivert` 目录：WinDivert/ProxyBridge 引擎、DLL、驱动和许可证。
- x64 `mihomo` 目录：从 MetaCubeX 官方 GitHub Release 下载的 `mihomo.exe`、许可证和版本信息。

项目不再构建或打包任何 TUN/sing-box 组件。

## 触发构建

推送 `client-hook/**`、`client-lite/**` 或工作流文件到 `main` 会自动触发。也可在 GitHub 的 **Actions → Build Easy-Net Hook → Run workflow** 手动运行。

```powershell
git status
git add client-hook client-lite .github/workflows/client-hook-build.yml
git commit -m "refactor: use WinDivert-only application routing"
git push origin main
```

## 下载产物

| 产物 | 内容与用途 |
| --- | --- |
| `Easy-Net-Hook-x64` | 推荐完整包；包含 Lite、Hook、WinDivert 和独立 `mihomo` 目录。 |
| `Easy-Net-Hook-Win32` | 32 位 Hook 包；不包含 x64 WinDivert 和 Mihomo。 |

x64 解压后应保留：

```text
Easy-Net-Lite.exe
easy-net-hook.exe
easy-net-hook.dll
windivert\
mihomo\mihomo.exe
THIRD-PARTY-LICENSES\
```

不存在第三个 `x64-TUN` 产物，也不应出现 `tun`、`sing-box.exe` 或 `libcronet.dll`。

## Mihomo 下载规则

`client-hook/scripts/install-mihomo.ps1` 固定下载 MetaCubeX/mihomo 官方 Release 的兼容版 Windows amd64 ZIP，并在解压前验证 SHA-256。Mihomo 必须安装到 `mihomo\mihomo.exe`，不得放在应用根目录。

升级 Mihomo 时必须同时修改脚本中的版本号、Release 文件名/URL、官方 SHA-256 digest，并检查许可证地址。下载或校验失败会直接让打包步骤失败，不会生成缺少 Mihomo 的“成功”产物。

## 提交前验证

```powershell
cd client-lite
go test ./...
go vet ./...
cd ..
git diff --check
```

本机具备 MSVC/CMake 时：

```powershell
cd client-hook
.\scripts\build-windows.ps1 -Architecture x64 -Configuration Release -WithMihomo
```

发布前仍应确认 GitHub Actions 的 x64、Win32 构建和测试全部为绿色。
