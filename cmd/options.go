package cmd

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/pflag"

	"github.com/ma6254/tg_down/cmd/serve"
)

// Options 是服务启动参数。
//
// 当前只由命令行提供；后续接入配置文件与环境变量时，
// 这里会变成"命令行覆盖后的最终值"。
type Options struct {
	Listen         string // HTTP 监听地址
	DownloadDir    string // 下载根目录
	MaxConcurrency int    // 并发下载数
	AuthToken      string // 非回环监听时的 Bearer token
	BotAPIBaseURL  string // 自建 bot-api-server 地址，空表示官方
	LogLevel       string // debug / info / warn / error
}

// newOptions 返回内置默认值，默认只监听回环地址。
func newOptions() *Options {
	return &Options{
		Listen:         "127.0.0.1:22429",
		DownloadDir:    "./downloads",
		MaxConcurrency: 1,
		LogLevel:       "info",
	}
}

// bind 注册全局参数：根命令与所有子命令共用。
//
// --listen 不在这里：它只影响 HTTP 监听，属于 serve（见 bindServe）。
// 放进全局参数会让 download 之类的子命令平白多出一个无意义的旗标。
func (o *Options) bind(fs *pflag.FlagSet) {
	fs.StringVar(&o.DownloadDir, "download-dir", o.DownloadDir, "下载根目录")
	fs.IntVar(&o.MaxConcurrency, "max-concurrency", o.MaxConcurrency, "并发下载数，范围 1-8")
	fs.StringVar(&o.AuthToken, "auth-token", o.AuthToken, "API 鉴权 token；监听非回环地址时必填")
	fs.StringVar(&o.BotAPIBaseURL, "botapi-base-url", o.BotAPIBaseURL, "自建 Bot API 服务地址，留空使用官方地址")
	fs.StringVar(&o.LogLevel, "log-level", o.LogLevel, "日志级别：debug / info / warn / error")
}

// bindServe 注册 serve 专用参数。
func (o *Options) bindServe(fs *pflag.FlagSet) {
	fs.StringVar(&o.Listen, "listen", o.Listen, "HTTP 监听地址，例如 127.0.0.1:22429")
}

// validate 校验参数并归一化取值。
func (o *Options) validate() error {
	o.Listen = strings.TrimSpace(o.Listen)
	o.DownloadDir = strings.TrimSpace(o.DownloadDir)
	o.AuthToken = strings.TrimSpace(o.AuthToken)
	o.BotAPIBaseURL = strings.TrimSpace(o.BotAPIBaseURL)
	o.LogLevel = strings.ToLower(strings.TrimSpace(o.LogLevel))

	if err := o.validateListen(); err != nil {
		return err
	}
	if o.DownloadDir == "" {
		return errors.New("--download-dir 不能为空")
	}
	if info, err := os.Stat(o.DownloadDir); err == nil && !info.IsDir() {
		return fmt.Errorf("--download-dir %q 已存在且不是目录", o.DownloadDir)
	}
	if o.MaxConcurrency < 1 || o.MaxConcurrency > 8 {
		return fmt.Errorf("--max-concurrency 必须在 1-8 之间，当前为 %d", o.MaxConcurrency)
	}
	switch o.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("--log-level 只能是 debug、info、warn 或 error，当前为 %q", o.LogLevel)
	}
	return nil
}

// validateListen 校验监听地址，并在非回环监听时强制要求 token。
func (o *Options) validateListen() error {
	host, port, err := net.SplitHostPort(o.Listen)
	if err != nil {
		return fmt.Errorf("--listen %q 不是合法的 host:port: %w", o.Listen, err)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("--listen %q 的端口无效", o.Listen)
	}
	if isLoopbackHost(host) {
		return nil
	}
	if o.AuthToken == "" {
		return fmt.Errorf("监听非回环地址 %q 必须设置 --auth-token，否则任何人都能操作你的 Telegram 账号", o.Listen)
	}
	return nil
}

// isLoopbackHost 判断监听主机是否只对本机开放。
//
// 空 host、0.0.0.0、:: 都表示监听全部网卡，不算回环；
// 只有回环 IP 与 localhost 才算。
func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		return false
	case "localhost":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	// 其他主机名无法在本机静态判断，一律按"对外"处理，要求提供 token。
	return false
}

// authMode 返回鉴权状态的可读描述，供启动横幅使用。
func (o *Options) authMode() string {
	if o.AuthToken != "" {
		return "已启用 token"
	}
	return "未启用（仅回环）"
}

// serveOptions 汇总 serve 启动需要的参数快照。
//
// 取值要在 validate 之后才准确，所以 serve 在每次启动时重新取一份，
// 而不是在构造命令时把值拷进去。
func (o *Options) serveOptions() serve.Options {
	return serve.Options{
		Listen:         o.Listen,
		AuthToken:      o.AuthToken,
		DownloadDir:    o.DownloadDir,
		MaxConcurrency: o.MaxConcurrency,
		LogLevel:       o.LogLevel,
		AuthMode:       o.authMode(),
	}
}
