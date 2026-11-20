package config

import "slices"

// Organize 为共享（片库）的自动整理设置（06 文档第 3 节）。nil = 未配置过。
// 指针数值字段为 nil（JSON null）时取 Config.OrganizeDefaults。
type Organize struct {
	Enabled     bool             `json:"enabled"`
	Sources     []OrganizeSource `json:"sources"`
	Interval    *int             `json:"interval"`    // 扫描间隔（分钟）
	RateLimit   *int             `json:"rateLimit"`   // 复制限速 MB/s，0 = 不限
	MinFree     *int             `json:"minFree"`     // 库盘最小剩余空间 GB
	MaxRetries  *int             `json:"maxRetries"`  // 单文件失败重试上限
	Paused      bool             `json:"paused"`      // 手动暂停（重启后保持）
	PauseOnPlay *bool            `json:"pauseOnPlay"` // 同盘播放时暂停复制；nil = true
}

// OrganizeSource 为源文件夹。
type OrganizeSource struct {
	ID        string `json:"id"`                  // 短随机 ID，状态按它归属
	Path      string `json:"path"`                // 真实路径
	Mode      string `json:"mode"`                // copy / move
	AfterMove string `json:"afterMove,omitempty"` // 仅 move：recycle（默认）/ delete
}

// 源文件夹取值。
const (
	OrganizeCopy      = "copy"
	OrganizeMove      = "move"
	AfterMoveRecycle  = "recycle"
	AfterMoveDelete   = "delete"
	defaultIntervalM  = 30
	defaultRateMBps   = 50
	defaultMaxRetries = 3
)

// OrganizeDefaults 为自动整理的全局默认值（管理页“设置”，06 文档第 3、10 节）。
type OrganizeDefaults struct {
	Interval   int    `json:"interval"`   // 分钟
	RateLimit  int    `json:"rateLimit"`  // MB/s，0 = 不限
	MinFree    *int   `json:"minFree"`    // GB；nil = 取“库盘总容量 10% 与 20 GB 的较小值”
	MaxRetries int    `json:"maxRetries"` // 次
	AfterMove  string `json:"afterMove"`  // 新增移动源时的默认处理
}

// DefaultOrganizeDefaults 返回出厂默认值。
func DefaultOrganizeDefaults() OrganizeDefaults {
	return OrganizeDefaults{Interval: defaultIntervalM, RateLimit: defaultRateMBps, MaxRetries: defaultMaxRetries, AfterMove: AfterMoveRecycle}
}

func (o *Organize) clone() *Organize {
	if o == nil {
		return nil
	}
	n := *o
	n.Sources = slices.Clone(o.Sources)
	n.Interval, n.RateLimit, n.MinFree, n.MaxRetries = clonePtr(o.Interval), clonePtr(o.RateLimit), clonePtr(o.MinFree), clonePtr(o.MaxRetries)
	n.PauseOnPlay = clonePtr(o.PauseOnPlay)
	return &n
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
