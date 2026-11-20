package download

import "github.com/spf13/pflag"

// Defaults 由根命令注入：下载目录与并发数来自全局参数，
// 避免子命令再定义一份同名 flag。
type Defaults struct {
	DownloadDir    func() string
	MaxConcurrency func() int
}

// options 是 download 子命令的参数。
type options struct {
	// URLs 是待下载的 Telegram 消息链接，按传入顺序保留。
	URLs []string
	// ExpandTextLinks 表示展开消息正文里的文字链接（只展开一层）。
	ExpandTextLinks bool
	// ExportLinks 是导出媒体消息链接的 txt 路径，空表示不导出。
	ExportLinks string
	// DryRun 只解析预览，不下载文件。
	DryRun bool
	// Force 忽略已存在的同名文件，重新下载。
	Force bool
	// Flat 不按会话标题建子目录。
	Flat bool
	// Threads 是单个文件的并行分块数。
	Threads int

	defaults Defaults
}

// bind 把参数注册到命令行。
//
// 用 StringArray 而不是 StringSlice：后者会按逗号切分，
// 而链接的查询串里允许出现逗号，按逗号切会把一条链接拆成两条。
func (o *options) bind(fs *pflag.FlagSet) {
	fs.StringArrayVarP(&o.URLs, "url", "u", nil,
		"Telegram 消息链接，可重复传入，例如 -u https://t.me/somechannel/123")
	fs.BoolVar(&o.ExpandTextLinks, "expand-text-links", false,
		"展开消息正文里的文字链接并查询目标消息（只展开一层，上限 200 条）")
	fs.StringVar(&o.ExportLinks, "export-links", "",
		"把有可下载媒体的消息链接导出到指定 txt 文件（每行一条，覆盖写入）")
	fs.BoolVar(&o.DryRun, "dry-run", false,
		"只解析并预览消息与媒体信息，不下载文件")
	fs.BoolVar(&o.Force, "force", false,
		"忽略已存在的同名文件，重新下载（默认按大小一致跳过）")
	fs.BoolVar(&o.Flat, "flat", false,
		"不按会话标题建子目录，文件直接放在下载根目录")
	fs.IntVar(&o.Threads, "threads", 8,
		"单个文件的并行分块数（1-8）")
}
