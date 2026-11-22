(function () {
  "use strict";

  var shares = [];   // GET /api/admin/shares
  var phones = [];   // GET /api/devices
  var status = null; // GET /api/admin/status
  var pairing = null;
  var selected = null;
  var pendingSome = {}; // 选了“指定手机”但还没勾任何手机的片库（尚未保存）
  var panels = { vault: document.getElementById("vaultPanel"), organize: document.getElementById("organizePanel") };

  var PERMS = [
    { key: "play", title: "手机能打开看、能播放", note: "这是共享的前提，一直开着。", locked: true },
    { key: "download", title: "手机能下载回去看", note: "存到手机里，出门没网也能看。" },
    { key: "upload", title: "手机能往这里放东西", note: "比如把手机拍的照片传回这台电脑。" },
    { key: "delete", title: "允许手机删除（移入回收站）", note: "手机上删掉的文件会进这台电脑的回收站，可以从回收站找回；所在硬盘没有回收站时不会删除。", risky: true },
    { key: "hidden", title: "显示藏起来的文件", note: "电脑上设为“隐藏”的文件也会出现在手机上。" }
  ];
  var COVERS = ["movie", "anime", "photo", "work", "new"];
  var NEW_COLORS = ["#6B3A5A", "#2F5E63", "#7A5A2B", "#3E4C7A", "#5A2E2E"];

  var $ = function (id) { return document.getElementById(id); };
  function esc(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }

  /* ---------- 接口 ---------- */
  // 修改类请求带 X-LFB-Admin 与 JSON Content-Type（服务端 CSRF 校验要求）。
  function api(method, url, body) {
    var opt = { method: method, headers: {}, cache: "no-store" };
    if (method !== "GET") {
      opt.headers["X-LFB-Admin"] = "1";
      opt.headers["Content-Type"] = "application/json";
      opt.body = JSON.stringify(body || {});
    }
    return fetch(url, opt).then(function (r) {
      if (r.status === 204) return null;
      return r.json().then(function (j) {
        if (!r.ok) throw new Error((j && j.error && j.error.message) || "出错了（" + r.status + "）");
        return j;
      });
    });
  }
  function fail(e) { toast(e && e.message ? e.message : "操作失败"); }

  /* ---------- 工具 ---------- */
  function hash(s) {
    var h = 0;
    for (var i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0;
    return h;
  }
  function find(id) {
    for (var i = 0; i < shares.length; i++) if (shares[i].id === id) return shares[i];
    return null;
  }
  function phoneName(id) {
    for (var i = 0; i < phones.length; i++) if (phones[i].deviceId === id) return phones[i].deviceName || "未命名手机";
    return "";
  }
  function knownDevices(f) { return f.devices.filter(function (id) { return phoneName(id) !== ""; }); }
  function who(f) { return f.devices.length || pendingSome[f.id] ? "some" : "all"; }
  function seeLine(f) {
    if (who(f) === "all") return "全部手机可见";
    var names = knownDevices(f).map(phoneName);
    return names.length ? "仅" + names.join("、") + "可见" : "暂时没有手机能看";
  }
  function drive(path) { var m = /^([A-Za-z]):/.exec(path); return m ? m[1].toUpperCase() : ""; }
  function baseName(path) {
    var parts = path.replace(/[\\/]+$/, "").split(/[\\/]/);
    return parts[parts.length - 1] || path;
  }
  function permLine(f) {
    var p = f.perms, on = [];
    if (p.download) on.push("可下载");
    if (p.upload) on.push("可上传");
    if (p.delete) on.push("可删除");
    return on.length ? on.join(" · ") : "只能看";
  }
  function pad(n) { return (n < 10 ? "0" : "") + n; }
  function seenText(iso) {
    var t = new Date(iso);
    if (!iso || isNaN(t) || t.getFullYear() < 2000) return { text: "还没来过", recent: false };
    var diff = (Date.now() - t) / 1000, now = new Date();
    var hm = pad(t.getHours()) + ":" + pad(t.getMinutes());
    var day0 = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
    var text;
    if (diff < 60) text = "刚刚";
    else if (diff < 3600) text = Math.floor(diff / 60) + " 分钟前";
    else if (t >= day0) text = "今天 " + hm;
    else if (t >= day0 - 86400000) text = "昨天 " + hm;
    else text = Math.max(2, Math.floor((day0 - t) / 86400000) + 1) + " 天前";
    return { text: text, recent: diff < 600 };
  }

  var toastTimer;
  function toast(msg) {
    var t = $("toast");
    t.textContent = msg;
    t.classList.add("show");
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { t.classList.remove("show"); }, 2200);
  }

  /* ---------- 服务状态 ---------- */
  function renderStatus() {
    var s = status;
    $("pcName").textContent = s.name;
    $("ver").textContent = s.version;
    $("addr").innerHTML = s.ips.length
      ? s.ips.map(function (ip) { return esc(ip + ":" + s.port); }).join("<br>")
      : "未连接网络";
  }
  function setLamp(ok) {
    $("lamp").classList.toggle("off", !ok);
    $("lampText").textContent = ok ? "放映机运行中" : "放映机没有响应";
  }

  /* ---------- 海报封面 ---------- */
  // fit 按名称长度缩小海报大字，避免长名字折行或溢出（span 为可用长度，单位 cqw）。
  function fit(f, max, span) {
    var len = Array.from(f.name).length;
    return len * max > span ? ' style="font-size:' + (span / len).toFixed(1) + 'cqw"' : "";
  }
  // 封面构图按位置轮换（相邻海报不重复）；断开的片库用“灯灭了”。
  function cover(f, i) {
    var n = esc(f.name), d = drive(f.path);
    if (f.status !== "ok") {
      return '<div class="poster c-off"><div class="t">' + n + '</div><div class="warn">找不到这个文件夹<br>硬盘可能没接上</div></div>';
    }
    switch (COVERS[i % COVERS.length]) {
      case "movie":
        return '<div class="poster c-movie"><div class="t"' + fit(f, 36, 90) + '>' + n + '</div><div class="n">' + (d ? d + " 盘" : "") + '<small>' + esc(seeLine(f)) + '</small></div></div>';
      case "anime":
        return '<div class="poster c-anime"><div class="t"' + fit(f, 31, 85) + '>' + n + '</div><div class="n">' + esc(seeLine(f)) + '</div></div>';
      case "photo":
        return '<div class="poster c-photo"><div class="f f1"></div><div class="f f2"></div><div class="f f3"></div><div class="t"' + fit(f, 29, 85) + '>' + n + '</div><div class="n">' + (d ? d + ":" : "") + '</div></div>';
      case "work":
        var t = f.name.length > 2 ? esc(f.name.slice(0, 2)) + "<br>" + esc(f.name.slice(2)) : n;
        return '<div class="poster c-work"><div class="t">' + t + '</div><div class="lock">' + esc(seeLine(f)) + '</div></div>';
      default:
        return '<div class="poster c-new" style="background:' + NEW_COLORS[hash(f.id) % NEW_COLORS.length] + '"><div class="n">片库</div><div class="t">' + esc(f.name.charAt(0)) + '</div></div>';
    }
  }

  function renderWall() {
    var html = shares.map(function (f, i) {
      var off = f.status !== "ok";
      return '<button class="slot' + (off ? " off" : "") + '" data-id="' + esc(f.id) + '" aria-pressed="' + (f.id === selected) + '">' +
        cover(f, i) + '<div class="cap"><b>' + esc(f.name) + '</b><small>' + (off ? "路径不存在" : esc(permLine(f))) + '</small></div></button>';
    }).join("");
    html += '<button class="slot add" id="addSlot"><div class="poster empty"><div class="plus">+</div><span>添加片库</span></div>' +
      '<div class="cap"><b>&nbsp;</b><small>&nbsp;</small></div></button>';
    $("wall").innerHTML = html;
    $("libCount").textContent = shares.length + " 个";
  }

  /* ---------- 放映单 ---------- */
  // 重绘放映单后把 vault.js / organize.js 的挂载点（index.html）放回原位，并通知它们当前片库。
  function mountPanels(f) {
    var col = $("sheet").querySelector(".sheet-body > div");
    (col || $("sheet")).appendChild(panels.vault);
    $("sheet").appendChild(panels.organize);
    document.dispatchEvent(new CustomEvent("lfb:sheet", { detail: { share: f || null } }));
  }

  function renderSheet() {
    var f = find(selected);
    var el = $("sheet");
    if (!f) {
      el.innerHTML = '<p class="empty-note">还没有片库。点上面的“添加片库”，选一个电脑上的文件夹开始。</p>';
      mountPanels(null);
      return;
    }
    var off = f.status !== "ok", w = who(f);
    var seeCount = w === "all" ? "全部" : knownDevices(f).length + " 台";
    var facts = off
      ? '<div><small>状态</small><b class="bad">路径不存在，硬盘未连接</b></div>'
      : '<div><small>能看到的手机</small><b>' + seeCount + '</b></div><div><small>状态</small><b class="ok">正常</b></div>';

    var perms = PERMS.map(function (p) {
      var on = p.locked ? true : !!f.perms[p.key];
      return '<div class="perm' + (p.risky ? " risky" : "") + '"><div><b id="pl-' + p.key + '">' + p.title + '</b><small>' + p.note + '</small></div>' +
        '<button class="sw" role="switch" aria-labelledby="pl-' + p.key + '" aria-checked="' + on + '" data-perm="' + p.key + '"' + (p.locked ? " disabled" : "") + '></button></div>';
    }).join("");

    var pick = phones.map(function (ph) {
      return '<label><input type="checkbox" data-phone="' + esc(ph.deviceId) + '"' + (f.devices.indexOf(ph.deviceId) > -1 ? " checked" : "") + '>' +
        esc(ph.deviceName || "未命名手机") + '</label>';
    }).join("");
    if (!phones.length) pick = '<p class="empty-note">还没有配对的手机。</p>';

    var d = drive(f.path);
    el.innerHTML =
      '<div class="sheet-head"><div><h3>' + esc(f.name) + '</h3><div class="path">' + esc(f.path) + '</div></div><div class="facts">' + facts + '</div></div>' +
      (off ? '<p class="off-note">' + (d ? "把 " + d + " 盘的硬盘接回电脑" : "等这个文件夹重新出现") + '，这部片库会自动恢复，设置都还在。</p>' : '') +
      '<div class="sheet-body">' +
        '<div><h4>手机能做什么</h4>' + perms + '</div>' +
        '<div><h4>哪些手机能看到</h4>' +
          '<div class="who" role="group" aria-label="可见范围"><button data-who="all" aria-pressed="' + (w === "all") + '">全部手机</button><button data-who="some" aria-pressed="' + (w === "some") + '">指定手机</button></div>' +
          '<div class="phone-pick' + (w === "all" ? " locked" : "") + '">' + pick + '</div>' +
          '<div class="remove"><p>移除后手机上就看不到“' + esc(f.name) + '”了。电脑里 ' + esc(f.path) + ' 的文件不会被删除。</p>' +
          '<button class="btn danger" id="removeBtn">移除这个片库</button></div>' +
        '</div>' +
      '</div>';
    mountPanels(f);
  }

  function renderSeats() {
    $("phoneCount").textContent = phones.length + " 台";
    if (!phones.length) {
      $("seats").innerHTML = '<li class="empty-note">还没有手机入座。用上面的二维码或配对码让手机入场。</li>';
      return;
    }
    $("seats").innerHTML = phones.map(function (p) {
      var s = seenText(p.lastSeen);
      return '<li class="seat' + (s.recent ? " recent" : "") + '"><span class="chair" aria-hidden="true"></span>' +
        '<div><b>' + esc(p.deviceName || "未命名手机") + '</b><small>最后来看：' + esc(s.text) + '</small></div>' +
        '<button class="btn danger" data-revoke="' + esc(p.deviceId) + '">取消配对</button></li>';
    }).join("");
  }

  function renderAll() { renderWall(); renderSheet(); renderSeats(); }

  /* ---------- 保存片库 ---------- */
  // PUT 整体替换 name/path/perms/devices；失败时恢复原值。
  function saveShare(f, change, okMsg) {
    var before = JSON.stringify(f);
    change(f);
    renderWall(); renderSheet();
    return api("PUT", "/api/admin/shares/" + encodeURIComponent(f.id),
      { name: f.name, path: f.path, perms: f.perms, devices: f.devices })
      .then(function (s) {
        var i = shares.indexOf(f);
        if (i > -1) shares[i] = s;
        renderWall(); renderSheet();
        if (okMsg) toast(okMsg);
      })
      .catch(function (e) {
        var old = JSON.parse(before);
        Object.keys(old).forEach(function (k) { f[k] = old[k]; });
        renderWall(); renderSheet();
        fail(e);
      });
  }

  /* ---------- 事件 ---------- */
  $("wall").addEventListener("click", function (e) {
    var slot = e.target.closest(".slot");
    if (!slot) return;
    if (slot.id === "addSlot") { openAdd(); return; }
    selected = slot.dataset.id;
    renderWall(); renderSheet();
  });

  $("sheet").addEventListener("click", function (e) {
    var f = find(selected);
    if (!f) return;
    var sw = e.target.closest(".sw[data-perm]"); // 只管权限开关；挂载点内（vault.js 等）的开关由各自脚本处理
    if (sw && !sw.disabled) {
      var k = sw.dataset.perm;
      var title = PERMS.filter(function (p) { return p.key === k; })[0].title;
      var on = !f.perms[k];
      saveShare(f, function (x) { x.perms[k] = on; }, (on ? "已开启：" : "已关闭：") + title);
      return;
    }
    var w = e.target.closest("[data-who]");
    if (w) {
      if (w.dataset.who === who(f)) return;
      if (w.dataset.who === "all") {
        delete pendingSome[f.id];
        saveShare(f, function (x) { x.devices = []; }, "“" + f.name + "”全部手机都能看到");
      } else {
        pendingSome[f.id] = true;
        renderWall(); renderSheet();
        toast(phones.length ? "勾选能看到“" + f.name + "”的手机" : "还没有配对的手机");
      }
      return;
    }
    if (e.target.id === "removeBtn") {
      confirmBox("移除“" + f.name + "”？",
        "手机上将看不到这个片库。<strong>电脑里 " + esc(f.path) + " 的文件原样保留，不会被删除。</strong>",
        "移除片库", function () {
          api("DELETE", "/api/admin/shares/" + encodeURIComponent(f.id)).then(function () {
            var i = shares.indexOf(f);
            shares.splice(i, 1);
            delete pendingSome[f.id];
            selected = shares.length ? shares[Math.max(0, i - 1)].id : null;
            renderAll();
            toast("已移除片库：" + f.name);
          }).catch(fail);
        });
    }
  });

  $("sheet").addEventListener("change", function (e) {
    var f = find(selected);
    var id = e.target.dataset.phone;
    if (!f || !id) return;
    var next = knownDevices(f).filter(function (x) { return x !== id; });
    if (e.target.checked) next.push(id);
    if (!next.length) { // 空列表表示“全部手机”，不能用来表示“没有手机”
      e.target.checked = true;
      toast("至少留一台手机；想让全部手机都看到，请选“全部手机”");
      return;
    }
    delete pendingSome[f.id];
    saveShare(f, function (x) { x.devices = next; },
      (e.target.checked ? "现在能看：" : "现在看不到：") + phoneName(id));
  });

  $("seats").addEventListener("click", function (e) {
    var b = e.target.closest("[data-revoke]");
    if (!b) return;
    var id = b.dataset.revoke, name = phoneName(id);
    confirmBox("取消“" + name + "”的配对？",
      "这台手机将立刻无法再浏览和播放，想再看需要重新扫码配对。<strong>电脑上的文件不受影响。</strong>",
      "取消配对", function () {
        api("DELETE", "/api/devices/" + encodeURIComponent(id)).then(function () {
          phones = phones.filter(function (p) { return p.deviceId !== id; });
          renderAll();
          toast("已取消配对：" + name);
        }).catch(fail);
      });
  });

  /* ---------- 弹窗 ---------- */
  document.querySelectorAll("[data-close]").forEach(function (b) {
    b.addEventListener("click", function () { b.closest("dialog").close(); });
  });

  var cfAction = null;
  function confirmBox(title, html, okText, fn) {
    $("cfTitle").textContent = title;
    $("cfText").innerHTML = html;
    $("cfOk").textContent = okText;
    cfAction = fn;
    $("confirmDlg").showModal();
  }
  $("cfOk").addEventListener("click", function () {
    $("confirmDlg").close();
    if (cfAction) cfAction();
  });

  /* ---------- 添加片库 + 文件夹选择器 ---------- */
  var pick = { path: "", parent: "" };
  function browse(path) {
    api("GET", "/api/admin/browse?path=" + encodeURIComponent(path)).then(function (r) {
      pick = r;
      $("pickWhere").textContent = r.path || "此电脑";
      $("pickUp").disabled = !r.path;
      $("pickList").innerHTML = r.items.length
        ? r.items.map(function (it) {
            return '<li><button type="button" data-path="' + esc(it.path) + '" title="' + esc(it.path) + '">' + esc(it.name) + '</button></li>';
          }).join("")
        : '<li class="empty-note">' + (r.path ? "这里没有子文件夹" : "没有找到磁盘") + '</li>';
      if (r.path) {
        $("addPath").value = r.path;
        $("addName").placeholder = baseName(r.path);
      }
    }).catch(fail);
  }
  $("pickList").addEventListener("click", function (e) {
    var b = e.target.closest("[data-path]");
    if (b) browse(b.dataset.path);
  });
  $("pickUp").addEventListener("click", function () { browse(pick.parent); });
  $("addPath").addEventListener("input", function () {
    var p = $("addPath").value.trim();
    $("addName").placeholder = p ? baseName(p) : "例如：纪录片";
  });

  function openAdd() {
    $("addPath").value = ""; $("addName").value = ""; $("addErr").textContent = "";
    $("addName").placeholder = "例如：纪录片";
    browse("");
    $("addDlg").showModal();
    $("addPath").focus();
  }
  $("addForm").addEventListener("submit", function (e) {
    e.preventDefault();
    var path = $("addPath").value.trim().replace(/^"|"$/g, "");
    if (!/^[A-Za-z]:\\/.test(path) && !/^\\\\/.test(path)) {
      $("addErr").textContent = "请在上面选一个文件夹，或填写完整路径，例如 D:\\Movies";
      $("addPath").focus();
      return;
    }
    var name = $("addName").value.trim() || baseName(path);
    $("addOk").disabled = true;
    api("POST", "/api/admin/shares", {
      name: name, path: path, devices: [],
      perms: { download: false, upload: false, delete: false, hidden: false }
    }).then(function (s) {
      shares.push(s);
      selected = s.id;
      $("addDlg").close();
      renderAll();
      toast("已添加片库：" + s.name);
    }).catch(function (err) {
      $("addErr").textContent = err.message;
    }).then(function () { $("addOk").disabled = false; });
  });

  /* ---------- 配对：二维码 + PIN 倒计时 ---------- */
  function renderPairing() {
    $("qr").innerHTML = pairing.qrSvg; // 服务端生成的 SVG
    $("pin").textContent = pairing.pin.slice(0, 3) + " " + pairing.pin.slice(3);
    paintTimer();
  }
  function loadPairing() {
    return api("GET", "/api/admin/pairing").then(function (p) {
      var changed = !pairing || p.pin !== pairing.pin || p.code !== pairing.code;
      pairing = p;
      if (changed) renderPairing();
    });
  }
  var expiring = false;
  function paintTimer() {
    if (!pairing) return;
    var t = $("timer");
    var left = Math.max(0, Math.round((new Date(pairing.expiresAt) - Date.now()) / 1000));
    if (left === 0) {
      t.textContent = "正在换新的配对码…";
      if (!expiring) { // 过期后服务端会自动换新
        expiring = true;
        loadPairing().catch(function () {}).then(function () { expiring = false; });
      }
      return;
    }
    t.textContent = Math.floor(left / 60) + ":" + pad(left % 60) + " 后失效";
    t.classList.toggle("low", left < 60);
  }
  $("refreshPin").addEventListener("click", function () {
    var b = this;
    b.disabled = true;
    api("POST", "/api/admin/pairing/refresh").then(function (p) {
      pairing = p;
      renderPairing();
      toast("已换一组配对码，5 分钟内有效");
    }).catch(fail).then(function () { b.disabled = false; });
  });

  /* ---------- 加载与轮询 ---------- */
  function loadPhones() {
    return api("GET", "/api/devices").then(function (list) {
      list.sort(function (a, b) { return new Date(b.lastSeen) - new Date(a.lastSeen); });
      var changed = JSON.stringify(list) !== JSON.stringify(phones);
      phones = list;
      return changed;
    });
  }

  Promise.all([
    api("GET", "/api/admin/status"),
    api("GET", "/api/admin/shares"),
    loadPhones(),
    loadPairing()
  ]).then(function (r) {
    status = r[0];
    shares = r[1];
    selected = shares.length ? shares[0].id : null;
    renderStatus();
    renderAll();
  }).catch(function (e) {
    setLamp(false);
    fail(e);
  });

  setInterval(paintTimer, 1000);
  // 每 5 秒刷新手机列表（新配对的手机）与配对码（一次性码被使用后会换新）
  setInterval(function () {
    Promise.all([loadPhones(), loadPairing()]).then(function (r) {
      setLamp(true);
      if (r[0]) { renderSeats(); renderWall(); if (!document.querySelector("#sheet :focus")) renderSheet(); }
    }).catch(function () { setLamp(false); });
  }, 5000);
})();
