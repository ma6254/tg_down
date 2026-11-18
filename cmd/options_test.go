package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// TestOptionsValidate 覆盖参数校验的主要分支。
func TestOptionsValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Options)
		wantErr string // 空表示期望通过
	}{
		{"默认值", func(*Options) {}, ""},
		{"回环带 token", func(o *Options) { o.AuthToken = "s3cret" }, ""},

		{
			"非回环无 token",
			func(o *Options) { o.Listen = "0.0.0.0:22429" },
			"auth-token",
		},
		{
			"仅端口无主机",
			func(o *Options) { o.Listen = ":22429" },
			"auth-token",
		},
		{
			"非回环带 token",
			func(o *Options) {
				o.Listen = "0.0.0.0:22429"
				o.AuthToken = "s3cret"
			},
			"",
		},
		{
			"缺少端口",
			func(o *Options) { o.Listen = "127.0.0.1" },
			"host:port",
		},
		{
			"端口非数字",
			func(o *Options) { o.Listen = "127.0.0.1:abc" },
			"端口",
		},
		{
			"端口越界",
			func(o *Options) { o.Listen = "127.0.0.1:70000" },
			"端口",
		},
		{
			"并发为零",
			func(o *Options) { o.MaxConcurrency = 0 },
			"max-concurrency",
		},
		{
			"并发过大",
			func(o *Options) { o.MaxConcurrency = 9 },
			"max-concurrency",
		},
		{
			"下载目录为空",
			func(o *Options) { o.DownloadDir = "  " },
			"download-dir",
		},
		{
			"日志级别非法",
			func(o *Options) { o.LogLevel = "verbose" },
			"log-level",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := newOptions()
			tt.mutate(o)

			err := o.validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("期望通过，实际报错: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("期望报错包含 %q，实际通过", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("错误信息 %q 未包含 %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestOptionsBind 确认命令行参数能正确落到结构体，并且覆盖默认值后仍能通过校验。
func TestOptionsBind(t *testing.T) {
	o := newOptions()
	fs := pflag.NewFlagSet("tg_down", pflag.ContinueOnError)
	o.bind(fs)

	err := fs.Parse([]string{
		"--listen", "0.0.0.0:39001",
		"--auth-token", "s3cret",
		"--download-dir", "./tmp-downloads",
		"--max-concurrency", "5",
		"--botapi-base-url", "http://127.0.0.1:8081",
		"--log-level", "debug",
	})
	if err != nil {
		t.Fatalf("解析参数失败: %v", err)
	}

	if o.Listen != "0.0.0.0:39001" {
		t.Errorf("Listen = %q", o.Listen)
	}
	if o.AuthToken != "s3cret" {
		t.Errorf("AuthToken = %q", o.AuthToken)
	}
	if o.DownloadDir != "./tmp-downloads" {
		t.Errorf("DownloadDir = %q", o.DownloadDir)
	}
	if o.MaxConcurrency != 5 {
		t.Errorf("MaxConcurrency = %d", o.MaxConcurrency)
	}
	if o.BotAPIBaseURL != "http://127.0.0.1:8081" {
		t.Errorf("BotAPIBaseURL = %q", o.BotAPIBaseURL)
	}
	if o.LogLevel != "debug" {
		t.Errorf("LogLevel = %q", o.LogLevel)
	}
	if err := o.validate(); err != nil {
		t.Fatalf("校验失败: %v", err)
	}
}

// TestIsLoopbackHost 覆盖回环判断的边界情况。
func TestIsLoopbackHost(t *testing.T) {
	cases := map[string]bool{
		"":            false,
		"0.0.0.0":     false,
		"::":          false,
		"127.0.0.1":   true,
		"::1":         true,
		"localhost":   true,
		"192.168.1.5": false,
		"example.com": false,
	}
	for host, want := range cases {
		if got := isLoopbackHost(host); got != want {
			t.Errorf("isLoopbackHost(%q) = %v，期望 %v", host, got, want)
		}
	}
}
