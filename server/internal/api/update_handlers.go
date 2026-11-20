package api

import (
	"context"
	"net/http"
	"os"
	"slices"
	"time"

	"lfb/server/internal/update"
)

// MinAppVersion 为本服务端要求的最低 App 版本（02 文档 5.3）：接口发生 App 旧版无法兼容的变更时提升。
const MinAppVersion = "0.0.0"

// GET /api/admin/update —— 仅本机，管理页横幅
func (s *Server) handleAdminUpdate(w http.ResponseWriter, r *http.Request) {
	if s.Updates == nil {
		notImplemented(w, r)
		return
	}
	st := s.Updates.Status()
	s.Updates.MaybeRefresh()
	writeJSON(w, st)
}

// POST /api/admin/update/check —— 仅本机 + CSRF，立即检查；APK 在后台缓存
func (s *Server) handleAdminUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if s.Updates == nil {
		notImplemented(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if s.Updates.Check(ctx) == nil {
		s.Updates.CacheAPKsAsync()
	}
	writeJSON(w, s.Updates.Status())
}

// GET /api/app/latest —— 需设备鉴权；apks 只列已缓存（已校验）的 ABI，地址指向本服务
func (s *Server) handleAppLatest(w http.ResponseWriter, r *http.Request) {
	if s.Updates == nil {
		writeError(w, CodeNotFound, "暂无版本信息")
		return
	}
	rel, cached := s.Updates.App()
	s.Updates.MaybeRefresh()
	if rel == nil {
		writeError(w, CodeNotFound, "暂无版本信息")
		return
	}
	apks, sums := map[string]string{}, map[string]string{}
	for abi := range cached {
		apks[abi] = "http://" + r.Host + "/api/app/apk/" + abi
		sums[abi] = rel.SHA256["lfb-app-"+abi+".apk"]
	}
	writeJSON(w, map[string]any{
		"version": rel.Version, "minAppVersion": MinAppVersion, "notes": rel.Notes, "apks": apks, "sha256": sums,
	})
}

// GET /api/app/apk/{abi} —— 需设备鉴权（含 ?t=），从本地缓存提供 APK，支持 Range；未缓存 404
func (s *Server) handleAppAPK(w http.ResponseWriter, r *http.Request) {
	abi := r.PathValue("abi")
	if s.Updates == nil || !slices.Contains(update.ABIs, abi) {
		writeError(w, CodeNotFound, "安装包不存在")
		return
	}
	p, ver, ok := s.Updates.APK(abi)
	if !ok {
		writeError(w, CodeNotFound, "安装包尚未缓存")
		return
	}
	f, err := os.Open(p) // 缓存目录内由服务端生成的文件，不属于共享访问
	if err != nil {
		writeError(w, CodeNotFound, "安装包尚未缓存")
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		writeError(w, CodeInternal, "服务端错误")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", `attachment; filename="lfb-app-`+abi+`-`+ver+`.apk"`)
	http.ServeContent(w, r, "", fi.ModTime(), f)
}
