package config

// UpdateSettings 为检查更新设置（02 文档 5.4）。
type UpdateSettings struct {
	Enabled bool   `json:"enabled"` // 缺省 true
	Mirror  string `json:"mirror"`  // 下载地址前缀：替换 Release 资产地址中的 https://github.com/，为空时直连
}
