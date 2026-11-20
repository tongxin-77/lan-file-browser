package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/windows"
	"rsc.io/qr"

	"lfb/server/internal/config"
	"lfb/server/internal/fsx"
	"lfb/server/internal/listing"
)

// 本机管理页接口（均经 localOnly；修改类另经 adminWrite）。

// GET /api/admin/status
func (s *Server) handleAdminStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.Config.Get()
	writeJSON(w, map[string]any{
		"serverId": cfg.ServerID, "name": cfg.Name, "port": s.port(), "version": s.Version, "ips": LocalIPv4s(),
	})
}

// GET /api/admin/pairing
func (s *Server) handleAdminPairing(w http.ResponseWriter, r *http.Request) {
	if svc, ok := s.authService(w, r); ok {
		s.writePairing(w, svc.Codes.CurrentWithExpiry)
	}
}

// POST /api/admin/pairing/refresh —— 刷新 PIN/一次性码并解除全部锁定
func (s *Server) handleAdminPairingRefresh(w http.ResponseWriter, r *http.Request) {
	if svc, ok := s.authService(w, r); ok {
		svc.Codes.Refresh()
		s.writePairing(w, svc.Codes.CurrentWithExpiry)
	}
}

func (s *Server) writePairing(w http.ResponseWriter, current func() (string, string, time.Time)) {
	pin, code, exp := current()
	cfg := s.Config.Get()
	link := PairingLink(cfg.ServerID, cfg.Name, s.port(), LocalIPv4s(), code)
	svg, err := qrSVG(link)
	if err != nil {
		log.Printf("生成二维码失败: %v", err)
		writeError(w, CodeInternal, "生成二维码失败")
		return
	}
	writeJSON(w, map[string]any{"pin": pin, "expiresAt": exp, "code": code, "link": link, "qrSvg": svg})
}

// PairingLink 生成二维码内容（01 文档 2.1）。
func PairingLink(serverID, name string, port int, ips []string, code string) string {
	return fmt.Sprintf("lfb://connect?id=%s&name=%s&port=%d&ips=%s&code=%s",
		url.QueryEscape(serverID), url.QueryEscape(name), port, strings.Join(ips, ","), code)
}

// qrSVG 把 text 编码为二维码 SVG（四周留 2 格静区，单个 path 绘制黑块）。
func qrSVG(text string) (string, error) {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", err
	}
	const quiet = 2
	n := c.Size + 2*quiet
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges"><rect width="%d" height="%d" fill="#fff"/><path fill="#2A1D24" d="`, n, n, n, n)
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if c.Black(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+quiet, y+quiet)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String(), nil
}

// LocalIPv4s 返回本机全部候选 IPv4：排除回环与 169.254 链路本地，保留虚拟网卡。
func LocalIPv4s() []string {
	ips := []string{}
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if ip := n.IP.To4(); ip != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
				ips = append(ips, ip.String())
			}
		}
	}
	return ips
}

// adminShare 是管理页看到的共享（含电脑真实路径）。
type adminShare struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Path    string       `json:"path"`
	Perms   config.Perms `json:"perms"`
	Devices []string     `json:"devices"` // 空 = 全部手机
	Status  string       `json:"status"`  // ok / offline
}

func toAdminShare(sh config.Share) adminShare {
	devices := sh.Devices
	if devices == nil {
		devices = []string{}
	}
	return adminShare{sh.ID, sh.Name, sh.Path, sh.Perms, devices, shareStatus(sh.Path)}
}

// GET /api/admin/shares
func (s *Server) handleAdminShares(w http.ResponseWriter, r *http.Request) {
	items := []adminShare{}
	for _, sh := range s.allShares() {
		items = append(items, toAdminShare(sh))
	}
	writeJSON(w, items)
}

// shareInput 是新建/修改共享的请求体；修改为整体替换这四个字段。
type shareInput struct {
	Name    string       `json:"name"`
	Path    string       `json:"path"`
	Perms   config.Perms `json:"perms"`
	Devices []string     `json:"devices"`
}

// adminErr 是 Update 回调中的校验错误，映射为对应错误码。
type adminErr struct{ code, msg string }

func (e *adminErr) Error() string { return e.msg }

const maxShareName = 64

func readShareInput(w http.ResponseWriter, r *http.Request) (shareInput, error) {
	var in shareInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		return in, &adminErr{CodeBadPath, "参数非法"}
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Path = strings.TrimSpace(in.Path)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > maxShareName {
		return in, &adminErr{CodeBadPath, fmt.Sprintf("名称不能为空，且不超过 %d 个字", maxShareName)}
	}
	devices := []string{}
	for _, d := range in.Devices {
		if d != "" && !slices.Contains(devices, d) {
			devices = append(devices, d)
		}
	}
	in.Devices = devices
	return in, nil
}

// checkShareDir 要求 p 为存在的目录的绝对路径，返回规范化路径。
func checkShareDir(p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", &adminErr{CodeBadPath, "请填写完整路径，以盘符开头，例如 D:\\Movies"}
	}
	p = filepath.Clean(p)
	fi, err := os.Stat(p)
	if err != nil {
		return "", &adminErr{CodeBadPath, "找不到这个文件夹"}
	}
	if !fi.IsDir() {
		return "", &adminErr{CodeBadPath, "这不是文件夹"}
	}
	return p, nil
}

func checkNameFree(c *config.Config, name, selfID string) error {
	for _, sh := range c.Shares {
		if sh.ID != selfID && strings.EqualFold(sh.Name, name) {
			return &adminErr{CodeConflict, "已经有叫“" + name + "”的片库了"}
		}
	}
	return nil
}

func writeAdminErr(w http.ResponseWriter, err error) {
	var ae *adminErr
	if errors.As(err, &ae) {
		writeError(w, ae.code, ae.msg)
		return
	}
	log.Printf("保存配置失败: %v", err)
	writeError(w, CodeInternal, "保存配置失败")
}

// POST /api/admin/shares
func (s *Server) handleAdminCreateShare(w http.ResponseWriter, r *http.Request) {
	in, err := readShareInput(w, r)
	if err != nil {
		writeAdminErr(w, err)
		return
	}
	var created config.Share
	err = s.Config.Update(func(c *config.Config) error {
		p, err := checkShareDir(in.Path)
		if err != nil {
			return err
		}
		if err := checkNameFree(c, in.Name, ""); err != nil {
			return err
		}
		id := config.NewShareID()
		for c.Share(id) != nil {
			id = config.NewShareID()
		}
		created = config.Share{ID: id, Name: in.Name, Path: p, Perms: in.Perms, Devices: in.Devices}
		c.Shares = append(c.Shares, created)
		return nil
	})
	if err != nil {
		writeAdminErr(w, err)
		return
	}
	writeJSON(w, toAdminShare(created))
}

// PUT /api/admin/shares/{id}
func (s *Server) handleAdminUpdateShare(w http.ResponseWriter, r *http.Request) {
	in, err := readShareInput(w, r)
	if err != nil {
		writeAdminErr(w, err)
		return
	}
	id := r.PathValue("id")
	var updated config.Share
	err = s.Config.Update(func(c *config.Config) error {
		sh := c.Share(id)
		if sh == nil {
			return &adminErr{CodeNotFound, "片库不存在"}
		}
		// 路径未改动时不校验存在性：硬盘未连接的片库仍可改名、改权限
		if !strings.EqualFold(filepath.Clean(in.Path), sh.Path) {
			p, err := checkShareDir(in.Path)
			if err != nil {
				return err
			}
			sh.Path = p
		}
		if err := checkNameFree(c, in.Name, id); err != nil {
			return err
		}
		sh.Name, sh.Perms, sh.Devices = in.Name, in.Perms, in.Devices
		updated = *sh
		return nil
	})
	if err != nil {
		writeAdminErr(w, err)
		return
	}
	writeJSON(w, toAdminShare(updated))
}

// DELETE /api/admin/shares/{id} —— 只移除共享配置，不动磁盘文件
func (s *Server) handleAdminDeleteShare(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.Config.Update(func(c *config.Config) error {
		i := slices.IndexFunc(c.Shares, func(sh config.Share) bool { return sh.ID == id })
		if i < 0 {
			return &adminErr{CodeNotFound, "片库不存在"}
		}
		c.Shares = slices.Delete(c.Shares, i, i+1)
		return nil
	})
	if err != nil {
		writeAdminErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/admin/browse?path= —— 本机文件夹选择器：path 为空列盘符，否则列子文件夹（不列文件、
// 不列隐藏/系统项）。parent 为空表示上一级是盘符列表。
func (s *Server) handleAdminBrowse(w http.ResponseWriter, r *http.Request) {
	type item struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	items := []item{}
	p := r.URL.Query().Get("path")
	if p == "" {
		mask, _ := windows.GetLogicalDrives()
		for i := 0; i < 26; i++ {
			d := string(rune('A'+i)) + `:\`
			if mask&(1<<i) == 0 {
				continue
			}
			if _, err := os.Stat(d); err == nil { // 跳过无盘的光驱/读卡器
				items = append(items, item{d, d})
			}
		}
		writeJSON(w, map[string]any{"path": "", "parent": "", "items": items})
		return
	}
	if !filepath.IsAbs(p) {
		writeError(w, CodeBadPath, "路径非法")
		return
	}
	p = filepath.Clean(p)
	entries, err := os.ReadDir(p)
	if err != nil {
		writeError(w, CodeNotFound, "无法打开这个文件夹")
		return
	}
	for _, e := range entries {
		if fsx.IsAlwaysHidden(e.Name()) || e.Type().IsRegular() {
			continue
		}
		full := filepath.Join(p, e.Name())
		if info, err := e.Info(); err != nil || fsx.IsHidden(info) {
			continue
		}
		if !e.IsDir() { // 链接/联接：看目标是否为目录
			if fi, err := os.Stat(full); err != nil || !fi.IsDir() {
				continue
			}
		}
		items = append(items, item{e.Name(), full})
	}
	slices.SortFunc(items, func(a, b item) int {
		switch {
		case listing.NaturalLess(a.Name, b.Name):
			return -1
		case listing.NaturalLess(b.Name, a.Name):
			return 1
		}
		return 0
	})
	parent := filepath.Dir(p)
	if parent == p {
		parent = ""
	}
	writeJSON(w, map[string]any{"path": p, "parent": parent, "items": items})
}
