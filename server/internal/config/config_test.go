package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadGeneratesAndKeepsServerID(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")

	c1, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(c1.ServerID) != 36 || c1.ServerID[14] != '4' {
		t.Fatalf("serverId 不是 UUID v4: %q", c1.ServerID)
	}
	if c1.Port != DefaultPort || c1.Name == "" {
		t.Fatalf("默认值错误: %+v", c1)
	}
	if _, err := os.Stat(filepath.Join(dir, fileName)); err != nil {
		t.Fatalf("首次加载应写入配置文件: %v", err)
	}

	c2, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c2.ServerID != c1.ServerID {
		t.Fatalf("再次加载 serverId 变化: %s → %s", c1.ServerID, c2.ServerID)
	}
}

func TestSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := NewShareID()
	c.Shares = append(c.Shares, Share{ID: id, Name: "动漫", Path: `D:\Anime`, Perms: Perms{Download: true}, Devices: []string{}})
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	c2, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := c2.Share(id)
	if s == nil || s.Name != "动漫" || !s.Perms.Download || s.Perms.Upload {
		t.Fatalf("共享未正确保存: %+v", c2.Shares)
	}
	if c2.Share("nope") != nil {
		t.Fatal("不存在的共享应返回 nil")
	}
	if len(id) != 8 {
		t.Fatalf("shareId 长度应为 8: %q", id)
	}
	// 原子写不应留下临时文件
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("数据目录应只有配置文件, 实际 %d 项", len(entries))
	}
}

// 旧配置（vault 为 null 占位、无 organize 与全局默认值）可加载；全局 organize 默认值补齐。
func TestLoadOldConfigCompat(t *testing.T) {
	dir := t.TempDir()
	old := `{"serverId":"11111111-2222-4333-8444-555555555555","name":"pc","port":8686,"shares":[
		{"id":"s1","name":"动漫","path":"D:/Anime","perms":{"download":true,"upload":false,"delete":false,"hidden":false},"devices":[],"vault":null},
		{"id":"s2","name":"电影","path":"D:/Movies","perms":{"download":false,"upload":false,"delete":false,"hidden":false},"devices":["d1"]}]}`
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Shares) != 2 || c.Share("s1").Vault != nil || c.Share("s2").Vault != nil || c.Share("s1").Organize != nil {
		t.Fatalf("旧配置解析错误: %+v", c.Shares)
	}
	if c.OrganizeDefaults != DefaultOrganizeDefaults() {
		t.Fatalf("全局整理默认值: %+v", c.OrganizeDefaults)
	}
}

// vault / organize / 全局默认值往返保存。
func TestVaultOrganizeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	rate, retries, onPlay := 30, 5, false
	c.Shares = []Share{{ID: "s1", Name: "影视库", Path: `E:\Library`,
		Vault: &Vault{Enabled: true, PasswordHash: "aGFzaA==", Salt: "c2FsdA==",
			Params: VaultParams{Time: 3, MemoryKiB: 65536, Threads: 2, KeyLen: 32}},
		Organize: &Organize{Enabled: true, Paused: true, RateLimit: &rate, MaxRetries: &retries, PauseOnPlay: &onPlay,
			Sources: []OrganizeSource{
				{ID: "s1a2", Path: `D:\Downloads\BT`, Mode: "copy"},
				{ID: "s3b4", Path: `D:\Downloads\Browser`, Mode: "move", AfterMove: "recycle"},
			}},
	}}
	c.OrganizeDefaults.Interval = 15
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, fileName))
	for _, k := range []string{`"passwordHash"`, `"memoryKiB"`, `"keyLen"`, `"afterMove"`, `"pauseOnPlay"`, `"rateLimit"`, `"organizeDefaults"`} {
		if !strings.Contains(string(data), k) {
			t.Errorf("JSON 缺少字段 %s", k)
		}
	}
	c2, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	sh := c2.Share("s1")
	if !reflect.DeepEqual(sh, &c.Shares[0]) {
		t.Fatalf("往返不一致:\n%+v\n%+v", sh, c.Shares[0])
	}
	if o := sh.Organize; o.Interval != nil || o.MinFree != nil || *o.RateLimit != 30 {
		t.Fatalf("null 数值字段应保持 nil（取全局默认）: %+v", o)
	}
	if c2.OrganizeDefaults.Interval != 15 || c2.OrganizeDefaults.RateLimit != 50 {
		t.Fatalf("全局默认值: %+v", c2.OrganizeDefaults)
	}
}
