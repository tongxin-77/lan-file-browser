// Package config 负责服务端 JSON 配置的读写。
package config

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	DefaultPort = 8686
	fileName    = "config.json"
)

type Perms struct {
	Download bool `json:"download"`
	Upload   bool `json:"upload"`
	Delete   bool `json:"delete"` // 删除与重命名（03 文档 API 中称 modify）
	Hidden   bool `json:"hidden"` // 显示隐藏文件（03 文档 API 中称 showHidden）
}

type Share struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Perms    Perms     `json:"perms"`
	Devices  []string  `json:"devices"`            // 空 = 全部手机可见
	Vault    *Vault    `json:"vault,omitempty"`    // 加密片库（04），nil = 普通共享
	Organize *Organize `json:"organize,omitempty"` // 自动整理（06），nil = 未配置
}

type Config struct {
	ServerID         string           `json:"serverId"`
	Name             string           `json:"name"`
	Port             int              `json:"port"`
	Shares           []Share          `json:"shares"`
	OrganizeDefaults OrganizeDefaults `json:"organizeDefaults"` // 自动整理全局默认值（06）
	Update           UpdateSettings   `json:"update"`           // 检查更新（02 §5）
	Discovery        Discovery        `json:"discovery"`        // 局域网自动发现（01 §2.2）

	dir string
}

// DefaultDir 返回 %APPDATA%\LanFileBrowser。
func DefaultDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "LanFileBrowser"), nil
}

// Load 读取 dir 下的配置；不存在时生成默认配置并保存。缺失的字段补默认值。
func Load(dir string) (*Config, error) {
	c := &Config{dir: dir, OrganizeDefaults: DefaultOrganizeDefaults(), Update: UpdateSettings{Enabled: true},
		Discovery: Discovery{Enabled: true}} // JSON 中缺失的键保留默认值
	data, err := os.ReadFile(filepath.Join(dir, fileName))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(data, c); err != nil {
			return nil, fmt.Errorf("解析 %s: %w", fileName, err)
		}
	}

	changed := err != nil
	if c.ServerID == "" {
		c.ServerID = newUUID()
		changed = true
	}
	if c.Name == "" {
		c.Name, _ = os.Hostname()
		changed = true
	}
	if c.Port == 0 {
		c.Port = DefaultPort
		changed = true
	}
	if c.Shares == nil {
		c.Shares = []Share{}
	}
	if changed {
		if err := c.Save(); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Save 原子写入：先写临时文件，再 rename 覆盖。
func (c *Config) Save() error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(c.dir, fileName+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // rename 成功后为空操作
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return renameRetry(tmp.Name(), filepath.Join(c.dir, fileName))
}

// renameRetry 在 Windows 上目标文件刚被替换、仍被杀毒/索引等短暂占用时 rename 会报拒绝访问，
// 短暂重试（最多约 1 秒）。
func renameRetry(from, to string) error {
	for i := 0; ; i++ {
		err := os.Rename(from, to)
		if err == nil || i == 20 || !errors.Is(err, fs.ErrPermission) {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Share 按 ID 查找共享，不存在返回 nil。
func (c *Config) Share(id string) *Share {
	for i := range c.Shares {
		if c.Shares[i].ID == id {
			return &c.Shares[i]
		}
	}
	return nil
}

// NewShareID 生成 8 位小写 base32 随机 ID。
func NewShareID() string {
	b := make([]byte, 5)
	rand.Read(b)
	return strings.ToLower(base32.StdEncoding.EncodeToString(b))
}

func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
