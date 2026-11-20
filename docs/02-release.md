# 02 · 发布设计：GitHub Release 与自动更新

> 范围：代码上传 GitHub 后，如何打 tag 自动构建并发布 Release，以及服务端 / App 如何检查并更新。
> 不含：workflow 完整实现（本文仅给骨架）。只做 Android，不涉及 iOS。

## 1. 仓库结构

现有 monorepo（`server/` + `app/` + `docs/`）可直接支持 Release，**无需调整目录**，仅补充：

| 文件 | 作用 |
| ---- | ---- |
| `.github/workflows/release.yml` | push tag `v*` 触发构建与发布 |
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
| Go 注入 | `go build -ldflags "-s -w -X main.version=1.2.0"` |
| Flutter 注入 | `flutter build apk --build-name=1.2.0 --build-number=${{ github.run_number }}`（覆盖 `pubspec.yaml` 中 version） |

> 注：Android 的 `versionCode` 必须单调递增，用 `run_number` 可保证；分 abi 构建时 Flutter 会自动给不同 abi 加偏移。

## 3. CI 流程

```
push tag v* ──┬─> job server (windows, Go 编译 + Inno Setup 打安装包) ──┐
              └─> job app    (ubuntu, Flutter APK)                     ──┴─> job release (汇总产物 → checksums → 发布)
```

| Job | 内容 | 产物 |
| ---- | ---- | ---- |
| server | windows-latest 上编译 exe，用 Inno Setup（runner 已预装）编译 `server/installer/lfb-server.iss` | `lfb-server-setup-<版本>.exe`（安装包） |
| app | `flutter build apk --release --split-per-abi` | `lfb-app-arm64-v8a.apk`、`lfb-app-armeabi-v7a.apk` |
| release | 下载 artifacts → 生成 `checksums.txt`（sha256）→ `softprops/action-gh-release` 上传，`generate_release_notes: true` 自动生成 changelog | GitHub Release 页面 |

APK 签名：

| Secret | 说明 |
| ---- | ---- |
| `ANDROID_KEYSTORE_BASE64` | `base64 -w0 release.jks` 的结果，CI 中解码回 `app/android/release.jks` |
| `ANDROID_KEYSTORE_PASSWORD` / `ANDROID_KEY_ALIAS` / `ANDROID_KEY_PASSWORD` | CI 中写入 `key.properties`，由 `build.gradle` 读取 |

> keystore 务必离线备份：丢失后无法以同签名覆盖安装，用户只能卸载重装。

workflow 骨架（示意，非完整实现）：

```yaml
on: { push: { tags: ['v*'] } }
permissions: { contents: write }
jobs:
  server:
    runs-on: windows-latest
    defaults: { run: { shell: bash } }
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: server/go.mod }
      - run: |
          V=${GITHUB_REF_NAME#v}
          cd server && go build -ldflags "-s -w -H windowsgui -X main.version=$V" \
            -o dist/lfb-server.exe ./cmd/server
          iscc //DAppVersion=$V installer/lfb-server.iss   # 输出 dist/lfb-server-setup-$V.exe
      - uses: actions/upload-artifact@v4
        with: { name: server, path: server/dist/*-setup-*.exe }
  app:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: subosito/flutter-action@v2
        with: { channel: stable }
      - run: echo "${{ secrets.ANDROID_KEYSTORE_BASE64 }}" | base64 -d > app/android/release.jks
      - run: cd app && flutter build apk --release --split-per-abi
               --build-name=${GITHUB_REF_NAME#v} --build-number=${{ github.run_number }}
      - uses: actions/upload-artifact@v4
        with: { name: app, path: app/build/app/outputs/flutter-apk/*-release.apk }
  release:
    needs: [server, app]
    runs-on: ubuntu-latest
    steps:
      - uses: actions/download-artifact@v4
        with: { path: out, merge-multiple: true }
      - run: cd out && sha256sum * > checksums.txt
      - uses: softprops/action-gh-release@v2
        with: { files: out/*, generate_release_notes: true }
```

## 4. 发版操作

| 步骤 | 命令 / 动作 |
| ---- | ---- |
| 1 | 确认 main 分支已合并、测试通过 |
| 2 | `git tag v1.2.0 && git push origin v1.2.0` |
| 3 | 等待 Actions 完成，检查 Release 页面产物与 checksums |
| 4 | 如需修正：删除 Release 与 tag 后重打（仅限未被用户拉取前） |

## 5. 自动更新

### 5.1 服务端（Windows）

| 项 | 设计 |
| ---- | ---- |
| 检查时机 | 启动时 + 每 24h |
| 检查地址 | `GET https://api.github.com/repos/<owner>/<repo>/releases/latest`，取 `tag_name` 与本地 `main.version` 比较（semver） |
| 提示位置 | 本机管理网页 `http://localhost:8686/` 顶部横幅：新版本号、changelog、下载链接 |
| 第一期 | **仅提示 + 手动下载**（用户下载新安装包覆盖安装，配置与数据保留） |
| 后期：一键更新 | 下载 `setup.exe` → 校验 `checksums.txt` 中的 sha256 → 以 `/VERYSILENT /SUPPRESSMSGBOXES` 启动安装包 → 主进程退出 → Inno Setup 覆盖文件并在结束时重启服务端。有安装包后无需自写 updater |
| 开发版 | `version == "dev"` 时不检查 |

### 5.2 App（Android）

| 项 | 设计 |
| ---- | ---- |
| 检查来源（优先） | 通过已连接服务端代理：`GET /api/app/latest`，返回 `{version, apks: {arm64-v8a: url, armeabi-v7a: url}, sha256, notes}`。手机无外网时，服务端可从本地缓存直接提供 APK |
| 检查来源（兜底） | 直接请求 GitHub latest release |
| abi 选择 | 通过 `device_info_plus` 读取 `supportedAbis`，优先 `arm64-v8a` |
| 下载与安装 | 下载 APK → 校验 sha256 → 调用系统安装器（`open_filex` 或 `ota_update` 包） |
| Android 配置 | `REQUEST_INSTALL_PACKAGES` 权限；`FileProvider` 暴露下载目录；首次需用户授予“安装未知应用” |
| 提示方式 | 普通新版本：可关闭的对话框；低于 `minAppVersion`：强提示（不可跳过） |

### 5.3 版本兼容

`/api/ping` 扩展返回：

```json
{ "name": "lfb-server", "version": "1.2.0", "minAppVersion": "1.1.0" }
```

| 情形 | 行为 |
| ---- | ---- |
| App 版本 < `minAppVersion` | App 强提示更新，可从服务端 `/api/app/latest` 直接下载 |
| 服务端版本 < App 声明的 `minServerVersion`（App 内常量） | App 提示“请升级电脑端”，给出 Release 页面链接 |
| 同 MAJOR 且满足以上 | 正常使用 |

### 5.4 网络与限流

| 问题 | 方案 |
| ---- | ---- |
| GitHub API 未鉴权限 60 次/小时/IP | 服务端 24h 一次、App 优先走服务端代理，远低于上限；结果缓存 1h |
| 国内访问 GitHub 慢 / 不通 | 配置项 `update.mirror`（如 GitHub 加速代理前缀或自建镜像），为空时直连；下载后必须校验 sha256 |
| 检查失败 | 静默忽略，记录日志，不影响主功能 |

## 6. Windows 安装包（Inno Setup）

脚本位置：`server/installer/lfb-server.iss`。

| 项 | 设计 |
| ---- | ---- |
| 权限 | `PrivilegesRequired=admin`（添加防火墙规则需要管理员） |
| 安装目录 | `C:\Program Files\LanFileBrowser\` |
| 数据目录 | `%APPDATA%\LanFileBrowser\`（配置、`devices.json`），与程序目录分开，覆盖安装不丢数据；卸载时询问是否删除 |
| 防火墙 | 安装时执行 `netsh advfirewall firewall add rule ...`：TCP 8686、UDP 8687；卸载时删除规则 |
| 开机自启 | 安装页可勾选，写入 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`。**不做 Windows 服务**：服务以 SYSTEM 运行，看不到用户映射的网络盘，也无法显示托盘 |
| 运行形态 | 无控制台窗口（`-H windowsgui`）+ 托盘图标：打开管理网页 / 显示二维码 / 退出 |
| 升级 | `AppId` 固定，同 AppId 覆盖安装；`CloseApplications=yes` 自动关闭运行中的旧进程 |
| 卸载 | 开始菜单 + “设置 → 应用”中的卸载项 |

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
