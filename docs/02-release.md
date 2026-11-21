# 02 · 发布设计：GitHub Release 与自动更新

> 范围：代码上传 GitHub 后，如何打 tag 自动构建并发布 Release，以及服务端 / App 如何检查并更新。
> 实现：`.github/workflows/release.yml`（发布）、`.github/workflows/ci.yml`（main 分支检查）、`server/installer/lfb-server.iss`（安装包）。只做 Android，不涉及 iOS。

## 1. 仓库结构

现有 monorepo（`server/` + `app/` + `docs/`）可直接支持 Release，**无需调整目录**，仅补充：

| 文件 | 作用 |
| ---- | ---- |
| `.github/workflows/release.yml` | push tag `v*` 触发构建与发布 |
| `.github/workflows/ci.yml` | push / PR 到 main：服务端 gofmt、go vet、go test（windows-latest）；App dart format、flutter analyze、flutter test（ubuntu-latest） |
| `.gitignore`（根目录） | 忽略 Go 的 `dist/`、Flutter 的 `build/` `.dart_tool/` `.flutter-plugins*`、运行数据 `data/`、签名文件 `*.jks` `key.properties` |
| 版本来源 | **以 git tag 为准**，不另设 `VERSION` 文件（避免两处不一致）；本地开发版本默认 `dev` |

## 2. 版本策略

服务端与 App **共用同一版本号、同一 tag**（如 `v1.2.0`），遵循语义化版本。

| 项 | 约定 |
| ---- | ---- |
| 理由 | 接口协议同步演进，一个号即可对应一套兼容的服务端 + App，简单 |
| 格式 | `vMAJOR.MINOR.PATCH`；预发布用 `v1.2.0-beta.1`（Release 标记为 prerelease） |
| MAJOR | 接口不兼容变更（需同时升级两端） |
| MINOR / PATCH | 向后兼容的功能 / 修复 |
| Go 注入 | `go build -ldflags "-s -w -H windowsgui -X main.version=1.2.0"` |
| Flutter 注入 | `flutter build apk --build-name=1.2.0 --build-number=${{ github.run_number }}`（覆盖 `pubspec.yaml` 中 version） |

> 注：Android 的 `versionCode` 必须单调递增，用 `run_number` 可保证；分 abi 构建时 Flutter 会自动给不同 abi 加偏移。

## 3. CI 流程

```
push tag v* ──┬─> job server (windows, Go 编译 + Inno Setup 打安装包) ──┐
              └─> job app    (ubuntu, Flutter APK)                     ──┴─> job release (汇总产物 → checksums → 发布)
```

| Job | 内容 | 产物 |
| ---- | ---- | ---- |
| server | windows-latest：`setup-go`（`go-version-file: server/go.mod`）→ `go test ./...` → `go build -trimpath -ldflags "-s -w -H windowsgui -X main.version=<版本>"` → `iscc /DAppVersion=<版本> installer\lfb-server.iss`（Inno Setup 6 为 runner 预装，`iscc` 在 PATH 中；在 pwsh 中执行，避免 Git Bash 把 `/D` 当路径转换） | `lfb-server-setup-<版本>.exe`（安装包） |
| app | ubuntu-latest：Java 17（temurin）+ `subosito/flutter-action`（stable，固定 3.50.1）→ 从 Secrets 写入 keystore 与 `key.properties` → `flutter build apk --release --split-per-abi --target-platform android-arm,android-arm64 --build-name=<版本> --build-number=<run_number>` → 把 `app-<abi>-release.apk` 改名 | `lfb-app-arm64-v8a.apk`、`lfb-app-armeabi-v7a.apk`（x86_64 不发布） |
| release | 下载两个 artifact → `sha256sum lfb-server-setup-*.exe lfb-app-*.apk > checksums.txt` → `softprops/action-gh-release` 上传，`generate_release_notes: true`；tag 含 `-`（如 `v0.1.0-beta.1`）时 `prerelease: true` | GitHub Release 页面 |

`<版本>` 为 tag 去掉前缀 `v`。资产名必须与 `server/internal/update` 的识别规则一致：安装包 `lfb-server-setup-*.exe`、APK `lfb-app-<abi>.apk`、`checksums.txt`（`<sha256>  <文件名>`）。

第三方 action 均固定主版本：`actions/checkout@v7`、`actions/setup-go@v7`、`actions/setup-java@v6`、`subosito/flutter-action@v2`、`actions/upload-artifact@v7`、`actions/download-artifact@v8`、`softprops/action-gh-release@v3`（2026-11 时的最新主版本）。workflow 顶层权限 `contents: read`，只有 release job 为 `contents: write`。

APK 签名：

| Secret | 说明 |
| ---- | ---- |
| `ANDROID_KEYSTORE_BASE64` | `release.jks` 的 base64，CI 中解码回 `app/android/release.jks` |
| `ANDROID_KEYSTORE_PASSWORD` / `ANDROID_KEY_ALIAS` / `ANDROID_KEY_PASSWORD` | CI 中写入 `app/android/key.properties`（`storeFile=release.jks`，相对 `app/android/`），由 `app/android/app/build.gradle.kts` 读取；任一 Secret 为空时 job 失败 |

- `key.properties` 不存在时 release 构建回退到 debug 签名（便于本地开发）；本地要出正式签名的 APK，在 `app/android/key.properties` 中把 `storeFile` 写成 keystore 的绝对路径。
- keystore 为 PKCS12、RSA 4096、别名 `lfb`、有效期 30 年，存放在仓库之外（位置与备份见 07-notes §4）。

> keystore 务必离线备份：丢失后无法以同签名覆盖安装，用户只能卸载重装。

## 4. 发版操作

| 步骤 | 命令 / 动作 |
| ---- | ---- |
| 1 | 确认 main 分支已合并、测试通过 |
| 2 | `git tag -a v1.2.0 -m v1.2.0 && git push origin v1.2.0`（推送 tag 只需 contents 权限；推送 `.github/workflows/` 的改动才需要 workflow 权限） |
| 3 | 等待 Actions 完成，检查 Release 页面产物与 checksums |
| 4 | 如需修正：删除 Release 与 tag 后重打（仅限未被用户拉取前） |

## 5. 自动更新

### 5.1 服务端（Windows）

| 项 | 设计 |
| ---- | ---- |
| 检查时机 | 启动时 + 每 24h |
| 检查地址 | `GET https://api.github.com/repos/<owner>/<repo>/releases/latest`，取 `tag_name` 与本地 `main.version` 比较（semver） |
| 提示位置 | 本机管理网页 `http://localhost:8686/` 顶部横幅：新版本号、changelog（折叠）、“下载安装包”（新标签页打开）、“稍后提醒”（同一版本隐藏 1 天）；“当前版本”旁有“检查更新” |
| 接口（仅本机） | `GET /api/admin/update` → `{current, latest, hasUpdate, notes, installerUrl, releaseUrl, checkedAt, failed, disabled}`（`disabled`：`dev` 开发版 / `config` 已关闭）；`POST /api/admin/update/check` 立即检查（CSRF 同其他修改类接口） |
| 资产识别 | 安装包 `lfb-server-setup-*.exe`；APK `lfb-app-<abi>.apk`；`checksums.txt`（sha256sum 格式） |
| 第一期 | **仅提示 + 手动下载**（用户下载新安装包覆盖安装，配置与数据保留） |
| 后期：一键更新 | 下载 `setup.exe` → 校验 `checksums.txt` 中的 sha256 → 以 `/VERYSILENT /SUPPRESSMSGBOXES` 启动安装包 → 主进程退出 → Inno Setup 覆盖文件并在结束时重启服务端。有安装包后无需自写 updater |
| 开发版 | `version == "dev"` 时不检查 |

### 5.2 App（Android）

| 项 | 设计 |
| ---- | ---- |
| 检查来源（优先） | 通过已连接服务端代理：`GET /api/app/latest`（需设备鉴权），返回 `{version, minAppVersion, notes, apks: {abi: url}, sha256: {abi: hash}}`；尚无版本信息时 404。`apks` 只列已缓存且校验通过的 ABI，url 指向本服务 `GET /api/app/apk/{abi}`（支持 Range，可用 `?t=`），为空时走兜底 |
| APK 缓存 | 服务端检查到最新 Release（不论服务端自身是否需要更新）即把 APK 下载到 `<数据目录>\cache\apk\<版本>\`，按 `checksums.txt` 校验 sha256，失败删除、缺校验值不缓存；只保留最新一个版本。缓存状态在内存中，重启后需再次检查成功才提供 |
| 检查来源（兜底） | 直接请求 GitHub latest release |
| abi 选择 | 通过 `device_info_plus` 读取 `supportedAbis`，优先 `arm64-v8a` |
| 下载与安装 | 下载 APK → 校验 sha256 → 调用系统安装器（`open_filex` 或 `ota_update` 包） |
| Android 配置 | `REQUEST_INSTALL_PACKAGES` 权限；`FileProvider` 暴露下载目录；首次需用户授予“安装未知应用” |
| 提示方式 | 普通新版本：可关闭的对话框；低于 `minAppVersion`：强提示（不可跳过） |
| 手动检查 | 首页右上角“设置”面板显示当前 App 版本，“检查更新”立即请求 `/api/app/latest`：有新版弹更新框；已是最新、服务端暂无版本信息（404）、连接失败都在面板内显示结果 |

### 5.3 版本兼容

`/api/ping` 扩展返回：

```json
{ "id": "…", "name": "电脑名", "version": "1.2.0", "minAppVersion": "1.1.0", "needPair": false }
```

`minAppVersion` 为服务端代码常量 `api.MinAppVersion`（初值 `0.0.0`）：仅当服务端接口发生旧版 App 无法兼容的变更（删除/改名字段、改语义）时提升到第一个兼容的 App 版本；新增字段不提升。

| 情形 | 行为 |
| ---- | ---- |
| App 版本 < `minAppVersion` | App 强提示更新，可从服务端 `/api/app/latest` 直接下载 |
| 服务端版本 < App 声明的 `minServerVersion`（App 内常量） | App 提示“请升级电脑端”，给出 Release 页面链接 |
| 同 MAJOR 且满足以上 | 正常使用 |

### 5.4 网络与限流

| 问题 | 方案 |
| ---- | ---- |
| GitHub API 未鉴权限 60 次/小时/IP | 服务端启动时 + 24h 一次、App 优先走服务端代理，远低于上限；结果缓存 1h（管理页 / App 请求距上次检查超过 1h 才在后台重新检查，“检查更新”按钮除外）；请求带 `User-Agent: lfb-server` |
| 国内访问 GitHub 慢 / 不通 | `config.json` 的 `update.mirror`：把资产下载地址中的 `https://github.com/` 替换为该前缀（加速代理填 `https://代理/https://github.com/`，自建镜像按 GitHub 路径布局），为空时直连；API 请求不走镜像；下载后必须校验 sha256。`update.enabled`（默认 `true`）为 `false` 时不检查 |
| 检查失败 | 静默忽略，记录日志，不影响主功能 |

## 6. Windows 安装包（Inno Setup）

脚本位置：`server/installer/lfb-server.iss`。

| 项 | 设计 |
| ---- | ---- |
| 权限 | `PrivilegesRequired=admin`（添加防火墙规则需要管理员）；仅 x64 |
| 安装目录 | `C:\Program Files\LanFileBrowser\` |
| 数据目录 | `%APPDATA%\LanFileBrowser\`（配置、`devices.json`、`data.db`、无控制台运行时的日志 `logs\server.log`），缩略图缓存在 `%LOCALAPPDATA%\LanFileBrowser\`；与程序目录分开，覆盖安装不丢数据；卸载时询问是否删除这两个目录（默认“否”，静默卸载保留） |
| 防火墙 | 安装时 `netsh advfirewall firewall add rule`：入站 TCP 8686、UDP 8687（规则名 `LAN File Browser (TCP 8686)` / `(UDP 8687)`，限定程序 `lfb-server.exe`，所有网络类型），先删同名规则再添加；卸载时删除 |
| 开机自启 | 安装页可勾选（默认不勾），写入 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`，卸载时删除。**不做 Windows 服务**：服务以 SYSTEM 运行，看不到用户映射的网络盘，也无法显示托盘 |
| 开始菜单 | 启动服务端、“打开管理页”（`http://127.0.0.1:8686/` 的 URL 快捷方式）、卸载 |
| 安装完成 | 可选“启动并打开管理页”：以安装者本人（非提升权限）运行 `lfb-server.exe -no-browser`，等 `/api/ping` 就绪（最多约 10 秒）后打开管理页 |
| 运行形态 | 无控制台窗口（`-H windowsgui`）；日志写入数据目录 `logs\server.log`（超过 10MB 启动时轮换为 `.1`）；首次运行（数据目录下尚无 `config.json`）时自动用默认浏览器打开管理页，`-no-browser` 禁用。托盘图标（打开管理网页 / 显示二维码 / 退出）**尚未实现** |
| 图标 | `server/installer/lfb.ico`（图标 2，16–256 多尺寸）：安装包 `SetupIconFile`；exe 通过 `server/cmd/server/rsrc_windows_amd64.syso` 嵌入（`go run github.com/akavel/rsrc@v0.10.2 -arch amd64 -ico installer/lfb.ico -o cmd/server/rsrc_windows_amd64.syso` 生成，更换图标时重新生成） |
| 界面语言 | 简体中文：`server/installer/ChineseSimplified.isl`（Inno Setup 非官方翻译，官方安装包不带，随仓库提供），需 Inno Setup 6.5+ |
| 升级 | `AppId` 固定，同 AppId 覆盖安装；`CloseApplications=yes` 自动关闭运行中的旧进程 |
| 卸载 | 开始菜单 + “设置 → 应用”中的卸载项；卸载前结束 `lfb-server.exe` 进程 |

本地编译安装包（在 `server/` 下）：`go build -ldflags "-s -w -H windowsgui -X main.version=0.1.0" -o dist/lfb-server.exe ./cmd/server`，再 `iscc /DAppVersion=0.1.0 installer\lfb-server.iss`，输出 `dist\lfb-server-setup-0.1.0.exe`。

> 安装包未做代码签名，首次运行 SmartScreen 会提示“未知发布者”，需点“仍要运行”；后期可考虑购买代码签名证书。

## 7. 实施分期

| 期 | 内容 |
| ---- | ---- |
| 一期 | `.gitignore`、release.yml、版本注入、APK 签名、checksums；服务端更新提示；`/api/ping` 返回版本 |
| 二期 | App 内检查更新 + 下载安装；`/api/app/latest` 代理；`minAppVersion` 强提示 |
| 三期 | 服务端自动替换 exe（updater）；更新镜像配置 |

## 8. 已确认决策

| # | 问题 | 结论 |
| ---- | ---- | ---- |
| 1 | 仓库公开 / 私有 | **公开**，Release 可匿名下载，自动更新直接走 GitHub API |
| 2 | 服务端分发形式 | **Inno Setup 安装包**（防火墙规则、开机自启、托盘） |
| 3 | 平台 | **只做 Android**，不涉及 iOS 分发 |
