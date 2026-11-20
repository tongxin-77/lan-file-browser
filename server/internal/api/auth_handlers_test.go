package api

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"lfb/server/internal/auth"
	"lfb/server/internal/config"
)

const phone = "192.168.1.9:5000"

type authEnv struct {
	t   *testing.T
	dir string
	now time.Time
	svc *auth.Service
	h   http.Handler
}

func newAuthEnv(t *testing.T) *authEnv {
	e := &authEnv{t: t, dir: t.TempDir(), now: time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)}
	e.reload()
	return e
}

// reload 重新从数据目录加载（模拟重启）。
func (e *authEnv) reload() {
	cfg, err := config.Load(e.dir)
	if err != nil {
		e.t.Fatal(err)
	}
	e.svc, err = auth.New(e.dir, func() time.Time { return e.now })
	if err != nil {
		e.t.Fatal(err)
	}
	e.h = (&Server{Config: config.NewStore(cfg), Auth: e.svc, Version: "test"}).Handler()
}

func (e *authEnv) req(method, url, remote, token string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	var b bytes.Buffer
	if body != nil {
		json.NewEncoder(&b).Encode(body)
	}
	r := httptest.NewRequest(method, url, &b)
	r.RemoteAddr = remote
	r.Host = "127.0.0.1:8686"        // 本机名，localOnly 另需远端为回环
	r.Header.Set("X-LFB-Admin", "1") // 本机修改类接口的 CSRF 防护要求
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func (e *authEnv) pair(code, deviceID string) *httptest.ResponseRecorder {
	return e.req("POST", "/api/pair", phone, "", map[string]string{"code": code, "deviceName": "Pixel 8", "deviceId": deviceID})
}

func (e *authEnv) pairOK(deviceID string) string {
	e.t.Helper()
	pin, _ := e.svc.Codes.Current()
	w := e.pair(pin, deviceID)
	if w.Code != 200 {
		e.t.Fatalf("pair: %d %s", w.Code, w.Body)
	}
	var resp struct{ Token, ServerID, Name string }
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Token == "" || resp.ServerID == "" {
		e.t.Fatalf("pair 响应缺字段: %s", w.Body)
	}
	return resp.Token
}

func TestPingNeedPair(t *testing.T) {
	e := newAuthEnv(t)
	ping := func(token string) map[string]any {
		w := e.req("GET", "/api/ping", phone, token, nil)
		if w.Code != 200 {
			t.Fatalf("ping: %d %s", w.Code, w.Body)
		}
		var m map[string]any
		json.Unmarshal(w.Body.Bytes(), &m)
		return m
	}
	m := ping("")
	if m["needPair"] != true || m["id"] == "" || m["version"] != "test" {
		t.Fatalf("无 token: %v", m)
	}
	if ping("bad")["needPair"] != true {
		t.Fatal("无效 token 应 needPair=true")
	}
	tok := e.pairOK("dev1")
	if ping(tok)["needPair"] != false {
		t.Fatal("有效 token 应 needPair=false")
	}
}

func TestPairThenAccessProtected(t *testing.T) {
	e := newAuthEnv(t)
	if w := e.req("GET", "/api/shares", phone, "", nil); w.Code != 401 {
		t.Fatalf("未配对: %d", w.Code)
	}
	tok := e.pairOK("dev1")
	if w := e.req("GET", "/api/shares", phone, tok, nil); w.Code == 401 {
		t.Fatal("配对后 Bearer 应可访问")
	}
	if w := e.req("GET", "/api/file?t="+tok, phone, "", nil); w.Code == 401 {
		t.Fatal("?t= 应可访问")
	}
	if w := e.req("GET", "/api/download?t="+tok, phone, "", nil); w.Code == 401 {
		t.Fatal("download ?t= 应可访问")
	}
	for _, u := range []string{"/api/shares?t=", "/api/list?t="} {
		if w := e.req("GET", u+tok, phone, "", nil); w.Code != 401 {
			t.Fatalf("%s 仅认 Authorization 头: %d", u, w.Code)
		}
	}

	// 重载 devices.json 后 token 仍有效
	e.reload()
	if w := e.req("GET", "/api/shares", phone, tok, nil); w.Code == 401 {
		t.Fatal("重载后 token 应仍有效")
	}
}

func TestPairErrors(t *testing.T) {
	e := newAuthEnv(t)
	if w := e.req("POST", "/api/pair", phone, "", map[string]string{"code": "1"}); w.Code != 400 {
		t.Fatalf("缺 deviceId: %d", w.Code)
	}

	// 过期码
	pin, code := e.svc.Codes.Current()
	e.now = e.now.Add(5*time.Minute + time.Second)
	for _, c := range []string{pin, code} {
		if w := e.pair(c, "dev1"); w.Code != 401 || errCode(t, w) != CodeUnauthorized {
			t.Fatalf("过期码: %d %s", w.Code, w.Body)
		}
	}

	// 一次性码用后失效
	_, code = e.svc.Codes.Current()
	if w := e.pair(code, "dev1"); w.Code != 200 {
		t.Fatalf("一次性码: %d %s", w.Code, w.Body)
	}
	if w := e.pair(code, "dev2"); w.Code != 401 {
		t.Fatalf("一次性码重复使用: %d", w.Code)
	}
}

func TestPairLock(t *testing.T) {
	e := newAuthEnv(t)
	for i := 0; i < 5; i++ {
		e.pair("wrong", "dev1")
	}
	pin, _ := e.svc.Codes.Current()
	w := e.pair(pin, "dev1")
	if w.Code != 423 || errCode(t, w) != CodeLocked {
		t.Fatalf("锁定: %d %s", w.Code, w.Body)
	}
	if w := e.req("POST", "/api/pair", "192.168.1.10:6000", "", map[string]string{"code": pin, "deviceId": "dev2"}); w.Code != 200 {
		t.Fatalf("其他 IP 不受锁定影响: %d %s", w.Code, w.Body)
	}
	e.reload() // 重启解除锁定
	pin, _ = e.svc.Codes.Current()
	if w := e.pair(pin, "dev1"); w.Code != 200 {
		t.Fatalf("重启后: %d", w.Code)
	}
}

// 日志注入：设备名中的换行等控制字符不得原样写入日志。
func TestPairLogEscapesControlChars(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	e := newAuthEnv(t)
	pin, _ := e.svc.Codes.Current()
	w := e.req("POST", "/api/pair", phone, "", map[string]string{
		"code": pin, "deviceName": "Pixel\n2026/11/20 10:00:00 伪造日志\r", "deviceId": "dev\x1b[31m1",
	})
	if w.Code != 200 {
		t.Fatalf("pair: %d %s", w.Code, w.Body)
	}
	out := strings.TrimSuffix(buf.String(), "\n")
	if strings.ContainsAny(out, "\n\r\x1b") {
		t.Fatalf("日志含控制字符: %q", buf.String())
	}
}

func TestDevicesListAndRevoke(t *testing.T) {
	e := newAuthEnv(t)
	tok := e.pairOK("dev1")

	w := e.req("GET", "/api/devices", "127.0.0.1:1", "", nil)
	var list []map[string]any
	json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != 200 || len(list) != 1 || list[0]["deviceId"] != "dev1" || list[0]["deviceName"] != "Pixel 8" {
		t.Fatalf("devices: %d %s", w.Code, w.Body)
	}
	if _, leaked := list[0]["tokenHash"]; leaked {
		t.Fatal("不应返回 tokenHash")
	}
	if w := e.req("DELETE", "/api/devices/dev1", phone, "", nil); w.Code != 403 {
		t.Fatalf("非本机吊销: %d", w.Code)
	}
	if w := e.req("DELETE", "/api/devices/dev1", "127.0.0.1:1", "", nil); w.Code != 204 {
		t.Fatalf("吊销: %d %s", w.Code, w.Body)
	}
	if w := e.req("GET", "/api/shares", phone, tok, nil); w.Code != 401 {
		t.Fatalf("吊销后应 401: %d", w.Code)
	}
	if w := e.req("DELETE", "/api/devices/dev1", "127.0.0.1:1", "", nil); w.Code != 404 {
		t.Fatalf("吊销不存在设备: %d", w.Code)
	}
}
