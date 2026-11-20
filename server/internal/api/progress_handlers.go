package api

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"time"

	"lfb/server/internal/activity"
	"lfb/server/internal/auth"
	"lfb/server/internal/config"
	"lfb/server/internal/media"
	"lfb/server/internal/progress"
)

// 观看进度与继续观看（03 文档 4.8）。存储用 s.Store（data.db，表经 internal/store 迁移创建）；
// 收到进度上报时须调用 s.Activity.Progress(卷) 供阻止睡眠与整理库判断（05 文档 3.4）。

const (
	maxContinue    = 20
	maxDurationSec = 7 * 24 * 3600 // 时长上限（防异常值）
)

// itemProgress 为 /api/list 视频条目的本设备进度（秒）。
type itemProgress struct {
	Position float64 `json:"position"`
	Duration float64 `json:"duration"`
	Watched  bool    `json:"watched"`
}

// POST /api/progress {share, path, position, duration}
func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Share    string   `json:"share"`
		Path     string   `json:"path"`
		Position *float64 `json:"position"`
		Duration *float64 `json:"duration"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil ||
		in.Position == nil || in.Duration == nil {
		writeError(w, CodeBadPath, "参数非法")
		return
	}
	pos, dur := *in.Position, *in.Duration
	if math.IsNaN(pos) || math.IsNaN(dur) || pos < 0 || dur <= 0 || dur > maxDurationSec || math.IsInf(pos, 0) {
		writeError(w, CodeBadPath, "参数非法")
		return
	}
	pos = min(pos, dur) // 播放器上报的位置可能略超时长

	dev := DeviceID(r.Context())
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
	f, fi, err := openInShare(sh, in.Path)
	if err != nil {
		writeFSError(w, err)
		return
	}
	f.Close()
	if fi.IsDir() || media.Kind(in.Path) != media.KindVideo {
		writeError(w, CodeNotFound, "文件不存在")
		return
	}
	if s.Store == nil {
		writeError(w, CodeInternal, "进度存储不可用")
		return
	}
	rec := progress.Record{Device: dev, Share: sh.ID, Path: in.Path, Position: pos, Duration: dur, UpdatedAt: time.Now()}
	if err := progress.Save(r.Context(), s.Store, rec); err != nil {
		log.Printf("保存进度失败: %v", err)
		writeError(w, CodeInternal, "保存进度失败")
		return
	}
	if s.Activity != nil {
		s.Activity.Progress(activity.VolumeOf(filepath.Join(sh.Path, in.Path)))
	}
	w.WriteHeader(http.StatusNoContent)
}

// fillProgress 为目录 dirRel 下的视频条目填充本设备进度：整目录一次查询。失败时只记日志（progress 为 null）。
func (s *Server) fillProgress(ctx context.Context, dev, share, dirRel string, items []listItem) {
	if s.Store == nil {
		return
	}
	m, err := progress.InDir(ctx, s.Store, dev, share, strings.Trim(strings.ReplaceAll(dirRel, `\`, "/"), "/"))
	if err != nil {
		log.Printf("读取进度失败: %v", err)
		return
	}
	for i := range items {
		if items[i].Kind != media.KindVideo {
			continue
		}
		if e, ok := m[strings.ToLower(items[i].Name)]; ok {
			items[i].Progress = &itemProgress{e.Position, e.Duration, e.Watched}
		}
	}
}

// GET /api/continue —— 按「共享+目录」聚合，取所有设备中最近一条（家庭共享进度），最多 20 条。
// 只含当前设备可见的共享（加密片库需有效会话）；文件已不存在（含对本设备隐藏、共享离线）的记录跳过，由同目录较早的记录补位。
// 已看完的条目附 next（下一集）；无下一集则不出现。
func (s *Server) handleContinue(w http.ResponseWriter, r *http.Request) {
	type item struct {
		Share      string     `json:"share"`
		Path       string     `json:"path"`
		Name       string     `json:"name"`
		Position   float64    `json:"position"`
		Duration   float64    `json:"duration"`
		Watched    bool       `json:"watched"`
		UpdatedAt  string     `json:"updatedAt"`
		DeviceName string     `json:"deviceName"`
		Next       *mediaItem `json:"next,omitempty"` // 已看完时的下一集（03 文档 4.6）
	}
	shares := map[string]*config.Share{}
	var ids []string
	for _, sh := range s.allShares() {
		if s.accessible(r, &sh) { // 加密片库仅在带有效 X-Vault 时合并返回
			shares[sh.ID] = &sh
			ids = append(ids, sh.ID)
		}
	}
	items := []item{}
	if s.Store != nil {
		names := s.deviceNames()
		seen := map[string]bool{}
		err := progress.Recent(r.Context(), s.Store, ids, func(rec progress.Record) bool {
			group := rec.Share + "\x00" + path.Dir(progress.Key(rec.Path))
			if seen[group] {
				return true
			}
			f, fi, err := openInShare(shares[rec.Share], rec.Path)
			if err != nil {
				return true // 孤儿记录：读取时跳过（03 文档 4.8）
			}
			f.Close()
			if fi.IsDir() {
				return true
			}
			seen[group] = true
			it := item{rec.Share, rec.Path, path.Base(rec.Path), rec.Position, rec.Duration, rec.Watched,
				rec.UpdatedAt.UTC().Format(time.RFC3339), names[rec.Device], nil}
			if rec.Watched {
				// 看完：附下一集；无下一集则整条不出现（同目录较早的记录也不补位）
				dir, name := path.Split(rec.Path)
				dir = strings.TrimSuffix(dir, "/")
				sib, err := siblingFiles(shares[rec.Share], dir, name)
				if err != nil {
					return true
				}
				n := nextVideo(dir, name, sib)
				if n == nil {
					return true
				}
				it.Next = &mediaItem{*n, path.Join(dir, n.Name)}
			}
			items = append(items, it)
			return len(items) < maxContinue
		})
		if err != nil {
			log.Printf("读取继续观看失败: %v", err)
			writeError(w, CodeInternal, "读取进度失败")
			return
		}
	}
	writeJSON(w, map[string]any{"items": items})
}

// deviceNames 返回设备 ID → 设备名（仅正式鉴权服务可用；测试用 TokenMap 时为空）。
func (s *Server) deviceNames() map[string]string {
	m := map[string]string{}
	if svc, ok := s.Auth.(*auth.Service); ok && svc.Store != nil {
		for _, d := range svc.Store.List() {
			m[d.DeviceID] = d.DeviceName
		}
	}
	return m
}
