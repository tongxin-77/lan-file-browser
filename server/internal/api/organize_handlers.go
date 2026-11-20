package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"lfb/server/internal/config"
	"lfb/server/internal/organize"
)

// 自动整理（06 文档第 11 节），全部仅本机：读接口经 localOnly，修改类经 adminWrite（CSRF）。
// 路径校验（绝对路径、存在、源与库互不包含、跨片库不重叠）全部在这里执行，见 organize.CheckLayout。

const maxOrganizeSources = 20

var reSourceID = regexp.MustCompile(`^[a-z0-9]{1,16}$`)

// organizer 返回整理调度器；未启动（测试中可能为 nil）时写错误。
func (s *Server) organizer(w http.ResponseWriter) (*organize.Manager, bool) {
	if s.Organizer == nil || s.Store == nil {
		writeError(w, CodeInternal, "自动整理未启动")
		return nil, false
	}
	return s.Organizer, true
}

// organizeShare 返回 {id} 对应的片库快照，不存在时写 404。
func (s *Server) organizeShare(w http.ResponseWriter, r *http.Request) (*config.Share, bool) {
	sh := s.Config.Get().Share(r.PathValue("id"))
	if sh == nil {
		writeError(w, CodeNotFound, "片库不存在")
		return nil, false
	}
	return sh, true
}

type sourceHint struct {
	ID         string `json:"id"`
	SameDisk   bool   `json:"sameDisk"`   // 与库目录同一块物理盘：整理后仍会争用（第 1 节同盘提醒）
	SameVolume bool   `json:"sameVolume"` // 同一分区：移动模式直接重命名
}

// writeOrganize 输出 GET /organize 的完整内容：配置 + 默认值 + 同盘提醒 + 状态。
func (s *Server) writeOrganize(w http.ResponseWriter, m *organize.Manager, id string) {
	cfg := s.Config.Get()
	sh := cfg.Share(id)
	if sh == nil {
		writeError(w, CodeNotFound, "片库不存在")
		return
	}
	org := config.Organize{Sources: []config.OrganizeSource{}}
	if sh.Organize != nil {
		org = *sh.Organize
		if org.Sources == nil {
			org.Sources = []config.OrganizeSource{}
		}
	}
	hints := []sourceHint{}
	for _, src := range org.Sources {
		c := m.CheckPath(sh.Path, src.Path)
		hints = append(hints, sourceHint{src.ID, c.SameDisk, c.SameVolume})
	}
	st, err := m.Status(id)
	if err != nil {
		log.Printf("读取整理状态失败: %v", err)
		writeError(w, CodeInternal, "读取整理状态失败")
		return
	}
	writeJSON(w, map[string]any{"organize": org, "defaults": cfg.OrganizeDefaults, "sources": hints, "status": st})
}

// GET /api/admin/shares/{id}/organize —— 配置 + 状态
func (s *Server) handleOrganizeGet(w http.ResponseWriter, r *http.Request) {
	m, ok := s.organizer(w)
	if !ok {
		return
	}
	s.writeOrganize(w, m, r.PathValue("id"))
}

// checkOrganizeInput 校验并规范化整理配置（不含跨片库布局，布局在 Update 中用 CheckLayout 校验）。
func checkOrganizeInput(in *config.Organize, d config.OrganizeDefaults) error {
	if len(in.Sources) > maxOrganizeSources {
		return &adminErr{CodeBadPath, "源文件夹太多了"}
	}
	for _, v := range []*int{in.Interval, in.RateLimit, in.MinFree, in.MaxRetries} {
		if v != nil && (*v < 0 || *v > 100000) {
			return &adminErr{CodeBadPath, "参数超出范围"}
		}
	}
	if in.Interval != nil && *in.Interval < 1 {
		return &adminErr{CodeBadPath, "扫描间隔至少 1 分钟"}
	}
	ids := map[string]bool{}
	for i := range in.Sources {
		src := &in.Sources[i]
		p, err := checkShareDir(strings.Trim(strings.TrimSpace(src.Path), `"`))
		if err != nil {
			return err
		}
		src.Path = p
		switch src.Mode {
		case config.OrganizeCopy:
			src.AfterMove = ""
		case config.OrganizeMove:
			if src.AfterMove == "" {
				src.AfterMove = d.AfterMove
			}
			if src.AfterMove != config.AfterMoveRecycle && src.AfterMove != config.AfterMoveDelete {
				return &adminErr{CodeBadPath, "移动后的处理方式不对"}
			}
		default:
			return &adminErr{CodeBadPath, "请选择复制或移动"}
		}
		if !reSourceID.MatchString(src.ID) || ids[src.ID] {
			src.ID = newSourceID(ids)
		}
		ids[src.ID] = true
	}
	return nil
}

func newSourceID(used map[string]bool) string {
	for {
		id := config.NewShareID()[:4]
		if !used[id] {
			return id
		}
	}
}

func layoutErr(err error) error {
	var le *organize.LayoutError
	if errors.As(err, &le) {
		if le.Conflict {
			return &adminErr{CodeConflict, le.Msg}
		}
		return &adminErr{CodeBadPath, le.Msg}
	}
	return err
}

// PUT /api/admin/shares/{id}/organize —— 整体保存配置
func (s *Server) handleOrganizePut(w http.ResponseWriter, r *http.Request) {
	m, ok := s.organizer(w)
	if !ok {
		return
	}
	var in config.Organize
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeError(w, CodeBadPath, "参数非法")
		return
	}
	id := r.PathValue("id")
	err := s.Config.Update(func(c *config.Config) error {
		sh := c.Share(id)
		if sh == nil {
			return &adminErr{CodeNotFound, "片库不存在"}
		}
		if err := checkOrganizeInput(&in, c.OrganizeDefaults); err != nil {
			return err
		}
		sh.Organize = &in
		return layoutErr(organize.CheckLayout(c))
	})
	if err != nil {
		writeAdminErr(w, err)
		return
	}
	m.Wake()
	s.writeOrganize(w, m, id)
}

// POST /api/admin/shares/{id}/organize/scan —— 立即扫描，202
func (s *Server) handleOrganizeScan(w http.ResponseWriter, r *http.Request) {
	m, ok := s.organizer(w)
	if !ok {
		return
	}
	sh, ok := s.organizeShare(w, r)
	if !ok {
		return
	}
	if sh.Organize == nil || !sh.Organize.Enabled {
		writeError(w, CodeConflict, "请先开启自动整理")
		return
	}
	m.ScanNow(sh.ID)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte("{}\n"))
}

// POST /api/admin/shares/{id}/organize/dry-run —— 试运行：只列出，不复制、不写记录
func (s *Server) handleOrganizeDryRun(w http.ResponseWriter, r *http.Request) {
	m, ok := s.organizer(w)
	if !ok {
		return
	}
	sh, ok := s.organizeShare(w, r)
	if !ok {
		return
	}
	if err := organize.CheckLayout(s.Config.Get()); err != nil {
		writeAdminErr(w, layoutErr(err))
		return
	}
	items, total, err := m.DryRun(sh.ID)
	if err != nil {
		writeError(w, CodeShareOffline, "片库所在硬盘未连接")
		return
	}
	writeJSON(w, map[string]any{"items": items, "total": total})
}

func (s *Server) setOrganizePaused(w http.ResponseWriter, r *http.Request, paused bool) {
	m, ok := s.organizer(w)
	if !ok {
		return
	}
	id := r.PathValue("id")
	err := s.Config.Update(func(c *config.Config) error {
		sh := c.Share(id)
		if sh == nil {
			return &adminErr{CodeNotFound, "片库不存在"}
		}
		if sh.Organize == nil {
			return &adminErr{CodeConflict, "这个片库还没有设置自动整理"}
		}
		sh.Organize.Paused = paused
		return nil
	})
	if err != nil {
		writeAdminErr(w, err)
		return
	}
	if !paused {
		m.ScanNow(id) // 继续：尽快处理排队中的文件
	}
	s.writeOrganize(w, m, id)
}

// POST /api/admin/shares/{id}/organize/pause
func (s *Server) handleOrganizePause(w http.ResponseWriter, r *http.Request) {
	s.setOrganizePaused(w, r, true)
}

// POST /api/admin/shares/{id}/organize/resume
func (s *Server) handleOrganizeResume(w http.ResponseWriter, r *http.Request) {
	s.setOrganizePaused(w, r, false)
}

// GET /api/admin/shares/{id}/organize/items?state=&offset=&limit=
func (s *Server) handleOrganizeItems(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.organizer(w); !ok {
		return
	}
	sh, ok := s.organizeShare(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	state := q.Get("state")
	offset, err1 := intParam(q.Get("offset"), 0)
	limit, err2 := intParam(q.Get("limit"), 50)
	valid := []string{"", organize.StateQueued, organize.StateCopying, organize.StateDone, organize.StateDuplicate, organize.StateFailed}
	if err1 != nil || err2 != nil || offset < 0 || limit < 1 || limit > 500 || !slices.Contains(valid, state) {
		writeError(w, CodeBadPath, "参数非法")
		return
	}
	items, total, err := organize.ListItems(s.Store, sh.ID, state, offset, limit)
	if err != nil {
		log.Printf("读取整理记录失败: %v", err)
		writeError(w, CodeInternal, "读取整理记录失败")
		return
	}
	writeJSON(w, map[string]any{"items": items, "total": total})
}

// POST /api/admin/shares/{id}/organize/items/retry —— Body {ids: []}
func (s *Server) handleOrganizeRetry(w http.ResponseWriter, r *http.Request) {
	m, ok := s.organizer(w)
	if !ok {
		return
	}
	sh, ok := s.organizeShare(w, r)
	if !ok {
		return
	}
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil || len(in.IDs) > 1000 {
		writeError(w, CodeBadPath, "参数非法")
		return
	}
	n, err := organize.RetryItems(s.Store, sh.ID, in.IDs, time.Now())
	if err != nil {
		log.Printf("重试整理记录失败: %v", err)
		writeError(w, CodeInternal, "重试失败")
		return
	}
	if n > 0 && sh.Organize != nil && sh.Organize.Enabled {
		m.ScanNow(sh.ID)
	}
	writeJSON(w, map[string]any{"retried": n})
}

// DELETE /api/admin/shares/{id}/organize/items/{itemId} —— 重新整理
func (s *Server) handleOrganizeDeleteItem(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.organizer(w); !ok {
		return
	}
	sh, ok := s.organizeShare(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("itemId"), 10, 64)
	if err != nil {
		writeError(w, CodeNotFound, "记录不存在")
		return
	}
	found, err := organize.DeleteItem(s.Store, sh.ID, id)
	if err != nil {
		log.Printf("删除整理记录失败: %v", err)
		writeError(w, CodeInternal, "删除失败")
		return
	}
	if !found {
		writeError(w, CodeNotFound, "记录不存在")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/admin/organize/defaults
func (s *Server) handleOrganizeDefaultsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.Config.Get().OrganizeDefaults)
}

// PUT /api/admin/organize/defaults
func (s *Server) handleOrganizeDefaultsPut(w http.ResponseWriter, r *http.Request) {
	var in config.OrganizeDefaults
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		writeError(w, CodeBadPath, "参数非法")
		return
	}
	if in.AfterMove == "" {
		in.AfterMove = config.AfterMoveRecycle
	}
	if in.Interval < 1 || in.Interval > 100000 || in.RateLimit < 0 || in.RateLimit > 100000 || in.MaxRetries < 0 || in.MaxRetries > 100 ||
		in.MinFree != nil && (*in.MinFree < 0 || *in.MinFree > 100000) ||
		in.AfterMove != config.AfterMoveRecycle && in.AfterMove != config.AfterMoveDelete {
		writeError(w, CodeBadPath, "参数超出范围")
		return
	}
	if err := s.Config.Update(func(c *config.Config) error { c.OrganizeDefaults = in; return nil }); err != nil {
		writeAdminErr(w, err)
		return
	}
	if s.Organizer != nil {
		s.Organizer.Wake()
	}
	writeJSON(w, in)
}

// POST /api/admin/organize/check-path —— Body {sharePath, sourcePath}
func (s *Server) handleOrganizeCheckPath(w http.ResponseWriter, r *http.Request) {
	m, ok := s.organizer(w)
	if !ok {
		return
	}
	var in struct {
		SharePath  string `json:"sharePath"`
		SourcePath string `json:"sourcePath"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil ||
		!filepath.IsAbs(in.SharePath) || !filepath.IsAbs(in.SourcePath) {
		writeError(w, CodeBadPath, "请填写完整路径")
		return
	}
	writeJSON(w, m.CheckPath(filepath.Clean(in.SharePath), filepath.Clean(in.SourcePath)))
}
