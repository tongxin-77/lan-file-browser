// LAN File Browser 服务端入口。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
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
	"lfb/server/internal/organize"
	"lfb/server/internal/store"
	"lfb/server/internal/thumb"
)

var version = "dev" // 由 -ldflags "-X main.version=..." 注入

func main() {
	dataDir := flag.String("data", "", `数据目录（默认 %APPDATA%\LanFileBrowser）`)
	port := flag.Int("port", 0, "HTTP 端口（默认取配置，8686）")
	flag.Parse()

	if err := run(*dataDir, *port); err != nil {
		log.Fatal(err)
	}
}

func run(dataDir string, port int) error {
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
	org := organize.New(organize.Options{Config: cfgStore, DB: db, Activity: act})
	srv := &api.Server{Config: cfgStore, Auth: svc, Version: version, Port: port, Store: db, Activity: act,
		Thumbs: thumb.New(thumbDir, ffmpeg), Organizer: org}
	hs := api.NewHTTPServer(fmt.Sprintf(":%d", port), srv.Handler())

	// SIGTERM：Windows 关闭控制台窗口、注销、关机（CTRL_CLOSE/LOGOFF/SHUTDOWN_EVENT）
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go act.Run(ctx)
	orgDone := make(chan struct{})
	go func() { org.Run(ctx); close(orgDone) }()

	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	log.Printf("LAN File Browser %s 已启动：端口 %d，数据目录 %s", version, port, dataDir)
	log.Printf("管理页（仅本机）: http://127.0.0.1:%d/", port)
	go printPairingLoop(ctx, cfg, svc.Codes, port)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Print("正在退出…")
	shutdown(hs, 5*time.Second)
	<-orgDone   // 整理：当前块写完后停止并删除临时文件，之后才能关闭数据库
	act.Close() // 恢复睡眠；之后 defer 依次关闭 keeper、数据库
	return nil
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
