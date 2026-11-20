# 07 · 注意事项（已知限制、待验证项、后续实现须知）

> 开发过程中发现、需要长期记住的事项。新增功能前先看对应小节；事项解决后在此划掉或删除。

## 1. 尚未验证的测试

| 事项 | 原因 | 如何补测 |
| ---- | ---- | ---- |
| 符号链接测试 `TestSymlinks`（P1 验收要求） | 开发机无创建符号链接权限 | 开启 Windows“开发者模式”或用管理员终端运行 `go test ./internal/fsx/...` |
| 并发数据竞争检测 `go test -race` | 需要 cgo，本机 `CGO_ENABLED=0` 且未装 gcc | 安装 gcc（如 MSYS2 mingw-w64），`CGO_ENABLED=1 go test -race ./...` |
| 8.3 短文件名真实卷测试 | 开发机卷未生成 8.3 短名 | 在开启短名的卷（`fsutil 8dot3name query`）上运行 `TestOpenRejectsShortNames`；当前由第 3 层逻辑测试覆盖 |
| 检查更新与 APK 缓存（02 §5） | 目前只用伪造的 GitHub 服务验证；release.yml 骨架上传的 APK 名为 Flutter 默认的 `app-<abi>-release.apk`，需改名为 `lfb-app-<abi>.apk` 才能被识别 | 首个真实 Release 发布后，用正式版本号构建服务端实测管理页横幅、安装包下载、`update.mirror` 与 `/api/app/latest`、`/api/app/apk/{abi}` |
| 真机 / 真电视效果 | 设计稿、图标只在桌面浏览器中验证 | Flutter 端完成后在 Android 手机、电视盒子上实测 |

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
| 服务端一键更新（02 §5.1 后期） | 本期只做提示 + 手动下载；静默安装（下载 setup.exe → 校验 sha256 → `/VERYSILENT` 启动 → 退出）等 Inno Setup 安装包脚本完成后再做 |
| 照片缩略图方向 | 未处理 EXIF 旋转，竖拍照片缩略图可能横置；后续需读取 EXIF Orientation 后再缩放 |

## 4. 构建与环境

| 事项 | 说明 |
| ---- | ---- |
| Go 版本 | 最低 1.25（`os.Root` 的 Rename / Readlink）；开发机为 1.26 |
| `golang.org/x/sys` | 固定 v0.46.0，更高版本要求 Go 1.26；升级前同步调整 `go.mod` 的 go 版本 |
| 仅 Windows 可编译 | 服务端依赖 `golang.org/x/sys/windows`，CI 必须使用 Windows runner（02 已按此设计） |
| 无零拷贝 | 桌面版 Windows 上 Go 不走 TransmitFile，属预期行为（05 §3） |
| git 仓库 | 公开仓库 https://github.com/tongxin-77/lan-file-browser（Apache-2.0）；本机 ssh 推送需先把 github.com 主机密钥加入 known_hosts（`ssh -T git@github.com` 核对指纹），或 `gh auth refresh -s workflow` 后用 HTTPS 推送（推 `.github/workflows/` 需要 workflow 权限） |

## 5. 播放相关待实测（见 05）

- media_kit 打包的 libmpv 是否使用 libcurl：决定断线重连参数用 `stream-lavf-o` 还是 `curl-max-retries`，目前两组都设置。
- Android WifiLock 是否需要 `WAKE_LOCK` 权限。
- 播放状态面板使用的 mpv 属性名。
- 4K 高码率片源下 150MB 缓存只够约 15 秒，“预读 30–60 秒”只对 1080p 成立。

## 6. 设计稿相关

- 手机 a4 的字母键盘按键宽度小于 48dp 触控下限，实现时改用系统键盘。
- 图标 2 尚未转换为 Android VectorDrawable，也未在真机启动器上验证。
- 设计稿示例数据“第 12 集看到 70%”与“还剩 6 分钟”不一致，实现时以真实进度计算为准。
