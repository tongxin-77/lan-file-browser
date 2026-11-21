# LAN File Browser

局域网文件浏览器：Windows 电脑运行 Go 服务端（单 exe + 本机管理网页），Android 手机 / 电视用 Flutter App 浏览共享目录、播放视频（自动下一集）、看图。只做 Android，不做 iOS。

## 目录结构

```
server/                     Go 服务端，module lfb/server
  cmd/server/               入口 main.go（flag：-data 数据目录、-port 端口、-no-browser；main.version 由 ldflags 注入，默认 dev）；rsrc_windows_amd64.syso 为 exe 图标
  internal/api/             HTTP 路由（server.go）、鉴权与本机/CSRF 中间件（middleware.go）、统一错误码（errors.go）
  internal/auth/            设备配对（PIN / 一次性码）、token 存储 devices.json、请求鉴权
  internal/config/          config.json 读写；Store 提供不可变快照 + 原子更新
  internal/fsx/             共享目录内的安全文件访问（词法校验 → os.Root → 最终路径校验、隐藏项）
  internal/listing/         目录列表自然排序
  internal/media/           按扩展名判定类别与 MIME
  internal/web/             内嵌本机管理页静态文件（static/：原生 HTML/CSS/JS）
  internal/discovery/       局域网自动发现：UDP 8687 应答 "LFB_DISCOVER"（01 §2.2）
  configs/                  配置示例（尚为空）
  installer/                Inno Setup 脚本 lfb-server.iss、简体中文语言文件、图标 lfb.ico（02 §6）
app/                        Flutter 工程 lfb_app（lib/ 下 api models pages widgets services；Android 原生代码在 android/app/src/main/kotlin）
docs/                       设计文档 01–07
docs/desgin/                设计稿、图标、DECISIONS.md（目录名就是 desgin，保持原拼写，勿改名）
.github/workflows/          release.yml（tag v* 发布）、ci.yml（main 分支检查）
```

## 常用命令

服务端（在 `server/` 下执行）：

```sh
gofmt -l .                                  # 应无输出
go vet ./...
go test ./...
go build -o dist/lfb-server.exe ./cmd/server
go run ./cmd/server -data devdata -port 8686   # 本地调试；devdata/ 已在 .gitignore
```

- 管理页仅本机访问：`http://127.0.0.1:8686/`
- 符号链接测试需开发者模式或管理员终端；`-race` 需 cgo + gcc（见 07-notes §1）

App（在 `app/` 下执行；Flutter 3.50+，Gradle 依赖优先走阿里云镜像）：

```sh
dart format --output=none --set-exit-if-changed lib test
flutter analyze
flutter test
flutter build apk --release --split-per-abi
```

## 开发约定（硬规则）

### 安全

- 面向设备的共享内文件访问一律走 `internal/fsx`（`fsx.Open` / `fsx.OpenFinal`，基于 `os.Root`），不得直接 `os.Open` 拼接路径。例外仅限本机管理页的文件夹选择器等 localOnly 接口。
- 新增本机管理接口：读接口包 `localOnly`，修改类接口包 `s.adminWrite`（Host 校验 + `X-LFB-Admin` 头 + JSON + Origin 校验防 CSRF）。
- `?t=` token 只对 GET/HEAD 的 `/api/file`、`/api/download`、`/api/thumb`、`/api/app/apk/{abi}` 生效（`auth.Service.DeviceFromRequest`）；新增需要 URL 直链的接口必须显式加入白名单并补测试。
- 不可见共享、无会话访问的加密片库、隐藏/系统项、链接逃逸：一律 `404 NOT_FOUND`，与“不存在”完全一致；`403` 仅用于“可见但无该操作权限”（03 §3、04）。
- 错误 `message` 不含真实路径；日志必须对 `?t=`、`?v=` 脱敏，且不记录加密片库内文件名。
- `TokenMap` 只用于测试，正式运行用 `auth.Service`。

### 并发与配置

- 读配置用 `config.Store.Get()` 取快照（只读，不得修改）；修改一律经 `Store.Update`。
- 原子写文件在 Windows 上可能偶发 rename“拒绝访问”，参考 `config.Save` 的重试。

### 播放与 HTTP

- 视频不转码、不压缩：任何压缩中间件必须排除 `/api/file`、`/api/download`、`/api/speedtest`；保持 `ServeContent` 的 `ReadFrom` 快路径。
- 不设全局 `WriteTimeout`（会切断长视频）；`ReadTimeout` 30s，上传接口需用 `http.ResponseController` 按块延长读期限。

### 数据存储

- SQLite：`internal/store`（`modernc.org/sqlite` v1.55.0，`<数据目录>\data.db`，WAL）。每个功能新建自己的 `internal/store/migrations_<功能>.go`，在 `init()` 中 `store.Register(store.Migration{Version, Name, Up})`；版本号段：1 初始、100–199 进度、200–299 整理、300–399 缩略图、400–499 设备迁入；不改已发布的迁移。设备目前仍存 `devices.json`。
- modernc.org/sqlite 不要升到 v1.56+（要求 x/sys v0.47 / Go 1.26）。

### 构建环境

- Go 最低 1.25（`os.Root` 的 Rename/Readlink）；`golang.org/x/sys` 固定 v0.46.0（更高版本要求 Go 1.26，升级前先改 `go.mod` 的 go 版本）。
- 服务端依赖 `golang.org/x/sys/windows`，只能在 Windows 编译；CI 用 Windows runner。
- App：Flutter 3.50 起 Material 组件从 `package:material_ui/material_ui.dart` 导入（`flutter create` 默认）；`media_kit_libs_android_video` 构建时从 GitHub Releases 下载 libmpv jar。
- 版本号以 git tag 为准，不设 VERSION 文件；服务端与 App 共用同一 tag `vX.Y.Z`（发布见 `/release` skill 与 docs/02）。

### 流程

- TDD：安全相关规则（路径校验、鉴权、404 隐藏、CSRF、脱敏）先写失败测试再实现；Bug 先写复现测试。
- 文件统一 UTF-8、LF 换行（由 `.gitattributes` 强制；`.bat`、`.cmd`、`.ps1` 为 CRLF）。
- 新增功能前先读对应设计文档与 `docs/07-notes.md` 相应小节；实现与文档不一致时同步更新文档，事项解决后在 07-notes 划掉或删除。
- `server/`、`app/` 可能有其他人同时在改：只改任务涉及的文件。

## 提交规范

- 提交信息用 Conventional Commits：`<type>(<scope>): <中文描述>`，首行 ≤ 72 字符；type ∈ feat fix docs style refactor perf test build ci chore revert；scope 建议 server / app / web / docs / design / release / ci。例：`feat(server): 添加缩略图接口`。
- 提交前必须通过格式化与测试（服务端 `gofmt -l .` 无输出、`go vet ./...`、`go test ./...`；App `dart format`、`flutter analyze`、`flutter test`）。
- 首次克隆后运行 `git config core.hooksPath .githooks` 启用 pre-commit（格式检查）与 commit-msg（提交信息校验）钩子；不要用 `--no-verify` 绕过。

## 文档索引

| 文档 | 内容 |
| ---- | ---- |
| `docs/01-connection.md` | 发现、配对、token、断线重连 |
| `docs/02-release.md` | 版本策略、CI、Release 产物、自动更新、Inno Setup |
| `docs/03-file-api.md` | 文件接口、路径安全、权限、错误码、观看进度 |
| `docs/04-vault.md` | 加密片库（锁定时完全隐藏） |
| `docs/05-playback.md` | 播放稳定性、不压缩、超时 |
| `docs/06-organizer.md` | 整理库 |
| `docs/07-notes.md` | 待验证测试、已接受风险、后续实现须知、构建环境 |
| `docs/desgin/DECISIONS.md` | 已选定的设计稿与图标 |
