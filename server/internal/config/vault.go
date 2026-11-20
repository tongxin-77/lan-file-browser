package config

// Vault 为共享的加密片库设置（04 文档第 3 节）。nil = 普通共享；关闭即删除该字段。
type Vault struct {
	Enabled      bool        `json:"enabled"`
	PasswordHash string      `json:"passwordHash"` // argon2id 哈希，base64
	Salt         string      `json:"salt"`         // 16 字节随机，base64
	Params       VaultParams `json:"params"`       // 随哈希保存，便于以后调整参数
}

// VaultParams 为 argon2id 参数。
type VaultParams struct {
	Time      uint32 `json:"time"`
	MemoryKiB uint32 `json:"memoryKiB"`
	Threads   uint8  `json:"threads"`
	KeyLen    uint32 `json:"keyLen"`
}
