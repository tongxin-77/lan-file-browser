// 加密片库设置（04 文档）：只管理 index.html 中 #vaultPanel 挂载点（默认 hidden）。
// app.js 每次重绘放映单后派发 document 事件 "lfb:sheet"，event.detail.share 为当前片库（GET /api/admin/shares 的条目）或 null。
// 管理接口修改类请求须带 X-LFB-Admin: 1 与 Content-Type: application/json（CSRF，见 internal/api/middleware.go）。
// 挂载点位于 #sheet 内，app.js 在 #sheet 上监听 click（只认 .sw[data-perm]、[data-who]、#removeBtn）与
// change（只认 data-phone）：本文件的元素不要使用这些属性/ID。
// 海报上的“加密”标记：观察 #wall 重绘后补上（app.js 不感知加密状态）。
(function () {
  "use strict";

  var $ = function (id) { return document.getElementById(id); };
  var panel = $("vaultPanel");
  var current = null; // 当前片库（app.js 的对象，加密状态改后同步写回 .vault）
  var vaults = {};    // shareId → 是否加密

  var MARK = '<span class="vault-mark" title="加密片库：手机和电视上要输密码才出现">' +
    '<svg viewBox="0 0 10 11" aria-hidden="true"><path d="M2.5 5V3.5a2.5 2.5 0 0 1 5 0V5" fill="none" stroke="currentColor" stroke-width="1.4"/>' +
    '<rect x="1" y="5" width="8" height="6" rx="1.2" fill="currentColor"/></svg>加密</span>';

  function esc(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }
  function api(method, url, body) {
    var opt = { method: method, headers: {}, cache: "no-store" };
    if (method !== "GET") {
      opt.headers["X-LFB-Admin"] = "1";
      opt.headers["Content-Type"] = "application/json";
      opt.body = JSON.stringify(body || {});
    }
    return fetch(url, opt).then(function (r) {
      return r.json().then(function (j) {
        if (!r.ok) throw new Error((j && j.error && j.error.message) || "出错了（" + r.status + "）");
        return j;
      });
    });
  }
  var toastTimer;
  function toast(msg) {
    var t = $("toast");
    t.textContent = msg;
    t.classList.add("show");
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { t.classList.remove("show"); }, 2200);
  }

  /* ---------- 弹窗（设置/修改密码、取消加密确认） ---------- */
  var dlgs = document.createElement("div");
  dlgs.innerHTML =
    '<dialog id="vaultDlg"><form class="dlg" id="vaultForm" novalidate>' +
      '<h3 id="vTitle"></h3><p id="vText"></p>' +
      '<label class="field"><span>密码 <em>4–16 位，可以只用数字</em></span>' +
        '<input type="password" id="vPw1" maxlength="16" autocomplete="new-password"></label>' +
      '<label class="field"><span>再输一次</span>' +
        '<input type="password" id="vPw2" maxlength="16" autocomplete="new-password"></label>' +
      '<div class="err" id="vErr" role="alert"></div>' +
      '<div class="dlg-actions"><button type="button" class="btn ghost" data-vclose>取消</button>' +
        '<button type="submit" class="btn main" id="vOk"></button></div>' +
    '</form></dialog>' +
    '<dialog id="vaultOffDlg"><div class="dlg">' +
      '<h3 id="vOffTitle"></h3>' +
      '<p>取消后，能看到这个片库的手机和电视会直接在首页看到它，不用再输密码。<strong>电脑上的文件不受影响。</strong></p>' +
      '<div class="dlg-actions"><button class="btn ghost" data-vclose>先不了</button>' +
        '<button class="btn danger solid" id="vOffOk">取消加密</button></div>' +
    '</div></dialog>';
  while (dlgs.firstChild) document.body.appendChild(dlgs.firstChild);
  document.querySelectorAll("[data-vclose]").forEach(function (b) {
    b.addEventListener("click", function () { b.closest("dialog").close(); });
  });

  var target = null; // 弹窗打开时的片库
  function openSet(f) {
    target = f;
    var n = esc(f.name);
    $("vTitle").textContent = f.vault ? "修改“" + f.name + "”的密码" : "给“" + f.name + "”设密码";
    $("vText").innerHTML = f.vault
      ? "不需要旧密码，直接设新的。改好后，手机和电视要输新密码才能看到“" + n + "”。"
      : "设好后，手机和电视上平时看不到“" + n + "”。在首页长按电脑名称（电视上长按遥控器的菜单键）输入这个密码，它才会出现。<strong>电脑上的文件不会被加密或改动。</strong>";
    $("vOk").textContent = f.vault ? "保存新密码" : "设为加密片库";
    $("vOk").disabled = false;
    $("vPw1").value = ""; $("vPw2").value = ""; $("vErr").textContent = "";
    $("vaultDlg").showModal();
    $("vPw1").focus();
  }
  function openOff(f) {
    target = f;
    $("vOffTitle").textContent = "取消“" + f.name + "”的加密？";
    $("vOffOk").disabled = false;
    $("vaultOffDlg").showModal();
  }

  function applied(f, s) {
    f.vault = s.vault;
    vaults[f.id] = s.vault;
    markWall();
    if (current === f) render();
  }

  $("vaultForm").addEventListener("submit", function (e) {
    e.preventDefault();
    var f = target, p1 = $("vPw1").value, p2 = $("vPw2").value;
    var len = Array.from(p1).length;
    if (len < 4 || len > 16) {
      $("vErr").textContent = "密码要 4 到 16 位";
      $("vPw1").focus();
      return;
    }
    if (p1 !== p2) {
      $("vErr").textContent = "两次输入的密码不一样，请再输一次";
      $("vPw2").value = "";
      $("vPw2").focus();
      return;
    }
    var changed = f.vault;
    $("vOk").disabled = true;
    api("PUT", "/api/admin/shares/" + encodeURIComponent(f.id) + "/vault", { password: p1 }).then(function (s) {
      $("vaultDlg").close();
      applied(f, s);
      toast(changed ? "已修改“" + f.name + "”的密码" : "已设为加密片库：" + f.name);
    }).catch(function (err) {
      $("vErr").textContent = err.message;
      $("vOk").disabled = false;
    });
  });

  $("vOffOk").addEventListener("click", function () {
    var f = target;
    $("vOffOk").disabled = true;
    api("DELETE", "/api/admin/shares/" + encodeURIComponent(f.id) + "/vault").then(function (s) {
      $("vaultOffDlg").close();
      applied(f, s);
      toast("已取消加密：" + f.name);
    }).catch(function (err) {
      $("vaultOffDlg").close();
      toast(err.message);
    });
  });

  /* ---------- 放映单中的开关 ---------- */
  function render() {
    var f = current;
    panel.hidden = !f;
    if (!f) { panel.innerHTML = ""; return; }
    panel.innerHTML =
      '<div class="perm"><div><b id="pl-vault">设为加密片库</b>' +
        '<small>手机和电视上平时看不到这个片库。在首页长按电脑名称（电视上长按遥控器的菜单键），输入密码后才会出现。</small></div>' +
        '<button class="sw" role="switch" aria-labelledby="pl-vault" aria-checked="' + !!f.vault + '" data-vault></button></div>' +
      (f.vault
        ? '<div class="vault-on"><p>已加密。<strong>电脑上的文件不会被加密或改动</strong>，在这台电脑上照常能打开。忘记密码不要紧，点“修改密码”直接设个新的。</p>' +
          '<button class="btn ghost" data-vault-change>修改密码</button><button class="btn danger" data-vault-off>取消加密</button></div>'
        : "");
  }
  panel.addEventListener("click", function (e) {
    var f = current;
    if (!f) return;
    if (e.target.closest("[data-vault-change]") || (e.target.closest(".sw[data-vault]") && !f.vault)) openSet(f);
    else if (e.target.closest("[data-vault-off]") || e.target.closest(".sw[data-vault]")) openOff(f);
  });

  document.addEventListener("lfb:sheet", function (e) {
    current = e.detail.share;
    if (current) vaults[current.id] = !!current.vault;
    render();
    markWall();
  });

  /* ---------- 海报标记 ---------- */
  function markWall() {
    document.querySelectorAll("#wall .slot[data-id]").forEach(function (slot) {
      var poster = slot.querySelector(".poster");
      var mark = poster && poster.querySelector(".vault-mark");
      if (!poster) return;
      if (vaults[slot.dataset.id] && !mark) poster.insertAdjacentHTML("afterbegin", MARK);
      else if (!vaults[slot.dataset.id] && mark) mark.remove();
    });
  }
  new MutationObserver(markWall).observe($("wall"), { childList: true });

  api("GET", "/api/admin/shares").then(function (list) {
    list.forEach(function (s) { vaults[s.id] = !!s.vault; });
    markWall();
  }).catch(function () {});
})();
