// LAN File Browser 服务端入口。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mdp/qrterminal/v3"

	"lfb/server/internal/activity"
	"lfb/server/internal/api"
	"lfb/server/internal/auth"
	"lfb/server/internal/config"
	"lfb/server/internal/discovery"
	"lfb/server/internal/organize"
	"lfb/server/internal/store"
	"lfb/server/internal/thumb"
	"lfb/server/internal/update"
)

var version = "dev" // 由 -ldflags "-X main.version=..." 注入

func main() {
	dataDir := flag.String("data", "", `数据目录（默认 %APPDATA%\LanFileBrowser）`)
	port := flag.Int("port", 0, "HTTP 端口（默认取配置，8686）")
	updateAPI := flag.String("update-api", update.DefaultAPI, "检查更新用的 GitHub API 基址（测试用）")
	noBrowser := flag.Bool("no-browser", false, "首次运行（刚生成配置）时不自动打开管理页")
	flag.Parse()

	if err := run(*dataDir, *port, *updateAPI, !*noBrowser); err != nil {
		log.Fatal(err)
	}
}

func run(dataDir string, port int, updateAPI string, browser bool) error {
	thumbDir, err := thumbCacheDir(dataDir)
	if err != nil {
		return err
	}
	if dataDir == "" {
		d, err := config.DefaultDir()
		if err != nil {
			return err
		}
		dataDir = d
	}
	if !hasConsole() {
		// 不关闭：main 中 log.Fatal 也要写入；进程退出时由系统关闭
		if f, err := openLogFile(dataDir); err == nil {
			log.SetOutput(f)
		}
	}
	firstRun := !configExists(dataDir)
	cfg, err := config.Load(dataDir)
	if err != nil {
		return err
	}
	if port == 0 {
		port = cfg.Port
	}

	svc, err := auth.New(dataDir, time.Now)
	if err != nil {
		return err
	}
	db, err := store.Open(dataDir)
	if err != nil {
		return err
	}
	defer db.Close() // 在 HTTP 服务关闭之后执行
	keeper := activity.NewSystemKeeper()
	defer keeper.Close()
	act := activity.New(activity.Options{Keeper: keeper})

	ffmpeg := thumb.FindFFmpeg()
	if ffmpeg == "" {
		log.Print("未在 PATH 中找到 ffmpeg，视频不生成缩略图")
	}
	cfgStore := config.NewStore(cfg)
	updates := update.New(update.Options{Current: version, APIBase: updateAPI,
		CacheDir: filepath.Join(dataDir, "cache", "apk"),
		Settings: func() config.UpdateSettings { return cfgStore.Get().Update }})
	org := organize.New(organize.Options{Config: cfgStore, DB: db, Activity: act})
	srv := &api.Server{Config: cfgStore, Auth: svc, Version: version, Port: port, Store: db, Activity: act,
		Thumbs: thumb.New(thumbDir, ffmpeg), Updates: updates, Organizer: org}
	hs := api.NewHTTPServer(fmt.Sprintf(":%d", port), srv.Handler())

	// SIGTERM：Windows 关闭控制台窗口、注销、关机（CTRL_CLOSE/LOGOFF/SHUTDOWN_EVENT）
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// 安装 / 卸载程序通过命名事件请求退出（无窗口进程收不到关闭消息）
	if c, cancel, err := notifyShutdownEvent(ctx, shutdownEventName); err != nil {
		log.Printf("退出事件未创建，安装程序将强制结束本进程: %v", err)
	} else {
		ctx = c
		defer cancel()
	}
	go act.Run(ctx)
	updDone := make(chan struct{})
	go func() { updates.Run(ctx); close(updDone) }()
	orgDone := make(chan struct{})
	go func() { org.Run(ctx); close(orgDone) }()

	ln, err := net.Listen("tcp", hs.Addr)
	if err != nil {
		return err
	}
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	adminURL := fmt.Sprintf("http://127.0.0.1:%d/", port)
	log.Printf("LAN File Browser %s 已启动：端口 %d，数据目录 %s", version, port, dataDir)
	log.Printf("管理页（仅本机）: %s", adminURL)
	if firstRun && browser {
		openBrowser(adminURL) // 已在监听，浏览器打开时页面可用
	}
	go printPairingLoop(ctx, cfg, svc.Codes, port)
	if cfg.Discovery.Enabled {
		startDiscovery(ctx, cfgStore, port)
	}

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Print("正在退出…")
	shutdown(hs, 5*time.Second)
	<-updDone   // 更新检查：取消进行中的下载
	<-orgDone   // 整理：当前块写完后停止并删除临时文件，之后才能关闭数据库
	act.Close() // 恢复睡眠；之后 defer 依次关闭 keeper、数据库
	return nil
}

// configExists 报告数据目录下是否已有 config.json（没有即首次运行）。
func configExists(dataDir string) bool {
	_, err := os.Stat(filepath.Join(dataDir, "config.json"))
	return !errors.Is(err, fs.ErrNotExist)
}

// openBrowser 用默认浏览器打开 url；失败只记日志。
func openBrowser(url string) {
	if err := exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start(); err != nil {
		log.Printf("打开管理页失败: %v", err)
	}
}

// thumbCacheDir 返回缩略图缓存目录：指定 -data 时为 <数据目录>\cache\thumbs（便于测试/便携），
// 否则 %LOCALAPPDATA%\LanFileBrowser\cache\thumbs（03 文档 4.5）。
func thumbCacheDir(dataDir string) (string, error) {
	if dataDir != "" {
		return filepath.Join(dataDir, "cache", "thumbs"), nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "LanFileBrowser", "cache", "thumbs"), nil
}

// shutdown 优雅关闭；超时（如长视频流仍在传输）后强制 Close，仍视为正常退出。
func shutdown(hs *http.Server, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := hs.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("优雅关闭未完成（%v），强制关闭连接", err)
		hs.Close()
	}
}

// startDiscovery 监听 UDP 8687 应答自动发现；端口被占用等失败只记日志，不影响 HTTP 服务。
func startDiscovery(ctx context.Context, cfgStore *config.Store, port int) {
	conn, err := net.ListenPacket("udp4", fmt.Sprintf(":%d", discovery.Port))
	if err != nil {
		log.Printf("自动发现未启动（UDP %d）: %v", discovery.Port, err)
		return
	}
	go discovery.Serve(ctx, conn, func() discovery.Info {
		c := cfgStore.Get()
		return discovery.Info{ID: c.ServerID, Name: c.Name, Version: version, Port: port, IPs: api.LocalIPv4s()}
	})
}

// printPairingLoop 打印连接信息，并在 PIN/一次性码刷新（过期或被使用）后重新打印。
func printPairingLoop(ctx context.Context, cfg *config.Config, codes *auth.Codes, port int) {
	var lastPin, lastCode string
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		if pin, code := codes.Current(); pin != lastPin || code != lastCode {
			lastPin, lastCode = pin, code
			printPairing(cfg, port, pin, code)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func printPairing(cfg *config.Config, port int, pin, code string) {
	ips := api.LocalIPv4s()
	link := api.PairingLink(cfg.ServerID, cfg.Name, port, ips, code)
	fmt.Println()
	fmt.Printf("本机地址: %s  端口: %d\n", strings.Join(ips, ", "), port)
	fmt.Println("用 App 扫描二维码连接（5 分钟有效，用后刷新）：")
	qrterminal.GenerateHalfBlock(link, qrterminal.L, os.Stdout)
	fmt.Printf("或输入配对 PIN: %s（5 分钟有效）\n\n", pin)
}
