package activity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVolumeOf(t *testing.T) {
	dir := t.TempDir()
	want := strings.ToUpper(filepath.VolumeName(dir)) + `\`
	if got := VolumeOf(filepath.Join(dir, "x.mkv")); !strings.EqualFold(got, want) {
		t.Fatalf("VolumeOf: %q, want %q", got, want)
	}
	if got := VolumeOf(os.Getenv("SystemRoot")); !strings.HasSuffix(got, `\`) {
		t.Fatalf("VolumeOf(SystemRoot): %q", got)
	}
}

// 真实调用 SetThreadExecutionState：只验证不阻塞、Close 后 SetAwake 不卡住。
func TestSystemKeeperSmoke(t *testing.T) {
	k := NewSystemKeeper()
	k.SetAwake(true)
	k.SetAwake(false)
	k.Close()
	k.Close()
	k.SetAwake(true)
}
