package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestServer() *Server {
	return &Server{Auth: TokenMap{"tok1": "dev1"}}
}

func do(t *testing.T, h http.Handler, method, url, remote, auth string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, url, nil)
	r.RemoteAddr = remote
	if auth != "" {
		r.Header.Set("Authorization", "Bearer "+auth)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func errCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("非 JSON 错误格式: %q", w.Body.String())
	}
	return body.Error.Code
}

func TestRequireDevice(t *testing.T) {
	var got string
	h := newTestServer().requireDevice(func(w http.ResponseWriter, r *http.Request) {
		got = DeviceID(r.Context())
	})

	w := do(t, h, "GET", "/api/list", "192.168.1.9:5000", "")
	if w.Code != 401 || errCode(t, w) != CodeUnauthorized {
		t.Fatalf("无 token: %d %s", w.Code, w.Body)
	}
	w = do(t, h, "GET", "/api/list", "192.168.1.9:5000", "bad")
	if w.Code != 401 {
		t.Fatalf("错误 token: %d", w.Code)
	}
	w = do(t, h, "GET", "/api/list", "192.168.1.9:5000", "tok1")
	if w.Code != 200 || got != "dev1" {
		t.Fatalf("有效 token: %d device=%q", w.Code, got)
	}
	got = ""
	w = do(t, h, "GET", "/api/file?t=tok1", "192.168.1.9:5000", "")
	if w.Code != 200 || got != "dev1" {
		t.Fatalf("?t= token: %d device=%q", w.Code, got)
	}
}

func TestLocalOnly(t *testing.T) {
	h := localOnly(func(w http.ResponseWriter, r *http.Request) {})
	for remote, want := range map[string]int{
		"127.0.0.1:5000":   200,
		"[::1]:5000":       200,
		"192.168.1.9:5000": 403,
		"[fe80::1]:5000":   403,
		"10.0.0.1:5000":    403,
	} {
		r := httptest.NewRequest("GET", "/api/devices", nil)
		r.RemoteAddr = remote
		r.Host = "127.0.0.1:8686"
		r.Header.Set("X-Forwarded-For", "127.0.0.1") // 不应被信任
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("%s: %d, want %d", remote, w.Code, want)
		}
		if want == 403 && errCode(t, w) != CodeForbidden {
			t.Errorf("%s: 错误码 %s", remote, errCode(t, w))
		}
	}
}

// DNS 重绑定：远端为回环但 Host 非本机名时拒绝。
func TestLocalOnlyHost(t *testing.T) {
	h := localOnly(func(w http.ResponseWriter, r *http.Request) {})
	for host, want := range map[string]int{
		"127.0.0.1":         200,
		"127.0.0.1:8686":    200,
		"localhost":         200,
		"LocalHost:8686":    200,
		"[::1]":             200,
		"[::1]:8686":        200,
		"evil.example:8686": 403,
		"evil.example":      403,
		"192.168.1.5:8686":  403,
		"localhost.evil":    403,
		"":                  403,
	} {
		r := httptest.NewRequest("GET", "/api/devices", nil)
		r.RemoteAddr = "127.0.0.1:5000"
		r.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("Host %q: %d, want %d", host, w.Code, want)
		}
	}
}

func TestRoutes(t *testing.T) {
	e := newAuthEnv(t)
	tok := e.pairOK("dev1")
	cases := []struct {
		method, url, remote, token string
		want                       int
	}{
		{"GET", "/api/ping", "192.168.1.9:1", "", 200},
		{"POST", "/api/pair", "192.168.1.9:1", "", 400}, // 空请求体
		{"GET", "/api/devices", "127.0.0.1:1", "", 200},
		{"GET", "/api/devices", "192.168.1.9:1", tok, 403},
		{"DELETE", "/api/devices/abc", "[::1]:1", "", 404},
		{"DELETE", "/api/devices/dev1", "192.168.1.9:1", tok, 403},
		{"GET", "/api/shares", "192.168.1.9:1", "", 401},
		{"GET", "/api/shares", "192.168.1.9:1", tok, 200},
		{"GET", "/api/list", "192.168.1.9:1", tok, 404},
		{"GET", "/api/file", "192.168.1.9:1", tok, 404},
		{"GET", "/api/download", "192.168.1.9:1", tok, 404},

		// P2 占位（暂返回 501）：设备接口先鉴权，管理接口先过 localOnly / CSRF
		{"GET", "/api/next", "192.168.1.9:1", "", 401},
		{"GET", "/api/next", "192.168.1.9:1", tok, 404},
		{"GET", "/api/subtitles", "192.168.1.9:1", tok, 404},
		{"POST", "/api/progress", "192.168.1.9:1", tok, 501},
		{"GET", "/api/continue", "192.168.1.9:1", tok, 501},
		{"GET", "/api/thumb", "192.168.1.9:1", tok, 501},
		{"POST", "/api/vault/unlock", "192.168.1.9:1", "", 401},
		{"POST", "/api/vault/unlock", "192.168.1.9:1", tok, 501},
		{"POST", "/api/vault/lock", "192.168.1.9:1", tok, 501},
		{"GET", "/api/admin/shares/s1/organize", "192.168.1.9:1", tok, 403},
		{"PUT", "/api/admin/shares/s1/vault", "192.168.1.9:1", tok, 403}, // 本机 + CSRF 见 TestAdminP2Routes
	}
	for _, c := range cases {
		w := e.req(c.method, c.url, c.remote, c.token, nil)
		if w.Code != c.want {
			t.Errorf("%s %s from %s: %d, want %d", c.method, c.url, c.remote, w.Code, c.want)
		}
		if w.Header().Get("Content-Type") != "application/json; charset=utf-8" {
			t.Errorf("%s %s: Content-Type %q", c.method, c.url, w.Header().Get("Content-Type"))
		}
	}
}

func TestHTTPServerTimeouts(t *testing.T) {
	s := NewHTTPServer(":0", http.NotFoundHandler())
	if s.WriteTimeout != 0 {
		t.Fatal("不应设置全局 WriteTimeout（见 05 文档 3.3）")
	}
	if s.ReadHeaderTimeout == 0 {
		t.Fatal("应设置 ReadHeaderTimeout")
	}
	if s.ReadTimeout != 30*time.Second {
		t.Fatalf("ReadTimeout 应为 30s（只限读取请求）: %v", s.ReadTimeout)
	}
}

// startTestHTTP 用 NewHTTPServer 的配置启动测试服务器，ReadTimeout 缩短为 200ms。
func startTestHTTP(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	ts.Config = NewHTTPServer("", h)
	ts.Config.ReadTimeout = 200 * time.Millisecond
	ts.Start()
	t.Cleanup(ts.Close)
	return ts
}

// 慢速请求体：读期限到后应断开，不能无限占用连接。
func TestSlowBodyTimesOut(t *testing.T) {
	ts := startTestHTTP(t, func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			w.WriteHeader(http.StatusRequestTimeout)
		}
	})
	c, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprint(c, "POST /api/pair HTTP/1.1\r\nHost: x\r\nContent-Length: 100\r\n\r\nab")
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	start := time.Now()
	io.ReadAll(c) // 服务端超时后关闭连接
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("慢速请求体未被读期限切断: %v", d)
	}
}

// 长时间流式响应不得被读期限中断（含请求 context 不被取消）。
func TestLongResponseNotCut(t *testing.T) {
	var ctxErr error
	ts := startTestHTTP(t, func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 8; i++ {
			fmt.Fprintf(w, "chunk%d\n", i)
			w.(http.Flusher).Flush()
			time.Sleep(100 * time.Millisecond) // 共 800ms，远超 200ms 读期限
		}
		ctxErr = r.Context().Err()
	})
	resp, err := http.Get(ts.URL + "/api/file")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || strings.Count(string(body), "chunk") != 8 {
		t.Fatalf("流式响应被中断: %v %q", err, body)
	}
	if ctxErr != nil {
		t.Fatalf("请求 context 被取消: %v", ctxErr)
	}
}
