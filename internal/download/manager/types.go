// Package manager 是下载任务队列：负责入队、并发执行、暂停/恢复与进度落库。
//
// 它依赖 internal/download（路径规划）与 internal/tgclient/mtproto（传输），
// 因此放在 download 的子包里，避免 mtproto → download 的导入形成环。
// 本包不认识 HTTP：任务快照与事件都是领域结构，由 cmd 层映射成接口 DTO。
package manager

import (
	"errors"

	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// 事件类型，与 cmd 层的 SSE 事件名一一对应。
const (
	EventTaskUpdate   = "task.update"
	EventTaskProgress = "task.progress"
	EventTaskDeleted  = "task.deleted"
)

// 任务相关的哨兵错误，cmd 层据此映射成 CLI 文案或 HTTP 状态码。
var (
	// ErrNotFound 表示任务不存在。
	ErrNotFound = errors.New("下载任务不存在")
	// ErrConflict 表示任务当前状态不允许该操作。
	ErrConflict = errors.New("下载任务当前状态不允许该操作")
	// ErrThumbnailNotFound 表示任务没有可用的缩略图。
	ErrThumbnailNotFound = errors.New("缩略图不存在")
	// ErrNoAccount 表示没有可用的当前账号。
	ErrNoAccount = errors.New("没有可用的当前账号")
	// ErrNoSession 表示当前账号的 session 文件不可用。
	ErrNoSession = errors.New("账号的 session 文件不可用")
	// ErrPreviewFailed 表示链接解析失败（网络或 Telegram 侧报错）。
	ErrPreviewFailed = errors.New("链接解析失败")
)

// Config 是管理器的启动参数。
type Config struct {
	// RootDir 是下载根目录。
	RootDir string
	// Workers 是同时执行的任务数，取值 1-8。
	Workers int
	// OnEvent 在任务状态变化时被调用；为 nil 时不广播。
	OnEvent func(Event)
	// ThumbnailURL 为一张缩略图登记 token 地址；为 nil 时任务不带封面。
	ThumbnailURL func(session mtproto.Session, thumb mtproto.Thumb) string
}

// Task 是一条任务的可展示状态：数据库记录 + 内存里的实时进度。
type Task struct {
	Row    database.DownloadTask
	Offset int64 // 已下载字节数（running 时来自内存）
	Speed  int64 // B/s，仅 running 时有意义
}

// CreateRequest 是创建任务请求。
type CreateRequest struct {
	Links     []string
	Overwrite bool
}

// Skip 记录因「没有可下载媒体」或「超过单文件上限」跳过的链接。
type Skip struct {
	Link   string
	Reason string
}

// Failure 记录解析失败的链接。
type Failure struct {
	Link  string
	Error string
}

// CreateResult 是一次创建任务的结果。
type CreateResult struct {
	Created  []uint // 新建并已入队的任务
	Existing []uint // 之前已存在的任务
	Skipped  []Skip
	Failed   []Failure
}

// ListQuery 是列表筛选条件；Status 为空表示全部。
type ListQuery struct {
	Status string
	Limit  int
}

// Event 是一条任务事件；Type 决定其余字段的含义。
type Event struct {
	Type string
	// Task 只在 task.update 时有效。
	Task Task
	// ID 是任务 ID（task.progress / task.deleted）。
	ID uint
	// 以下字段只在 task.progress 时有效。
	Offset int64
	Speed  int64
	Size   int64
}
