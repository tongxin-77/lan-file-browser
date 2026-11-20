// Package auth 实现设备配对、token 存储与请求鉴权（01 文档第 3 节）。
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

const devicesFile = "devices.json"

// lastSeenInterval 内重复访问不更新 lastSeen，避免频繁落盘。
const lastSeenInterval = time.Minute

type Device struct {
	DeviceID   string    `json:"deviceId"`
	DeviceName string    `json:"deviceName"`
	TokenHash  string    `json:"tokenHash"` // "sha256:<hex>"
	CreatedAt  time.Time `json:"createdAt"`
	LastSeen   time.Time `json:"lastSeen"`
}

// Store 是已配对设备表，持久化在 dir/devices.json，只存 token 哈希。
type Store struct {
	mu      sync.Mutex
	dir     string
	now     func() time.Time
	devices []Device
}

func OpenStore(dir string, now func() time.Time) (*Store, error) {
	s := &Store{dir: dir, now: now, devices: []Device{}}
	data, err := os.ReadFile(filepath.Join(dir, devicesFile))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(data, &s.devices); err != nil {
			return nil, fmt.Errorf("解析 %s: %w", devicesFile, err)
		}
	}
	return s, nil
}

// Add 为设备生成新 token（同一 deviceId 覆盖旧记录），返回唯一一次的明文。
func (s *Store) Add(deviceID, deviceName string) (string, error) {
	b := make([]byte, 32)
	rand.Read(b)
	tok := base64.RawURLEncoding.EncodeToString(b)
	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.devices = slices.DeleteFunc(s.devices, func(d Device) bool { return d.DeviceID == deviceID })
	s.devices = append(s.devices, Device{
		DeviceID: deviceID, DeviceName: deviceName, TokenHash: hashToken(tok),
		CreatedAt: now, LastSeen: now,
	})
	if err := s.save(); err != nil {
		return "", err
	}
	return tok, nil
}

// Lookup 用常量时间比较查找 token 对应的设备，并节流更新 lastSeen。
func (s *Store) Lookup(tok string) (string, bool) {
	if tok == "" {
		return "", false
	}
	h := []byte(hashToken(tok))

	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i := range s.devices {
		if subtle.ConstantTimeCompare(h, []byte(s.devices[i].TokenHash)) == 1 {
			idx = i
		}
	}
	if idx < 0 {
		return "", false
	}
	d := &s.devices[idx]
	if now := s.now(); now.Sub(d.LastSeen) >= lastSeenInterval {
		d.LastSeen = now
		if err := s.save(); err != nil {
			log.Printf("保存 %s 失败: %v", devicesFile, err)
		}
	}
	return d.DeviceID, true
}

func (s *Store) List() []Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.devices)
}

// Revoke 删除设备，其 token 立即失效。设备不存在返回 false。
func (s *Store) Revoke(deviceID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.devices)
	s.devices = slices.DeleteFunc(s.devices, func(d Device) bool { return d.DeviceID == deviceID })
	if len(s.devices) == n {
		return false, nil
	}
	return true, s.save()
}

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// save 原子写入（临时文件 + rename），调用方需持有锁。
func (s *Store) save() error {
	data, err := json.MarshalIndent(s.devices, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, devicesFile+".tmp-*")
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
	return os.Rename(tmp.Name(), filepath.Join(s.dir, devicesFile))
}
