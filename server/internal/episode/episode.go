// Package episode 从文件名解析剧名、季、集号，供“下一集”（03 文档 4.6）与自动整理（06 文档 5.2）共用。
// 排除 1080p、x265、年份等干扰数字的规则只在此维护一份。
package episode

// Info 为解析结果。
type Info struct {
	Show    string // 规范化后的剧名；文件名中没有剧名（如 `12.mkv`、`第12集.mp4`）时为空，由调用方取父文件夹名
	Season  int    // 季号；文件名中未出现时为 0（调用方按默认 1 处理）
	Episode int    // 集号
}

// Parse 解析文件名（不含目录，可含扩展名）。未识别出集号时 ok 为 false。
// 模式与优先级见 03 文档 4.6、06 文档 5.2。
//
// TODO: 待“下一集”/整理库同事实现。
func Parse(name string) (info Info, ok bool) {
	return Info{}, false
}
