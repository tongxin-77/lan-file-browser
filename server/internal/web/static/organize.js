// 自动整理（06 文档第 10 节）：只管理 index.html 中 #organizePanel 挂载点（默认 hidden）。
// app.js 每次重绘放映单后派发 document 事件 "lfb:sheet"，event.detail.share 为当前片库（GET /api/admin/shares 的条目）或 null。
// 管理接口修改类请求须带 X-LFB-Admin: 1 与 Content-Type: application/json（CSRF，见 internal/api/middleware.go）。
// 挂载点位于 #sheet 内，app.js 在 #sheet 上监听 click（只认 .sw[data-perm]、[data-who]、#removeBtn）与
// change（只认 data-phone）：本文件的元素不要使用这些属性/ID。
(function () {
  "use strict";
  // TODO: 自动整理设置与状态（/api/admin/shares/{id}/organize…、/api/admin/organize/…）
})();
