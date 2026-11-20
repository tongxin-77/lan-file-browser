// 更新横幅（02 文档 5.1）：只管理 index.html 中 #updateBar 挂载点（默认 hidden），
// 并在“当前版本”旁加一个“检查更新”按钮。数据来自 GET /api/admin/update、POST /api/admin/update/check。
// 第一期只提示 + 手动下载：“下载安装包”在新标签页打开下载地址（已按 update.mirror 替换）。
(function () {
  "use strict";

  var bar = document.getElementById("updateBar");
  var SNOOZE_KEY = "lfb.update.snooze"; // {version, until}：“稍后提醒”隐藏一天
  var DAY = 24 * 3600 * 1000;

  function api(method, url) {
    var opt = { method: method, headers: {}, cache: "no-store" };
    if (method !== "GET") {
      opt.headers["X-LFB-Admin"] = "1"; // CSRF，见 internal/api/middleware.go
      opt.headers["Content-Type"] = "application/json";
      opt.body = "{}";
    }
    return fetch(url, opt).then(function (r) {
      if (!r.ok) throw new Error("HTTP " + r.status);
      return r.json();
    });
  }

  function snoozed(v) {
    try {
      var s = JSON.parse(localStorage.getItem(SNOOZE_KEY) || "null");
      return s && s.version === v && Date.now() < s.until;
    } catch (e) { return false; }
  }
  function safeURL(u) { return /^https?:\/\//i.test(u || "") ? u : ""; }

  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }

  function render(st, force) {
    if (!st || !st.hasUpdate || (!force && snoozed(st.latest))) {
      bar.hidden = true;
      return;
    }
    bar.textContent = "";
    var row = el("div", "update-row");
    var msg = el("div", "update-msg");
    msg.appendChild(el("b", null, "有新版本 v" + st.latest));
    msg.appendChild(el("span", null, "当前 v" + st.current + "，下载新安装包覆盖安装即可，片库设置和已配对手机都会保留"));
    row.appendChild(msg);

    var acts = el("div", "update-acts");
    var href = safeURL(st.installerUrl) || safeURL(st.releaseUrl);
    if (href) {
      var a = el("a", "btn main", st.installerUrl ? "下载安装包" : "打开发布页");
      a.href = href;
      a.target = "_blank";
      a.rel = "noopener noreferrer";
      acts.appendChild(a);
    }
    var later = el("button", "btn ghost", "稍后提醒");
    later.type = "button";
    later.addEventListener("click", function () {
      try { localStorage.setItem(SNOOZE_KEY, JSON.stringify({ version: st.latest, until: Date.now() + DAY })); } catch (e) { /* 忽略 */ }
      bar.hidden = true;
    });
    acts.appendChild(later);
    row.appendChild(acts);
    bar.appendChild(row);

    if (st.notes) {
      var d = el("details", "update-notes");
      d.appendChild(el("summary", null, "发布说明"));
      d.appendChild(el("div", "update-notes-body", st.notes)); // 纯文本显示，不解析 Markdown
      bar.appendChild(d);
    }
    bar.hidden = false;
  }

  // “当前版本”旁的“检查更新”按钮
  function addCheckButton() {
    var ver = document.getElementById("ver");
    if (!ver || !ver.parentNode) return;
    var b = el("button", "update-check", "检查更新");
    b.type = "button";
    b.addEventListener("click", function () {
      b.disabled = true;
      b.textContent = "检查中…";
      api("POST", "/api/admin/update/check").then(function (st) {
        render(st, true);
        b.textContent = st.disabled === "dev" ? "开发版不检查" : st.disabled === "config" ? "已关闭检查" :
          st.failed ? "检查失败" : st.hasUpdate ? "有新版本" : "已是最新";
      }, function () {
        b.textContent = "检查失败";
      }).then(function () {
        setTimeout(function () { b.disabled = false; b.textContent = "检查更新"; }, 3000);
      });
    });
    ver.parentNode.appendChild(b);
  }

  if (!bar) return;
  addCheckButton();
  api("GET", "/api/admin/update").then(function (st) { render(st, false); }, function () { /* 静默 */ });
})();
