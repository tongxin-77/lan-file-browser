package config

import (
	"os"
	"path/filepath"
	"testing"
)

// update.enabled 缺省为 true；只写 mirror 时 enabled 仍为 true。
func TestUpdateSettingsDefaults(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !c.Update.Enabled || c.Update.Mirror != "" {
		t.Fatalf("默认值: %+v", c.Update)
	}

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, fileName), []byte(`{"update":{"mirror":"https://m.example/gh/"}}`), 0o600)
	c, err = Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Update.Enabled || c.Update.Mirror != "https://m.example/gh/" {
		t.Fatalf("只配 mirror: %+v", c.Update)
	}

	os.WriteFile(filepath.Join(dir, fileName), []byte(`{"update":{"enabled":false}}`), 0o600)
	if c, err = Load(dir); err != nil || c.Update.Enabled {
		t.Fatalf("关闭更新: %+v %v", c.Update, err)
	}
}
