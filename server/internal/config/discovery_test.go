package config

import (
	"os"
	"path/filepath"
	"testing"
)

// discovery.enabled 缺省为 true，可显式关闭。
func TestDiscoveryDefaults(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil || !c.Discovery.Enabled {
		t.Fatalf("默认值: %+v %v", c.Discovery, err)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, fileName), []byte(`{"discovery":{"enabled":false}}`), 0o600)
	if c, err = Load(dir); err != nil || c.Discovery.Enabled {
		t.Fatalf("关闭发现: %+v %v", c.Discovery, err)
	}
}
