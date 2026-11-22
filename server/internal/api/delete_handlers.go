package api

import (
	"encoding/json"
	"errors"
	"log"
	"mime"
	"net/http"
	"path/filepath"

	"lfb/server/internal/config"
	"lfb/server/internal/fsx"
	"lfb/server/internal/organize"
)

const maxDeletePaths = 1000

// deleteFailure 为 /api/delete 中单个路径的失败原因（03 文档 4.10）。
type deleteFailure struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// POST /api/delete {share, paths} —— 移入 Windows 回收站（03 文档 4.10），需 modify（配置 perms.delete）权限。
// 请求级错误（参数、共享不可见/离线、无权限）用统一错误格式；逐路径处理，全部成功 204，
// 否则 200 {"failed":[{path, code, message}]}（成功的照常删除）。
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		writeError(w, CodeBadPath, "参数非法")
		return
	}
	var in struct {
		Share string   `json:"share"`
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil ||
		len(in.Paths) == 0 || len(in.Paths) > maxDeletePaths {
		writeError(w, CodeBadPath, "参数非法")
		return
	}
	var sh *config.Share
	for _, x := range s.allShares() {
		if x.ID == in.Share && s.accessible(r, &x) {
			sh = &x
			break
		}
	}
	if sh == nil {
		writeError(w, CodeNotFound, "共享不存在")
		return
	}
	if shareStatus(sh.Path) != "ok" {
		writeError(w, CodeShareOffline, "共享文件夹不可用（硬盘未连接？）")
		return
	}
	if !sh.Perms.Delete {
		writeError(w, CodeForbidden, "该共享不允许删除")
		return
	}
	failed := []deleteFailure{}
	for _, p := range in.Paths {
		if code, msg := s.deleteOne(r, sh, p); code != "" {
			failed = append(failed, deleteFailure{p, code, msg})
		}
	}
	if len(failed) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, map[string]any{"failed": failed})
}

// deleteOne 把共享内 rel 移入回收站，失败时返回错误码与提示（不含真实路径）。
// 路径经 fsx 三层校验与隐藏项规则（openInShare）；校验通过后关闭句柄，再把绝对路径交给 Shell。
func (s *Server) deleteOne(r *http.Request, sh *config.Share, rel string) (code, msg string) {
	if rel == "" {
		return CodeBadPath, "不能删除共享文件夹本身"
	}
	f, _, err := openInShare(sh, rel)
	if err != nil {
		switch {
		case errors.Is(err, fsx.ErrBadPath):
			return CodeBadPath, "路径非法"
		case errors.Is(err, fsx.ErrShareOffline):
			return CodeShareOffline, "共享文件夹不可用（硬盘未连接？）"
		}
		return CodeNotFound, "文件不存在"
	}
	f.Close()
	if s.Activity != nil && s.Activity.Playing(filepath.Join(sh.Path, rel)) {
		return CodeInUse, "正在播放，不能删除"
	}
	realRoot, err := filepath.EvalSymlinks(sh.Path)
	if err != nil {
		return CodeShareOffline, "共享文件夹不可用（硬盘未连接？）"
	}
	rec := s.Recycler
	if rec == nil {
		rec = organize.ShellRecycler{}
	}
	err = rec.Recycle(filepath.Join(realRoot, filepath.FromSlash(rel)))
	// 加密片库不记录文件名（04 文档第 7 节 #10）
	name, detail := rel, err
	if isVault(sh) {
		name, detail = "（加密片库）", nil
	}
	dev := DeviceID(r.Context())
	switch {
	case err == nil:
		log.Printf("设备 %s 删除（移入回收站）：共享 %s %s", dev, sh.ID, name)
		return "", ""
	case errors.Is(err, organize.ErrInUse):
		return CodeInUse, "文件正被其他程序使用，不能删除"
	case errors.Is(err, organize.ErrNoRecycleBin):
		return CodeNoRecycleBin, "所在硬盘没有回收站（或超过回收站容量），未删除"
	}
	log.Printf("设备 %s 删除失败：共享 %s %s %v", dev, sh.ID, name, detail)
	return CodeInternal, "删除失败"
}
