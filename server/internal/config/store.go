package config

import (
	"slices"
	"sync"
	"sync/atomic"
)

// Store 持有不可变的配置快照，供并发读取：文件接口每个请求 Get 一次，拿到的 *Config 只读、不得修改。
// 修改一律经 Update：复制当前快照 → 修改副本 → 原子保存 config.json → 替换快照。
type Store struct {
	mu  sync.Mutex // 串行化 Update
	cur atomic.Pointer[Config]
}

func NewStore(c *Config) *Store {
	s := &Store{}
	s.cur.Store(c)
	return s
}

// Get 返回当前快照（只读）。
func (s *Store) Get() *Config { return s.cur.Load() }

// Update 在当前快照的深拷贝上执行 fn；fn 返回错误或保存失败时快照不变。
func (s *Store) Update(fn func(c *Config) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cur.Load().clone()
	if err := fn(c); err != nil {
		return err
	}
	if err := c.Save(); err != nil {
		return err
	}
	s.cur.Store(c)
	return nil
}

func (c *Config) clone() *Config {
	n := *c
	n.OrganizeDefaults.MinFree = clonePtr(c.OrganizeDefaults.MinFree)
	n.Shares = make([]Share, len(c.Shares))
	for i, sh := range c.Shares {
		sh.Devices = slices.Clone(sh.Devices)
		sh.Vault = clonePtr(sh.Vault)
		sh.Organize = sh.Organize.clone()
		n.Shares[i] = sh
	}
	return &n
}
