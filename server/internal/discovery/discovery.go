// Package discovery 应答局域网自动发现（01 文档 2.2）：
// 监听 UDP 8687，收到内容恰为 "LFB_DISCOVER" 的数据报后向来源单播回复服务器信息 JSON。
package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"
)

const (
	Port    = 8687
	Request = "LFB_DISCOVER"

	perIPLimit  = 5  // 每个来源 IP 每秒最多回复次数（App 1 秒内连发 3 次）
	globalLimit = 50 // 所有来源合计每秒最多回复次数，限制被伪造源地址利用时的总流量
	maxIPs      = 16 // 回复中最多带的 IP 数，限制回复大小
)

// Info 为回复内容；不得包含 PIN、配对码、token 等敏感信息。
type Info struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Port    int      `json:"port"`
	IPs     []string `json:"ips"`
}

// Serve 在 conn 上应答发现请求，直到 ctx 取消（此时关闭 conn 并返回 nil）或 conn 被关闭。
// info 在每次回复时调用，以反映改名、IP 变化。
func Serve(ctx context.Context, conn net.PacketConn, info func() Info) error {
	return serve(ctx, conn, info, time.Now)
}

func serve(ctx context.Context, conn net.PacketConn, info func() Info, now func() time.Time) error {
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	lim := limiter{now: now}
	buf := make([]byte, len(Request)+1) // 多 1 字节以识别更长的包
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			continue // Windows 上超长数据报（WSAEMSGSIZE）、ICMP 不可达（WSAECONNRESET）等，忽略
		}
		ua, ok := addr.(*net.UDPAddr)
		if !ok || n != len(Request) || !bytes.Equal(buf[:n], []byte(Request)) || !lim.allow(ua.IP.String()) {
			continue
		}
		in := info()
		if len(in.IPs) > maxIPs {
			in.IPs = in.IPs[:maxIPs]
		}
		if in.IPs == nil {
			in.IPs = []string{}
		}
		body, err := json.Marshal(in)
		if err != nil {
			continue
		}
		conn.WriteTo(body, addr) // 失败（如对方已关闭）忽略
	}
}

// limiter 按 1 秒固定窗口计数。
type limiter struct {
	now    func() time.Time
	window int64
	total  int
	perIP  map[string]int
}

func (l *limiter) allow(ip string) bool {
	if w := l.now().Unix(); w != l.window || l.perIP == nil {
		l.window, l.total, l.perIP = w, 0, map[string]int{}
	}
	if l.total >= globalLimit || l.perIP[ip] >= perIPLimit {
		return false
	}
	l.total++
	l.perIP[ip]++
	return true
}
