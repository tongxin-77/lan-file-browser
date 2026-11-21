package config

// Discovery 为局域网自动发现设置（01 文档 2.2）；修改后重启服务端生效。
type Discovery struct {
	Enabled bool `json:"enabled"` // 缺省 true
}
