package config

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// 并发 Update 与 Get：不 panic；每次读到的快照内部一致（要么整份旧、要么整份新，不会半新半旧）；
// 最终落盘内容与内存一致。
func TestStoreConcurrentReadWrite(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	c.Shares = []Share{{ID: "s1", Name: "n0", Path: dir, Devices: []string{"n0"}, Perms: Perms{Download: true}}}
	s := NewStore(c)

	const writers, writes, readers = 4, 25, 8
	var wg sync.WaitGroup
	stop := make(chan struct{})
	errc := make(chan error, readers)
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				cfg := s.Get()
				sh := cfg.Share("s1")
				var n int
				fmt.Sscanf(sh.Name, "n%d", &n)
				if len(sh.Devices) != 1 || sh.Devices[0] != sh.Name || sh.Perms.Download != (n%2 == 0) {
					errc <- fmt.Errorf("读到不一致的快照: %+v", *sh)
					return
				}
			}
		}()
	}
	var ww sync.WaitGroup
	for w := 0; w < writers; w++ {
		ww.Add(1)
		go func(w int) {
			defer ww.Done()
			for i := 0; i < writes; i++ {
				n := w*writes + i
				err := s.Update(func(c *Config) error {
					sh := c.Share("s1")
					sh.Name = fmt.Sprintf("n%d", n)
					sh.Devices[0] = sh.Name // 原地改：若未深拷贝，读者会看到被篡改的旧快照
					sh.Perms.Download = n%2 == 0
					return nil
				})
				if err != nil {
					t.Error(err)
				}
			}
		}(w)
	}
	ww.Wait()
	close(stop)
	wg.Wait()
	select {
	case err := <-errc:
		t.Fatal(err)
	default:
	}

	disk, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := disk.Share("s1").Name, s.Get().Share("s1").Name; got != want {
		t.Fatalf("落盘 %q ≠ 内存 %q", got, want)
	}
}

// Update 出错时不替换快照；旧快照不受修改影响。
func TestStoreUpdateIsolation(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c.Shares = []Share{{ID: "s1", Name: "a", Devices: []string{"d1"}}}
	s := NewStore(c)
	old := s.Get()

	boom := errors.New("boom")
	if err := s.Update(func(c *Config) error { c.Shares[0].Name = "x"; return boom }); err != boom {
		t.Fatalf("应返回 fn 的错误: %v", err)
	}
	if s.Get() != old || old.Shares[0].Name != "a" {
		t.Fatal("出错时不应替换快照")
	}
	if err := s.Update(func(c *Config) error {
		c.Shares[0].Devices[0] = "d2"
		c.Shares = append(c.Shares, Share{ID: "s2"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if old.Shares[0].Devices[0] != "d1" || len(old.Shares) != 1 {
		t.Fatalf("旧快照被改动: %+v", old.Shares)
	}
	if len(s.Get().Shares) != 2 {
		t.Fatal("新快照未生效")
	}
}

// 新增的指针/切片字段（vault、organize）同样深拷贝：Update 中原地修改不影响旧快照。
func TestStoreCloneVaultOrganize(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	n := 10
	c.Shares = []Share{{ID: "s1", Vault: &Vault{Salt: "a"},
		Organize: &Organize{Interval: &n, Sources: []OrganizeSource{{ID: "x", Path: "a"}}}}}
	s := NewStore(c)
	old := s.Get()
	if err := s.Update(func(c *Config) error {
		sh := c.Share("s1")
		sh.Vault.Salt = "b"
		sh.Organize.Sources[0].Path = "b"
		*sh.Organize.Interval = 20
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	o := old.Share("s1")
	if o.Vault.Salt != "a" || o.Organize.Sources[0].Path != "a" || *o.Organize.Interval != 10 {
		t.Fatalf("旧快照被改动: %+v %+v", o.Vault, o.Organize)
	}
}
