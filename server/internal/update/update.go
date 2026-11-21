// Package update 检查 GitHub Release 新版本，并为 App 缓存 APK（02 文档第 5 节）。
package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"lfb/server/internal/config"
)

const (
	DefaultAPI    = "https://api.github.com"
	repo          = "tongxin-77/lan-file-browser"
	userAgent     = "lfb-server" // 通用客户端名，不含用户信息
	CheckInterval = 24 * time.Hour
	cacheTTL      = time.Hour // 按需检查（MaybeRefresh）的结果缓存时长
	githubPrefix  = "https://github.com/"

	maxAPIBody   = 1 << 20
	maxChecksums = 64 << 10
	maxAPK       = 512 << 20
)

// ABIs 为 Release 中按 ABI 拆分的 APK（资产名 lfb-app-<abi>.apk）。
var ABIs = []string{"arm64-v8a", "armeabi-v7a"}

func apkName(abi string) string { return "lfb-app-" + abi + ".apk" }

type Options struct {
	Current  string                       // 当前服务端版本（main.version）；"dev" 时不检查
	APIBase  string                       // GitHub API 基址，空 = DefaultAPI（测试可注入）
	CacheDir string                       // APK 缓存目录 <数据目录>\cache\apk
	Settings func() config.UpdateSettings // 每次检查时读取，配置修改即时生效
	Client   *http.Client                 // 空 = 默认客户端
	Now      func() time.Time
}

// Release 为最新 Release 的解析结果，创建后只读。下载地址均已套用镜像。
type Release struct {
	Version      string            // 不带 v 前缀
	Notes        string            // 发布说明（Markdown 原文）
	PageURL      string            // Release 页面
	InstallerURL string            // lfb-server-setup-*.exe，可能为空
	APKURLs      map[string]string // abi → 下载地址
	SHA256       map[string]string // 资产名 → sha256（小写 hex），来自 checksums.txt
}

// Checker 并发安全。
type Checker struct {
	opt Options

	mu      sync.Mutex
	latest  *Release
	attempt time.Time // 上次尝试检查
	checked time.Time // 上次成功检查
	failed  bool      // 上次检查失败
	apkVer  string
	apks    map[string]string // abi → 已校验的本地文件（apkVer 版本）

	refreshing atomic.Bool
	bg         sync.WaitGroup
	dl         sync.Mutex // 串行化 APK 下载
}

func New(opt Options) *Checker {
	if opt.APIBase == "" {
		opt.APIBase = DefaultAPI
	}
	if opt.Client == nil {
		opt.Client = &http.Client{}
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	return &Checker{opt: opt}
}

// disabled 返回不检查的原因："dev"（开发版或版本号无法解析）、"config"（update.enabled=false），否则空。
func (c *Checker) disabled() string {
	if _, err := parseVersion(c.opt.Current); err != nil {
		return "dev"
	}
	if !c.opt.Settings().Enabled {
		return "config"
	}
	return ""
}

// Run 启动时与每 24 小时检查一次并缓存 APK，直到 ctx 结束。
func (c *Checker) Run(ctx context.Context) {
	t := time.NewTicker(CheckInterval)
	defer t.Stop()
	for {
		if c.Check(ctx) == nil {
			c.CacheAPKs(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// MaybeRefresh 在距上次检查超过 1 小时时后台检查并缓存 APK；不阻塞调用方。
func (c *Checker) MaybeRefresh() {
	if c.disabled() != "" {
		return
	}
	c.mu.Lock()
	fresh := !c.attempt.IsZero() && c.opt.Now().Sub(c.attempt) < cacheTTL
	c.mu.Unlock()
	if fresh || !c.refreshing.CompareAndSwap(false, true) {
		return
	}
	c.bg.Add(1)
	go func() {
		defer c.bg.Done()
		defer c.refreshing.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if c.Check(ctx) == nil {
			c.CacheAPKs(ctx)
		}
	}()
}

// CacheAPKsAsync 在后台缓存 APK（供“立即检查”使用，避免阻塞请求）。
func (c *Checker) CacheAPKsAsync() {
	c.bg.Add(1)
	go func() {
		defer c.bg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		c.CacheAPKs(ctx)
	}()
}

// Wait 等待后台检查与下载结束（测试用）。
func (c *Checker) Wait() { c.bg.Wait() }

// Check 立即请求 GitHub 最新 Release（预发布规则见 latestRelease）。失败只记日志并返回错误，保留上次结果。
// 开发版或已关闭时不发请求，返回 nil。
func (c *Checker) Check(ctx context.Context) error {
	if c.disabled() != "" {
		return nil
	}
	c.mu.Lock()
	c.attempt = c.opt.Now()
	c.mu.Unlock()
	rel, err := c.fetch(ctx, c.opt.Settings().Mirror)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.failed = true
		log.Printf("检查更新失败: %v", err)
		return err
	}
	c.latest, c.checked, c.failed = rel, c.opt.Now(), false
	return nil
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	Draft   bool   `json:"draft"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// latestRelease 选出要提示的 Release：当前为正式版时取 releases/latest（GitHub 跳过预发布）；
// 当前为预发布版时从最近的 Release 列表中（忽略草稿）取 semver 最大者，预发布与正式版都参与。
func (c *Checker) latestRelease(ctx context.Context) (ghRelease, error) {
	var gh ghRelease
	if cur, _ := parseVersion(c.opt.Current); len(cur.pre) == 0 {
		body, err := c.get(ctx, c.opt.APIBase+"/repos/"+repo+"/releases/latest", maxAPIBody)
		if err != nil {
			return gh, err
		}
		if err := json.Unmarshal(body, &gh); err != nil {
			return gh, fmt.Errorf("解析 Release: %w", err)
		}
		return gh, nil
	}
	body, err := c.get(ctx, c.opt.APIBase+"/repos/"+repo+"/releases?per_page=20", maxAPIBody)
	if err != nil {
		return gh, err
	}
	var list []ghRelease
	if err := json.Unmarshal(body, &list); err != nil {
		return gh, fmt.Errorf("解析 Release 列表: %w", err)
	}
	found := false
	for _, r := range list {
		if _, err := parseVersion(r.TagName); r.Draft || err != nil {
			continue
		}
		if n, _ := Compare(r.TagName, gh.TagName); !found || n > 0 {
			gh, found = r, true
		}
	}
	if !found {
		return gh, errors.New("没有可用的 Release")
	}
	return gh, nil
}

func (c *Checker) fetch(ctx context.Context, mirror string) (*Release, error) {
	gh, err := c.latestRelease(ctx)
	if err != nil {
		return nil, err
	}
	ver, err := normalize(gh.TagName)
	if err != nil {
		return nil, err
	}
	rel := &Release{Version: ver, Notes: gh.Body, PageURL: gh.HTMLURL, APKURLs: map[string]string{}, SHA256: map[string]string{}}
	var sumsURL string
	for _, a := range gh.Assets {
		u := applyMirror(a.URL, mirror)
		switch {
		case a.Name == "checksums.txt":
			sumsURL = u
		case strings.HasPrefix(a.Name, "lfb-server-setup-") && strings.HasSuffix(a.Name, ".exe"):
			rel.InstallerURL = u
		default:
			for _, abi := range ABIs {
				if a.Name == apkName(abi) {
					rel.APKURLs[abi] = u
				}
			}
		}
	}
	if sumsURL != "" {
		b, err := c.get(ctx, sumsURL, maxChecksums)
		if err != nil {
			return nil, fmt.Errorf("下载 checksums.txt: %w", err)
		}
		rel.SHA256 = parseChecksums(string(b))
	}
	return rel, nil
}

// applyMirror 把 https://github.com/ 前缀替换为镜像前缀（为空时原样返回）。
func applyMirror(u, mirror string) string {
	if mirror == "" || !strings.HasPrefix(u, githubPrefix) {
		return u
	}
	return strings.TrimRight(mirror, "/") + "/" + strings.TrimPrefix(u, githubPrefix)
}

func (c *Checker) request(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/vnd.github+json, */*")
	resp, err := c.opt.Client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode) // 403/429 多为 GitHub 限流
	}
	return resp, nil
}

func (c *Checker) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := c.request(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// parseChecksums 解析 sha256sum 输出（"<hash>  <name>" 或 "<hash> *<name>"）。
func parseChecksums(s string) map[string]string {
	m := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		hash, name, ok := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		name = strings.TrimPrefix(strings.TrimSpace(name), "*")
		if _, err := hex.DecodeString(hash); !ok || err != nil || len(hash) != 64 || name == "" {
			continue
		}
		m[name] = strings.ToLower(hash)
	}
	return m
}

// CacheAPKs 把最新 Release 的 APK 下载到 <CacheDir>\<版本>\ 并按 checksums 校验 sha256，
// 校验失败删除；只保留最新一个版本的缓存。已有下载进行中时直接返回。
func (c *Checker) CacheAPKs(ctx context.Context) {
	if !c.dl.TryLock() {
		return
	}
	defer c.dl.Unlock()
	c.mu.Lock()
	rel := c.latest
	c.mu.Unlock()
	if rel == nil {
		return
	}
	dir := filepath.Join(c.opt.CacheDir, rel.Version)
	got := map[string]string{}
	for _, abi := range ABIs {
		u, ok := rel.APKURLs[abi]
		if !ok {
			continue
		}
		name := apkName(abi)
		want := rel.SHA256[name]
		if want == "" {
			log.Printf("更新：%s 缺少 sha256 校验值，不缓存", name)
			continue
		}
		p := filepath.Join(dir, name)
		if fileSHA256(p) == want {
			got[abi] = p
			continue
		}
		if err := c.download(ctx, u, p, want); err != nil {
			log.Printf("更新：缓存 %s 失败: %v", name, err)
			continue
		}
		got[abi] = p
	}
	c.mu.Lock()
	if c.latest == rel {
		c.apkVer, c.apks = rel.Version, got
	}
	c.mu.Unlock()

	ents, _ := os.ReadDir(c.opt.CacheDir)
	for _, e := range ents {
		if e.Name() != rel.Version {
			if err := os.RemoveAll(filepath.Join(c.opt.CacheDir, e.Name())); err != nil {
				log.Printf("更新：清理旧 APK 缓存失败: %v", err)
			}
		}
	}
}

// download 下载到 p.part，sha256 一致后改名为 p；否则删除。
func (c *Checker) download(ctx context.Context, url, p, want string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	resp, err := c.request(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	part := p + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxAPK))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != want {
		err = errors.New("sha256 校验失败")
	}
	if err != nil {
		os.Remove(part)
		return err
	}
	os.Remove(p)
	return os.Rename(part, p)
}

func fileSHA256(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Status 为管理页横幅所需信息（GET /api/admin/update）。
type Status struct {
	Current      string     `json:"current"`
	Latest       string     `json:"latest"`
	HasUpdate    bool       `json:"hasUpdate"`
	Notes        string     `json:"notes"`
	InstallerURL string     `json:"installerUrl"`
	ReleaseURL   string     `json:"releaseUrl"`
	CheckedAt    *time.Time `json:"checkedAt"` // 上次成功检查，未检查过为 null
	Failed       bool       `json:"failed"`    // 上次检查失败（保留更早的结果）
	Disabled     string     `json:"disabled"`  // "dev" 开发版 / "config" 已关闭 / ""
}

func (c *Checker) Status() Status {
	s := Status{Current: c.opt.Current, Disabled: c.disabled()}
	c.mu.Lock()
	defer c.mu.Unlock()
	s.Failed = c.failed
	if !c.checked.IsZero() {
		t := c.checked
		s.CheckedAt = &t
	}
	if r := c.latest; r != nil && s.Disabled == "" {
		s.Latest, s.Notes, s.InstallerURL, s.ReleaseURL = r.Version, r.Notes, r.InstallerURL, r.PageURL
		n, err := Compare(r.Version, c.opt.Current)
		s.HasUpdate = err == nil && n > 0
	}
	return s
}

// App 返回最新 Release 与其中已缓存的 APK（abi → 本地路径）；未检查到 Release 时 rel 为 nil。
func (c *Checker) App() (rel *Release, apks map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	apks = map[string]string{}
	if c.latest == nil {
		return nil, apks
	}
	if c.apkVer == c.latest.Version {
		for k, v := range c.apks {
			apks[k] = v
		}
	}
	return c.latest, apks
}

// APK 返回 abi 对应的已缓存 APK 路径与版本。
func (c *Checker) APK(abi string) (path, version string, ok bool) {
	rel, apks := c.App()
	if p, found := apks[abi]; found {
		return p, rel.Version, true
	}
	return "", "", false
}
