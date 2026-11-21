# 07 · 注意事项（已知限制、待验证项、后续实现须知）

> 开发过程中发现、需要长期记住的事项。新增功能前先看对应小节；事项解决后在此划掉或删除。

## 1. 尚未验证的测试

| 事项 | 原因 | 如何补测 |
| ---- | ---- | ---- |
| 符号链接测试 `TestSymlinks`（P1 验收要求） | 开发机无创建符号链接权限 | 开启 Windows“开发者模式”或用管理员终端运行 `go test ./internal/fsx/...` |
| 并发数据竞争检测 `go test -race` | 需要 cgo，本机 `CGO_ENABLED=0` 且未装 gcc | 安装 gcc（如 MSYS2 mingw-w64），`CGO_ENABLED=1 go test -race ./...` |
| 8.3 短文件名真实卷测试 | 开发机卷未生成 8.3 短名 | 在开启短名的卷（`fsutil 8dot3name query`）上运行 `TestOpenRejectsShortNames`；当前由第 3 层逻辑测试覆盖 |
| 检查更新与 APK 缓存（02 §5） | 目前只用伪造的 GitHub 服务验证 | 首个真实 Release 发布后，用正式版本号构建服务端实测管理页横幅、安装包下载、`update.mirror` 与 `/api/app/latest`、`/api/app/apk/{abi}` |
| Windows 安装包（02 §6） | 本机只验证了编译（ISCC 6.7）与 exe 图标；安装需 UAC 提权，未实际安装 | 在 Windows 上实测：覆盖升级 / 卸载时关闭运行中的 `lfb-server.exe`（本机只以非提权的测试安装包验证过事件优雅退出与 taskkill 兜底；需验证提权安装程序能打开普通用户进程创建的 `Local\LanFileBrowserShutdown`，日志 `logs\server.log` 有“正在退出…”即走了优雅路径）、`/VERYSILENT` 覆盖安装后自动重新启动、防火墙规则增删、开机自启、开始菜单“打开管理页”、安装完成后启动并打开管理页、卸载时删除 / 保留数据、首次运行自动打开浏览器 |
| release.yml / ci.yml | 只经 actionlint 检查（本机无 shellcheck，未检查 run 脚本），尚未在 GitHub Actions 上实际运行 | 推送后看 CI 首次运行；首个 tag（可先打 `v0.1.0-beta.1` 预发布）核对产物命名、签名与 checksums |
| 真机 / 真电视效果 | 设计稿、图标只在桌面浏览器中验证 | Flutter 端完成后在 Android 手机、电视盒子上实测 |
| 手机开着 VPN 时绕过（01 §7.1） | 只编译通过 + Dart 单元测试，未上真机 | 真机验证：Clash Meta for Android 开 / 关 allowBypass（“允许应用绕过”）、规则 / 全局模式下能否连上电脑并播放；`bindProcessToNetwork` 对 libmpv 原生 socket 是否生效；不允许绕过时是否显示 VPN 提示且不崩溃；Wi-Fi 断开 / 切换后能否自动重新绑定 |
| 局域网自动发现（01 §2.2） | 只用本机回环 / 本机广播测试，未在真实路由器与手机上验证 | 真机验证：普通家用路由器下 App 广播能否收到回复；路由器开 AP 隔离 / 访客网络 / 屏蔽广播时的表现（预期为空列表，应提示改用扫码）；电脑网络为“公用”时安装包添加的 UDP 8687 规则（所有网络类型、限定程序）是否放行、未装安装包直接运行 exe 时防火墙弹窗只勾“专用”的情况；多网卡（VPN、Hyper-V、WSL）时 255.255.255.255 广播从哪块网卡发出、回复能否到达 |
| 保存连接与多地址重连（01 §6） | 只有单元 / widget 测试 | 真机验证：电脑关机 / 断 Wi-Fi / 开 VPN 时启动停在“正在连接”并自动重试、恢复后自动进首页；电脑端删除本机后提示“已将本机移除”；电脑换 IP 后“修改地址”免配对；覆盖安装新版本后凭据仍在 |
| App 加密片库与 App 内更新（手机端 P2） | 只有单元 / widget 测试，未构建 APK、未上真机 | 真机验证：FLAG_SECURE（最近任务空白、截屏被拒）、切后台 / 锁屏 / 空闲 10 分钟上锁并停止播放、系统键盘数字 / 字母切换、“安装未知应用”授权引导、FileProvider 调起安装器、强制更新不可关闭 |

## 2. 已接受的安全风险

| 风险 | 说明 | 缓解 |
| ---- | ---- | ---- |
| 局域网明文 HTTP | 同网段可抓包获取 token 与文件内容 | 见 01 §4，后续可选证书固定 |
| PIN 可被持续刷新 | 攻击者每 5 分钟累计输错 30 次可让 6 位 PIN 不断更换 | 扫码配对（128 位一次性码）不受影响 |
| 按 IP 锁定可被绕过 | IPv6 隐私地址轮换可换 IP 继续尝试 | 全局 30 次上限兜底 |
| “下载”权限无法杜绝另存 | 能播放即字节已到手机 | 见 03 §3 |
| 加密片库不是文件加密 | 不防能操作电脑的人、抓包、已下载文件 | 见 04 开头 |

## 3. 后续实现须知

| 场景 | 须知 |
| ---- | ---- |
| 上传接口（P3） | 服务端 `ReadTimeout` 为 30 秒，会截断大文件上传；上传处理函数中需用 `http.ResponseController.SetReadDeadline` 按块延长读期限 |
| ~~缩略图接口（P2）~~ | ~~`/api/thumb` 加入 `?t=` 白名单~~：已在 `auth.Service.DeviceFromRequest` 中加入（P2 地基） |
| `?t=` 与 HEAD | Go 的 GET 路由同时匹配 HEAD，`?t=` 在这两个接口的 HEAD 请求上也有效 |
| 请求日志 | 目前没有请求日志中间件；加日志时必须对 `?t=`、`?v=`（加密片库会话）脱敏，且不记录加密片库内的文件名（04） |
| 管理接口安全 | 本机接口有 Host 校验防 DNS 重绑定；修改类请求还需防 CSRF（自定义请求头 + JSON + Origin 校验），新增管理接口必须沿用 |
| 配置并发 | 配置会被管理页修改、被文件接口并发读取，新增读取配置的代码必须使用线程安全的快照方式 |
| 链接解析 | 符号链接 / 目录联接最多跟随 8 跳，只跟随目标仍在同一共享内的链接 |
| 测试用假鉴权 | `TokenMap` 接受任意路由的 `?t=`，仅用于测试，正式运行用 `auth.Service` |
| 设备存储 | 目前为 `devices.json`；P2 引入 SQLite（`data.db`）后可迁移 |
| 配对锁定解除 | 同 IP 锁 5 分钟自动解除；也可在管理页刷新配对码立即解除 |
| 配置保存重试 | Windows 上替换 config.json 时 rename 偶发“拒绝访问”（杀毒/索引占用），`Save` 会重试约 1 秒；其他原子写文件（如 devices.json）遇到同类问题可复用 |
| 管理页：片库大小/文件数 | 未实现（大目录统计开销大），后续需后台异步统计并缓存 |
| 管理页：文件夹选择器 | 不列隐藏文件夹（如 AppData），这类路径需手动粘贴 |
| 管理页：修改片库 | 路径未变时不检查路径是否存在，以便硬盘未连接时仍可改名、改权限 |
| 管理页：可见手机 | `devices` 为空表示全部可见，因此不允许取消勾选最后一台手机，需改选“全部手机” |
| 缩略图缓存上限 | 缓存目录暂无 1 GB 上限与按访问时间淘汰（03 §4.5），会持续增长；后续需加后台清理 |
| 服务端一键更新（02 §5.1 后期） | 本期只做提示 + 手动下载；安装包脚本已完成，静默安装（下载 setup.exe → 校验 sha256 → `/VERYSILENT /SUPPRESSMSGBOXES` 启动 → 退出）可以开始做；注意安装包 `[Run]` 的“启动并打开管理页”带 `skipifsilent`，静默升级后需由安装包或更新逻辑另行重启服务端 |
| 托盘图标（02 §6） | 未做：`-H windowsgui` 构建后没有窗口也没有托盘，只能通过开始菜单“打开管理页”、任务管理器结束进程；托盘（打开管理网页 / 显示二维码 / 退出）需后续实现 |
| 整理：占用超过 24 小时提示（06 §4.2） | 未做“文件已稳定超过 24 小时仍被占用 → 状态列表显示‘被其他程序占用（可能在做种）’及处理建议”；目前只有试运行统一显示“被其他程序占用（可能在下载或做种）” |
| 整理：管理页入口（06 §10） | “设置”页的整理全局默认值界面、新建片库时“作为自动整理库”选项均未做；接口已有（`GET/PUT /api/admin/organize/defaults`、`PUT /api/admin/shares/{id}/organize`） |
| APK 缓存状态 | 只在内存中，服务端重启后需再成功检查一次更新才会重新提供 `/api/app/apk/{abi}`（02 §5.2） |
| ~~release.yml 的 APK 命名~~ | ~~Flutter 默认产物需改名为 `lfb-app-<abi>.apk`~~：release.yml 已改名（02 §3） |
| App：FLAG_SECURE 范围 | 实现为“片库解锁期间整个窗口防截屏”（含解锁后首页、普通共享），比 04 §8 的“处于片库内”更严，避免首页片库名出现在最近任务缩略图 |
| App：片库内容不落盘 | 本地缓存（缩略图磁盘缓存、最近播放等）写入前须用 `LfbClient.isVaultShare(shareId)` 判断并跳过；上锁时已清空 Flutter 图片内存缓存 |
| App：minServerVersion | `lib/services/app_update.dart` 常量，初值 `0.0.0`；App 开始依赖新增服务端接口时提升 |
| 照片缩略图方向 | 未处理 EXIF 旋转，竖拍照片缩略图可能横置；后续需读取 EXIF Orientation 后再缩放 |

## 4. 构建与环境

| 事项 | 说明 |
| ---- | ---- |
| Go 版本 | 最低 1.25（`os.Root` 的 Rename / Readlink）；开发机为 1.26 |
| `golang.org/x/sys` | 固定 v0.46.0，更高版本要求 Go 1.26；升级前同步调整 `go.mod` 的 go 版本 |
| 仅 Windows 可编译 | 服务端依赖 `golang.org/x/sys/windows`，CI 必须使用 Windows runner（02 已按此设计） |
| 无零拷贝 | 桌面版 Windows 上 Go 不走 TransmitFile，属预期行为（05 §3） |
| git 仓库 | 公开仓库 https://github.com/tongxin-77/lan-file-browser（Apache-2.0）；本机 ssh 推送需先把 github.com 主机密钥加入 known_hosts（`ssh -T git@github.com` 核对指纹），或 `gh auth refresh -s workflow` 后用 HTTPS 推送。**当前 gh token 缺 workflow 权限**：经 gh 凭据走 HTTPS 推送含 `.github/workflows/` 改动的提交会被拒绝，需先 `gh auth refresh -h github.com -s workflow`（浏览器授权）或改用 ssh；只推 tag 不受影响 |
| Android 发布 keystore | 开发机 `%USERPROFILE%\.lfb-signing\release.jks`（PKCS12，RSA 4096，别名 `lfb`，30 年），密码在同目录 `signing.txt`，只在本机，不进仓库；已设置仓库 Secrets `ANDROID_KEYSTORE_BASE64` / `ANDROID_KEYSTORE_PASSWORD` / `ANDROID_KEY_ALIAS` / `ANDROID_KEY_PASSWORD`。**必须把 `release.jks` 与 `signing.txt` 离线备份**（如加密 U 盘 + 密码管理器）：丢失后已安装的 App 无法覆盖升级 |
| CI 中的 Flutter 版本 | release.yml / ci.yml 固定 Flutter 3.50.1（与开发机一致），本地升级 Flutter 时同步修改两个 workflow |
| CI 的 Gradle 镜像 | `app/android` 的 Gradle 仓库优先阿里云镜像、官方仓库兜底，GitHub runner 上也可用；若 CI 下载慢可改为仅在本机启用镜像 |

## 5. 播放相关待实测（见 05）

- media_kit 打包的 libmpv 是否使用 libcurl：决定断线重连参数用 `stream-lavf-o` 还是 `curl-max-retries`，目前两组都设置。
- Android WifiLock 是否需要 `WAKE_LOCK` 权限。
- 播放状态面板使用的 mpv 属性名。
- 4K 高码率片源下 150MB 缓存只够约 15 秒，“预读 30–60 秒”只对 1080p 成立。

## 6. 设计稿相关

- 手机 a4 的字母键盘按键宽度小于 48dp 触控下限，实现时改用系统键盘。
- 图标 2 已渲染为 Android 自适应图标（前景 / 背景 / 单色 PNG 图层，`mipmap-anydpi-v26`）与旧版 `ic_launcher.png`、Windows `lfb.ico`，均未在真机启动器 / 桌面上验证；未转 VectorDrawable。
- 设计稿示例数据“第 12 集看到 70%”与“还剩 6 分钟”不一致，实现时以真实进度计算为准。
