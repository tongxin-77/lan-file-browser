---
name: release
description: 发布 LAN File Browser 新版本（服务端 Windows 安装包 + Android APK，同一 tag vX.Y.Z）：前置条件检查、发布前检查、草拟 Release Notes、打 tag 推送触发 CI、核对 Release 产物与 checksums、发布后验证自动更新、出错回滚。用户说“发布新版本 / 发版 / 出版本 / 打 release / 打 tag / release vX.Y.Z”时使用。
---

# 发布 LAN File Browser

设计依据：`docs/02-release.md`（以它为准，本 skill 与之冲突时先问用户并同步修正其中一方）。

参数：目标版本号 `vX.Y.Z`。未给出时根据上一个 tag 以来的改动建议一个版本号，**先向用户确认**再继续。

## 规则

- 服务端与 App **共用同一 tag**，语义化版本：
  - MAJOR：接口不兼容（两端必须同时升级）；MINOR：向后兼容的新功能；PATCH：向后兼容的修复。
  - 预发布：`vX.Y.Z-beta.N`，Release 必须标记为 prerelease。
- 版本号**只来自 git tag**：不新增 VERSION 文件，不手改 `pubspec.yaml` 的 version（CI 用 `--build-name` 覆盖），不手改 `main.version`（CI 用 ldflags 注入）。
- 只在 `main` 上发布；工作区有与发版无关的未提交改动时先问用户。
- **对外发布动作必须先得到用户明确确认，每次单独确认**：推送 tag、手动创建/编辑 Release、删除远端 tag 或 Release。确认前把将执行的命令完整展示给用户。
- 签名材料（`release.jks`、`key.properties`、四个 `ANDROID_*` Secret 的值）不得出现在命令行、输出、日志或提交中；只检查“是否存在”，不读取内容。
- gh 在 Git Bash 中可能不在 PATH：`export PATH="$PATH:/c/Program Files/GitHub CLI"`。仓库名取自 `git remote get-url origin`，下文记作 `<repo>`。

## 0. 前置条件检查（必须先做）

```sh
bash .claude/skills/release/scripts/preflight.sh vX.Y.Z
```

脚本只读，检查：git 仓库/origin/main 分支/工作区干净/tag 未占用；`.github/workflows/release.yml`；`server/installer/lfb-server.iss`；`app/pubspec.yaml` 与 Android `build.gradle(.kts)` 的 `key.properties` 签名配置；`.gitignore` 忽略 `*.jks`、`key.properties`、`data/`；gh 登录与 4 个 `ANDROID_*` Secrets。

**退出码非 0 时停止发布**，把缺失项原样告诉用户，并说明各项按 02 文档哪一节补齐（workflow 骨架见 02 §3，安装包见 02 §6，签名见 02 §3“APK 签名”）。不要替用户临时生成这些文件来“凑合发布”，也不要假装已发布；用户明确要求补齐时再作为单独任务处理。

## 1. 发布前检查

逐项执行并记录结果，任一失败即停止并报告：

1. 服务端（在 `server/` 下）：`gofmt -l .` 无输出、`go vet ./...`、`go test ./...` 全部通过。
2. App（在 `app/` 下，Flutter 在 `C:\Users\<用户>\flutter\bin` 时先加入 PATH）：`dart format --output=none --set-exit-if-changed lib test`、`flutter analyze`、`flutter test` 通过。
   另看 main 上最近一次 CI（`ci.yml`）是否通过：`gh run list --repo <repo> --workflow ci.yml --branch main --limit 1`。
3. 阅读 `docs/07-notes.md`，列出与本次发布相关的阻塞/风险项给用户判断，至少包括：
   - §1 尚未验证的测试（符号链接、`-race`、8.3 短名、真机/电视实测）——本次是否需要先补测；
   - §3 中本次新增功能涉及的“后续实现须知”是否已落实（如新增 `/api/thumb` 是否加入 `?t=` 白名单、日志脱敏、管理接口 `adminWrite`）。
4. 对照 docs 与代码：本次改动若改变了接口或行为，相应文档是否已同步更新。

## 2. 草拟 Release Notes

- 项目无 CHANGELOG.md；CI 使用 `generate_release_notes: true` 自动生成提交列表。另外根据 `git log <上一个tag>..HEAD` 与改动，草拟一段给用户看的说明（按 新增 / 变更 / 修复 / 移除 分组，写功能而非提交流水；接口不兼容时写明“需同时升级电脑端与 App”）。
- 草稿写到 scratchpad，先给用户过目；Release 生成后再按用户意见编辑（见第 5 步）。

## 3. minAppVersion 判断

- 本次是否有 App 必须升级才能正常使用的变更（MAJOR，或服务端移除/改变了旧 App 依赖的接口）？
  - 是：需要提升服务端常量 `MinAppVersion`（`server/internal/api/update_handlers.go`，`/api/ping` 与 `/api/app/latest` 返回，02 §5.3）；同理检查 App 常量 `minServerVersion`（`app/lib/services/app_update.dart`）是否需提升。
- 结论告诉用户并确认。

## 4. 打 tag 并推送（对外发布，需用户确认）

确认第 0–3 步均通过、用户已确认版本号后，展示以下命令并**等待用户确认**再执行：

```sh
git tag -a vX.Y.Z -m "vX.Y.Z"
git push origin vX.Y.Z
```

推送后 CI（`.github/workflows/release.yml`）自动构建并创建 Release。跟踪：

```sh
gh run list --repo <repo> --workflow release.yml --limit 3
gh run watch <run-id> --repo <repo>
```

推送 tag 不需要 gh 的 workflow 权限（只有推送 `.github/workflows/` 的改动才需要，见 07-notes §4）。

CI 失败：报告失败的 job 与日志要点，按第 7 步回滚，不要在未修复的情况下重推同名 tag。

## 5. 核对 Release 产物

```sh
gh release view vX.Y.Z --repo <repo> --json tagName,isDraft,isPrerelease,assets,body
```

必须满足：

| 产物 | 要求 |
| ---- | ---- |
| `lfb-server-setup-X.Y.Z.exe` | 存在，大小合理（非几 KB） |
| `lfb-app-arm64-v8a.apk`、`lfb-app-armeabi-v7a.apk` | 按 ABI 拆分，两个都在（文件名以 workflow 实际重命名结果为准，与 02 §3 不符时报告） |
| `checksums.txt` | 存在，包含以上每个文件的 sha256 |
| draft / prerelease | 正式版 `isDraft=false`、`isPrerelease=false`；`-beta.N` 必须 `isPrerelease=true` |

校验 checksums（下载到 scratchpad 的新空目录）：

```sh
gh release download vX.Y.Z --repo <repo> --dir <scratchpad>/vX.Y.Z
cd <scratchpad>/vX.Y.Z && sha256sum -c checksums.txt
```

用户同意后，用第 2 步的草稿补充 Release 说明（对外动作，需确认）：`gh release edit vX.Y.Z --repo <repo> --notes-file <scratchpad>/notes.md`。

## 6. 发布后验证

1. 自动更新数据源能看到新版本（正式版才会出现在 latest）：
   ```sh
   gh api repos/<repo>/releases/latest --jq .tag_name   # 应为 vX.Y.Z
   ```
2. 服务端更新提示（02 §5.1，已实现）：用旧版本安装包运行，本机管理页 `http://127.0.0.1:8686/` 顶部应出现新版本横幅；未实测则如实说明“未验证”。
3. App 更新检查（02 §5.2，已实现）：旧版 App 应提示新版本（经服务端 `/api/app/latest`，服务端需先检查到新版本并缓存 APK）、下载的 APK sha256 与 `checksums.txt` 一致；未实测则如实说明。
4. 建议用户在一台 Windows 上覆盖安装 exe、在一台 Android 设备上覆盖安装 APK（验证签名一致、可覆盖升级、配置与数据保留）。

## 7. 出错回滚（对外操作，需用户确认）

可以删除重打的条件（02 §4）：**仅限用户尚未拉取前**——Release 刚发布、`releases/latest` 尚未被服务端/App 检查到（发布后很短时间内，且无用户反馈已下载）。否则不要删，改发 PATCH 版本修复。

步骤（每条命令展示给用户并确认后执行）：

```sh
gh release delete vX.Y.Z --repo <repo> --yes   # 删除 Release（若已创建）
git push origin :refs/tags/vX.Y.Z              # 删除远端 tag
git tag -d vX.Y.Z                              # 删除本地 tag
```

修复问题、重新走第 0–1 步后再打同名 tag。注意 APK 的 `versionCode` 来自 `run_number`，重打会自动递增，无需处理。

## 汇报

给出：版本号与 Release 链接、产物列表与大小、checksums 校验结果、`releases/latest` 验证结果、07-notes 中未解决的风险项；如实说明哪些更新检查功能未经手动验证。
