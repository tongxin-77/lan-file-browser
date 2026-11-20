#!/usr/bin/env bash
# 发布前置条件检查（只读，不修改任何文件、不推送）。
# 用法：在仓库根目录执行  bash .claude/skills/release/scripts/preflight.sh vX.Y.Z
# 全部通过退出码 0；有缺失时逐条列出并退出 1。
set -u

TAG="${1:-}"
missing=()
warn=()
miss() { missing+=("$1"); }

# 1. 版本号格式
if [[ ! "$TAG" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.]+)?$ ]]; then
  miss "版本号格式错误：'$TAG'，应为 vX.Y.Z 或 vX.Y.Z-beta.N"
fi

# 2. git 仓库与远端
if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  miss "当前目录不是 git 仓库（07-notes §4：尚未使用 git），需先初始化并推送到 GitHub"
else
  git remote get-url origin >/dev/null 2>&1 || miss "未配置 origin 远端（GitHub 仓库）"
  branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null)
  [ "$branch" = "main" ] || miss "当前分支为 '$branch'，只能在 main 上发布"
  [ -z "$(git status --porcelain)" ] || miss "工作区不干净（git status 有未提交改动）"
  if [ -n "$TAG" ]; then
    git rev-parse -q --verify "refs/tags/$TAG" >/dev/null && miss "本地已存在 tag $TAG"
    if git remote get-url origin >/dev/null 2>&1; then
      git ls-remote --exit-code --tags origin "refs/tags/$TAG" >/dev/null 2>&1 && miss "远端已存在 tag $TAG"
      git fetch -q origin main 2>/dev/null && [ "$(git rev-parse HEAD)" != "$(git rev-parse origin/main)" ] \
        && miss "本地 main 与 origin/main 不一致（先推送或拉取）"
    fi
  fi
fi

# 3. 发布所需文件（02-release §1、§3、§6）
[ -f .github/workflows/release.yml ] || miss "缺少 .github/workflows/release.yml（02 §3：server / app / release 三个 job）"
[ -f server/installer/lfb-server.iss ] || miss "缺少 server/installer/lfb-server.iss（02 §6：Inno Setup 脚本，接收 /DAppVersion）"
[ -f app/pubspec.yaml ] || miss "缺少 app/pubspec.yaml（Flutter 工程尚未 flutter create）"
gradle=$(ls app/android/app/build.gradle app/android/app/build.gradle.kts 2>/dev/null | head -1)
if [ -z "$gradle" ]; then
  miss "缺少 app/android/app/build.gradle(.kts)（Android 工程未生成）"
elif ! grep -q "key.properties" "$gradle"; then
  miss "$gradle 未读取 key.properties 的 release 签名配置（02 §3 APK 签名）"
fi
for p in '*.jks' 'key.properties' 'data/'; do
  grep -qxF "$p" .gitignore 2>/dev/null || miss ".gitignore 未忽略 $p（02 §1）"
done

# 4. GitHub CLI 与签名 Secrets
if ! command -v gh >/dev/null 2>&1; then
  miss "找不到 gh（Git Bash 中可能需 export PATH=\"\$PATH:/c/Program Files/GitHub CLI\"）"
elif ! gh auth status >/dev/null 2>&1; then
  miss "gh 未登录（gh auth login）"
elif git remote get-url origin >/dev/null 2>&1; then
  secrets=$(gh secret list 2>/dev/null | awk '{print $1}')
  for s in ANDROID_KEYSTORE_BASE64 ANDROID_KEYSTORE_PASSWORD ANDROID_KEY_ALIAS ANDROID_KEY_PASSWORD; do
    grep -qx "$s" <<<"$secrets" || miss "仓库缺少 Actions Secret：$s"
  done
fi

# 5. 非阻塞提示
grep -q '"minAppVersion"' server/internal/api/auth_handlers.go 2>/dev/null \
  || warn+=("/api/ping 尚未返回 minAppVersion（02 §5.3，二期），本次无法通过它强制 App 升级")

for w in "${warn[@]}"; do echo "提示: $w"; done
if [ ${#missing[@]} -gt 0 ]; then
  echo "前置条件未满足（${#missing[@]} 项）："
  for m in "${missing[@]}"; do echo "  - $m"; done
  exit 1
fi
echo "前置条件检查通过：$TAG"
