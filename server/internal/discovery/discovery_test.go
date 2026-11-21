package discovery

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

var testInfo = Info{ID: "sid", Name: "电脑", Version: "1.2.3", Port: 8686, IPs: []string{"192.168.1.5", "10.0.0.3"}}

// start 在随机端口启动应答，返回其地址、客户端 socket 与 Serve 的返回通道。
func start(t *testing.T, ctx context.Context, info Info, now func() time.Time) (net.Addr, net.PacketConn, chan error) {
	t.Helper()
	srv, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- serve(ctx, srv, func() Info { return info }, now) }()
	cli, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cli.Close() })
	return srv.LocalAddr(), cli, done
}

// recv 在 wait 内读一个回复；超时返回 nil。
func recv(t *testing.T, cli net.PacketConn, wait time.Duration) []byte {
	t.Helper()
	cli.SetReadDeadline(time.Now().Add(wait))
	buf := make([]byte, 2048)
	n, _, err := cli.ReadFrom(buf)
	if err != nil {
		return nil
	}
	return buf[:n]
}

func TestReply(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, cli, _ := start(t, ctx, testInfo, time.Now)
	cli.WriteTo([]byte(Request), addr)
	body := recv(t, cli, 2*time.Second)
	if body == nil {
		t.Fatal("未收到回复")
	}
	var got Info
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "sid" || got.Name != "电脑" || got.Version != "1.2.3" || got.Port != 8686 ||
		strings.Join(got.IPs, ",") != "192.168.1.5,10.0.0.3" {
		t.Fatalf("回复: %s", body)
	}
	var m map[string]any
	json.Unmarshal(body, &m)
	if len(m) != 5 {
		t.Fatalf("回复只应含 id/name/version/port/ips: %s", body)
	}
}

func TestIgnoresInvalid(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, cli, _ := start(t, ctx, testInfo, time.Now)
	for _, p := range []string{"", "LFB_DISCOVE", "LFB_DISCOVER ", "LFB_DISCOVER\n", "lfb_discover", "LFB_DISCOVEX",
		Request + strings.Repeat("x", 2000)} {
		cli.WriteTo([]byte(p), addr)
	}
	if body := recv(t, cli, 300*time.Millisecond); body != nil {
		t.Fatalf("非法包不应回复: %s", body)
	}
	// 非法包之后仍正常应答
	cli.WriteTo([]byte(Request), addr)
	if recv(t, cli, 2*time.Second) == nil {
		t.Fatal("非法包后未再应答")
	}
}

func TestIPsCapped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in := testInfo
	in.IPs = nil
	for range 40 {
		in.IPs = append(in.IPs, "192.168.100.100")
	}
	addr, cli, _ := start(t, ctx, in, time.Now)
	cli.WriteTo([]byte(Request), addr)
	var got Info
	if err := json.Unmarshal(recv(t, cli, 2*time.Second), &got); err != nil || len(got.IPs) != maxIPs {
		t.Fatalf("ips 应截断为 %d: %d %v", maxIPs, len(got.IPs), err)
	}
}

func TestRateLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clock := time.Unix(1000, 0)
	tick := make(chan struct{})
	now := func() time.Time {
		select {
		case <-tick:
			clock = clock.Add(time.Second)
		default:
		}
		return clock
	}
	addr, cli, _ := start(t, ctx, testInfo, now)
	count := func() int {
		for range 3 * perIPLimit {
			cli.WriteTo([]byte(Request), addr)
		}
		n := 0
		for recv(t, cli, 300*time.Millisecond) != nil {
			n++
		}
		return n
	}
	if n := count(); n != perIPLimit {
		t.Fatalf("同一秒内应只回复 %d 次，实际 %d", perIPLimit, n)
	}
	close(tick) // 之后每次取时间都前进 1 秒
	if n := count(); n != 3*perIPLimit {
		t.Fatalf("换窗口后应恢复，实际 %d", n)
	}
}

func TestLimiterGlobal(t *testing.T) {
	l := limiter{now: func() time.Time { return time.Unix(1000, 0) }}
	n := 0
	for i := range 200 {
		if l.allow(net.IPv4(10, 0, byte(i/250), byte(i%250)).String()) {
			n++
		}
	}
	if n != globalLimit {
		t.Fatalf("全局每秒上限 %d，实际 %d", globalLimit, n)
	}
}

func TestStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	_, _, done := start(t, ctx, testInfo, time.Now)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("取消后应返回 nil: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 取消后未退出")
	}
}
