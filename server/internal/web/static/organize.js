// 自动整理（06 文档第 10 节）：只管理 index.html 中 #organizePanel 挂载点（默认 hidden）。
// app.js 每次重绘放映单后派发 document 事件 "lfb:sheet"，event.detail.share 为当前片库（GET /api/admin/shares 的条目）或 null。
// 管理接口修改类请求须带 X-LFB-Admin: 1 与 Content-Type: application/json（CSRF，见 internal/api/middleware.go）。
// 挂载点位于 #sheet 内，app.js 在 #sheet 上监听 click（只认 .sw[data-perm]、[data-who]、#removeBtn）与
// change（只认 data-phone）：本文件的元素不要使用这些属性/ID（这里统一用 data-org-*）。
(function () {
  "use strict";

  var panel = document.getElementById("organizePanel");
  var share = null;   // 当前片库
  var data = null;    // GET /organize 的响应
  var dry = null;     // 试运行结果
  var records = null; // 失败与最近记录
  var picking = null; // 文件夹选择器 {path, parent, items}
  var pollTimer = null;

  var STATE = { idle: "空闲", scanning: "扫描中", copying: "复制中", paused: "已暂停", offline: "片库离线" };
  var REASON = { manual: "手动暂停", lowSpace: "片库所在硬盘空间不足", playback: "有人正在播放同一块硬盘上的视频" };
  var ACTION = { copy: "复制", move: "移动", rename: "移动（同分区直接改名）", duplicate: "重复，跳过", waiting: "下载中，暂不整理", locked: "被占用", done: "已整理过", skip: "不整理" };
  var CAT = { photo: "照片", episode: "剧集", movie: "电影", unsorted: "未分类", kept: "原结构" };

  function esc(s) {
    return String(s == null ? "" : s).replace(/[&<>"']/g, function (c) {
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
      if (r.status === 204) return null;
      return r.json().then(function (j) {
        if (!r.ok) throw new Error((j && j.error && j.error.message) || "出错了（" + r.status + "）");
        return j;
      });
    });
  }
  var toastTimer;
  function toast(msg) {
    var t = document.getElementById("toast");
    t.textContent = msg;
    t.classList.add("show");
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { t.classList.remove("show"); }, 2600);
  }
  function fail(e) { toast(e && e.message ? e.message : "操作失败"); }
  function base() { return "/api/admin/shares/" + encodeURIComponent(share.id) + "/organize"; }
  function gb(bytes) { return (bytes / 1073741824).toFixed(bytes < 10737418240 ? 1 : 0) + " GB"; }
  function mb(bytes) { return (bytes / 1048576).toFixed(1) + " MB"; }
  function pad(n) { return (n < 10 ? "0" : "") + n; }
  function when(iso) {
    if (!iso) return "—";
    var t = new Date(iso);
    if (isNaN(t) || t.getFullYear() < 2000) return "—";
    return (t.getMonth() + 1) + "-" + t.getDate() + " " + pad(t.getHours()) + ":" + pad(t.getMinutes());
  }

  /* ---------- 加载 ---------- */
  function load() {
    var id = share.id;
    return api("GET", base()).then(function (d) {
      if (!share || share.id !== id) return;
      data = d;
      render();
      if (d.organize.enabled) loadRecords();
    }).catch(fail);
  }
  function loadRecords() {
    var id = share.id;
    Promise.all([
      api("GET", base() + "/items?state=failed&limit=50"),
      api("GET", base() + "/items?limit=20")
    ]).then(function (r) {
      if (!share || share.id !== id) return;
      records = { failed: r[0], recent: r[1] };
      var el = panel.querySelector("[data-org-slot=records]");
      if (el) el.innerHTML = recordsHTML();
    }).catch(function () {});
  }
  // 只刷新状态区（不打断正在编辑的参数输入）
  function poll() {
    if (!share || !data || !data.organize.enabled || panel.hidden) return;
    var id = share.id;
    api("GET", base()).then(function (d) {
      if (!share || share.id !== id) return;
      var changed = JSON.stringify(d.status.counts) !== JSON.stringify(data.status.counts);
      if (JSON.stringify(d.status) === JSON.stringify(data.status)) return; // 没变化不重绘，避免点击落空
      data.status = d.status;
      var el = panel.querySelector("[data-org-slot=status]");
      if (el) el.innerHTML = statusHTML();
      if (changed) loadRecords();
    }).catch(function () {});
  }

  /* ---------- 渲染 ---------- */
  function statusHTML() {
    var st = data.status, c = st.counts;
    var text = STATE[st.state] || st.state;
    if (st.state === "paused" && st.pauseReason) text += "：" + REASON[st.pauseReason];
    if (st.state === "copying" && st.current) {
      text += " " + esc(st.current.name) + " " + Math.floor(st.current.bytes * 100 / Math.max(st.current.total, 1)) + "% · " + mb(st.current.speed) + "/s";
    }
    var low = st.pauseReason === "lowSpace"
      ? '<p class="org-warn">片库所在硬盘空间不足（剩余 ' + gb(st.freeBytes) + "，需保留 " + gb(st.minFreeBytes) + "）</p>" : "";
    var paused = data.organize.paused;
    return '<div class="org-state"><b class="org-dot ' + esc(st.state) + '"></b><span>' + text + "</span></div>" +
      (st.error ? '<p class="org-warn">' + esc(st.error) + "</p>" : "") + low +
      '<div class="org-meta">上次扫描 ' + when(st.lastScanAt) + " · 下次扫描 " + when(st.nextScanAt) +
      (st.freeBytes ? " · 片库盘剩余 " + gb(st.freeBytes) : "") + "</div>" +
      '<div class="org-counts">' +
        "<span><b>" + c.done + "</b>已整理</span><span><b>" + c.duplicate + "</b>重复</span><span><b>" + c.queued + "</b>队列中</span>" +
        "<span><b>" + c.watching + "</b>观察中</span><span" + (c.failed ? ' class="bad"' : "") + "><b>" + c.failed + "</b>失败</span>" +
      "</div>" +
      '<div class="org-actions">' +
        '<button class="btn main" data-org-act="scan">立即扫描</button>' +
        '<button class="btn ghost" data-org-act="dry">试运行</button>' +
        '<button class="btn ghost" data-org-act="' + (paused ? "resume" : "pause") + '">' + (paused ? "继续" : "暂停") + "</button>" +
      "</div>";
  }

  function sourceHTML(s, i) {
    var hint = (data.sources || []).filter(function (h) { return h.id === s.id; })[0];
    var move = s.mode === "move";
    return '<li class="org-src">' +
      '<div class="org-src-top"><span class="path" title="' + esc(s.path) + '">' + esc(s.path) + "</span>" +
      '<button class="btn danger org-mini" data-org-del="' + i + '">移除</button></div>' +
      '<div class="org-src-opts">' +
        '<label><input type="radio" name="org-mode-' + i + '" data-org-mode="' + i + '" value="copy"' + (move ? "" : " checked") + "> 复制<small>保留源文件，不影响做种</small></label>" +
        '<label><input type="radio" name="org-mode-' + i + '" data-org-mode="' + i + '" value="move"' + (move ? " checked" : "") + "> 移动<small>复制并校验后处理源文件</small></label>" +
        (move ? '<select data-org-after="' + i + '"><option value="recycle"' + (s.afterMove !== "delete" ? " selected" : "") + '>移入回收站（默认）</option>' +
          '<option value="delete"' + (s.afterMove === "delete" ? " selected" : "") + ">直接删除</option></select>" : "") +
        '<label><input type="checkbox" data-org-keep="' + i + '"' + (s.keepStructure ? " checked" : "") + "> 保持源文件夹结构" +
          "<small>" + (s.keepStructure ? "按原来的子文件夹放进片库，不自动归类" : "按照片、剧集、电影自动归类") + "</small></label>" +
      "</div>" +
      (move && s.afterMove === "delete" ? '<p class="org-warn">删除后无法从回收站恢复</p>' : "") +
      (hint && hint.sameDisk ? '<p class="org-warn">两者在同一块硬盘上，整理后播放仍会与下载争用同一块盘，无法解决卡顿</p>' : "") +
      "</li>";
  }

  function pickerHTML() {
    if (!picking) return '<button class="btn ghost" data-org-act="add">添加源文件夹</button>';
    var items = picking.items.length
      ? picking.items.map(function (it) { return '<li><button type="button" data-org-path="' + esc(it.path) + '">' + esc(it.name) + "</button></li>"; }).join("")
      : '<li class="empty-note">' + (picking.path ? "这里没有子文件夹" : "没有找到磁盘") + "</li>";
    return '<label class="field org-path"><span>源文件夹路径 <em>在下面点选，或直接粘贴</em></span>' +
      '<input class="mono" data-org-input placeholder="D:\\Downloads" autocomplete="off" value="' + esc(picking.path) + '"></label>' +
      '<div class="picker org-picker">' +
      '<div class="picker-bar"><button type="button" class="btn ghost" data-org-act="up"' + (picking.path ? "" : " disabled") + ">上一级</button>" +
      "<span>" + esc(picking.path || "此电脑") + "</span></div><ul>" + items + "</ul></div>" +
      '<div class="org-actions"><button class="btn main" data-org-act="pick">选这个文件夹</button>' +
      '<button class="btn ghost" data-org-act="cancelPick">取消</button></div>';
  }

  function paramsHTML() {
    var o = data.organize, d = data.defaults;
    function field(key, label, unit, def) {
      return '<label class="org-param"><span>' + label + "</span><input type=\"number\" min=\"0\" data-org-param=\"" + key + '" value="' +
        (o[key] == null ? "" : o[key]) + '" placeholder="' + esc(def) + '"><em>' + unit + "</em></label>";
    }
    return '<div class="org-params">' +
      field("interval", "扫描间隔", "分钟", d.interval) +
      field("rateLimit", "复制限速", "MB/s，0 不限", d.rateLimit) +
      field("minFree", "最小剩余空间", "GB", d.minFree == null ? "10% 与 20 取小" : d.minFree) +
      field("maxRetries", "失败重试", "次", d.maxRetries) +
      '<label class="org-check"><input type="checkbox" data-org-param="pauseOnPlay"' + (o.pauseOnPlay === false ? "" : " checked") +
        "> 有人播放同一块硬盘上的视频时暂停复制</label>" +
      '<div class="org-actions"><button class="btn ghost" data-org-act="saveParams">保存参数</button><small>留空 = 使用全局默认</small></div>' +
      "</div>";
  }

  function dryHTML() {
    if (!dry) return "";
    if (!dry.items.length) return '<div class="org-dry"><h5>试运行结果</h5><p class="empty-note">源文件夹里没有要整理的视频或照片。</p></div>';
    return '<div class="org-dry"><h5>试运行结果 <small>共 ' + dry.total + " 个" + (dry.total > dry.items.length ? "，只列前 " + dry.items.length + " 个" : "") +
      "，不会复制任何文件</small></h5><table><thead><tr><th>源文件</th><th>类别</th><th>会放到片库里的</th><th>动作</th></tr></thead><tbody>" +
      dry.items.map(function (it) {
        return "<tr><td>" + esc(it.srcPath) + "</td><td>" + esc(CAT[it.category] || it.category) + '</td><td class="mono">' + esc(it.targetPath) +
          '</td><td class="act-' + esc(it.action) + '">' + esc(ACTION[it.action] || it.action) + (it.note ? "<small>" + esc(it.note) + "</small>" : "") + "</td></tr>";
      }).join("") + "</tbody></table></div>";
  }

  function recordsHTML() {
    if (!records) return "";
    var f = records.failed.items, r = records.recent.items;
    var out = "";
    if (f.length) {
      out += '<div class="org-rec"><h5>失败 <button class="btn ghost org-mini" data-org-act="retryAll">全部重试</button></h5><ul>' +
        f.map(function (it) {
          return "<li><span>" + esc(it.srcPath) + '</span><small class="bad">' + esc(it.error) + " · " + it.attempts + ' 次</small>' +
            '<button class="btn ghost org-mini" data-org-retry="' + it.id + '">重试</button></li>';
        }).join("") + "</ul></div>";
    }
    if (r.length) {
      out += '<div class="org-rec"><h5>最近整理</h5><ul>' + r.map(function (it) {
        return "<li><span>" + esc(it.srcPath) + ' <i>→</i> <b class="mono">' + esc(it.targetPath || "—") + "</b></span><small>" +
          when(it.updatedAt) + " · " + esc({ done: "已整理", duplicate: "重复", queued: "排队", copying: "复制中", failed: "失败" }[it.state] || it.state) +
          "</small>" + '<button class="btn ghost org-mini" data-org-redo="' + it.id + '" title="删除这条记录，下次扫描按新文件处理">重新整理</button></li>';
      }).join("") + "</ul></div>";
    }
    return out;
  }

  function render() {
    var o = data.organize;
    var head = '<div class="org-head"><div><h4 id="orgTitle">自动整理</h4><small>把下载文件夹里下载完成的视频和照片，自动归类到这个片库。' +
      "片库最好放在另一块硬盘上，播放时就不会和下载抢硬盘。</small></div>" +
      '<button class="sw" role="switch" aria-labelledby="orgTitle" aria-checked="' + !!o.enabled + '" data-org-act="toggle"></button></div>';
    if (!o.enabled) {
      panel.innerHTML = head;
      return;
    }
    panel.innerHTML = head +
      '<div class="org-body">' +
        '<div class="org-col"><h5>状态</h5><div data-org-slot="status">' + statusHTML() + "</div>" +
          "<h5>参数</h5>" + paramsHTML() + "</div>" +
        '<div class="org-col"><h5>源文件夹 <small>下载完成后从这里整理过来</small></h5>' +
          (o.sources.length ? '<ul class="org-srcs">' + o.sources.map(sourceHTML).join("") + "</ul>" : '<p class="empty-note">还没有源文件夹。</p>') +
          pickerHTML() + "</div>" +
      "</div>" + dryHTML() + '<div data-org-slot="records">' + recordsHTML() + "</div>";
  }

  /* ---------- 保存 ---------- */
  function save(change, okMsg) {
    var next = JSON.parse(JSON.stringify(data.organize));
    change(next);
    return api("PUT", base(), next).then(function (d) {
      data = d;
      render();
      if (okMsg) toast(okMsg);
      if (d.organize.enabled) loadRecords();
    }).catch(function (e) { render(); fail(e); });
  }

  function browse(path) {
    api("GET", "/api/admin/browse?path=" + encodeURIComponent(path)).then(function (r) {
      picking = r;
      render();
    }).catch(fail);
  }

  /* ---------- 事件 ---------- */
  panel.addEventListener("click", function (e) {
    if (!share || !data) return;
    var t = e.target.closest("[data-org-act],[data-org-del],[data-org-path],[data-org-retry],[data-org-redo]");
    if (!t) return;
    var act = t.dataset.orgAct;
    if (t.dataset.orgPath) { browse(t.dataset.orgPath); return; }
    if (t.dataset.orgDel) {
      var i = +t.dataset.orgDel;
      save(function (o) { o.sources.splice(i, 1); }, "已移除源文件夹");
      return;
    }
    if (t.dataset.orgRetry || act === "retryAll") {
      var ids = t.dataset.orgRetry ? [+t.dataset.orgRetry] : records.failed.items.map(function (it) { return it.id; });
      api("POST", base() + "/items/retry", { ids: ids }).then(function (r) { toast("已重新排队 " + r.retried + " 个文件"); load(); }).catch(fail);
      return;
    }
    if (t.dataset.orgRedo) {
      api("DELETE", base() + "/items/" + t.dataset.orgRedo).then(function () { toast("下次扫描会重新整理这个文件"); loadRecords(); }).catch(fail);
      return;
    }
    switch (act) {
      case "toggle":
        save(function (o) { o.enabled = !o.enabled; }, data.organize.enabled ? "已关闭自动整理" : "已开启自动整理");
        break;
      case "add": browse(""); break;
      case "up": browse(picking.parent); break;
      case "cancelPick": picking = null; render(); break;
      case "pick":
        var p = panel.querySelector("[data-org-input]").value.trim().replace(/^"|"$/g, "");
        if (!/^[A-Za-z]:\\/.test(p) && !/^\\\\/.test(p)) { toast("请选一个文件夹，或填写完整路径，例如 D:\\Downloads"); break; }
        picking = null;
        save(function (o) { o.sources.push({ id: "", path: p, mode: "copy", keepStructure: true }); }, "已添加源文件夹");
        break;
      case "scan":
        api("POST", base() + "/scan").then(function () { toast("已开始扫描（新文件要两次扫描都没变化才会整理）"); setTimeout(poll, 800); }).catch(fail);
        break;
      case "dry":
        t.disabled = true;
        api("POST", base() + "/dry-run").then(function (r) { dry = r; render(); }).catch(fail).then(function () { t.disabled = false; });
        break;
      case "pause":
      case "resume":
        api("POST", base() + "/" + act).then(function (d) { data = d; render(); toast(act === "pause" ? "已暂停整理" : "已继续整理"); }).catch(fail);
        break;
      case "saveParams":
        save(function (o) {
          panel.querySelectorAll("[data-org-param]").forEach(function (inp) {
            var k = inp.dataset.orgParam;
            if (k === "pauseOnPlay") o.pauseOnPlay = inp.checked;
            else o[k] = inp.value === "" ? null : Math.max(0, parseInt(inp.value, 10) || 0);
          });
        }, "参数已保存");
        break;
    }
  });
  panel.addEventListener("change", function (e) {
    var t = e.target;
    if (t.dataset.orgMode) {
      var i = +t.dataset.orgMode, mode = t.value;
      save(function (o) {
        o.sources[i].mode = mode;
        o.sources[i].afterMove = mode === "move" ? (o.sources[i].afterMove || "recycle") : "";
      });
    } else if (t.dataset.orgKeep) {
      var k = +t.dataset.orgKeep, keep = t.checked;
      save(function (o) { o.sources[k].keepStructure = keep; }, keep ? "将保持源文件夹结构" : "将按类型自动归类");
    } else if (t.dataset.orgAfter) {
      var j = +t.dataset.orgAfter, v = t.value;
      save(function (o) { o.sources[j].afterMove = v; }, v === "delete" ? "源文件整理后将直接删除" : "源文件整理后移入回收站");
    }
  });

  document.addEventListener("lfb:sheet", function (e) {
    var f = e.detail && e.detail.share;
    if (!f) {
      share = null;
      panel.hidden = true;
      panel.innerHTML = "";
      return;
    }
    panel.hidden = false;
    if (share && share.id === f.id && data) { share = f; return; } // 同一片库重绘：挂载点被移回原位，内容保留
    share = f;
    data = dry = records = picking = null;
    panel.innerHTML = '<p class="empty-note">正在读取自动整理设置…</p>';
    load();
  });

  clearInterval(pollTimer);
  pollTimer = setInterval(poll, 3000);
})();
