package download

import (
	"reflect"
	"testing"

	"github.com/spf13/pflag"
)

// TestOptionsBindURLs 覆盖 -u/--url 的可重复解析与顺序保留。
func TestOptionsBindURLs(t *testing.T) {
	o := &options{}
	fs := pflag.NewFlagSet("download", pflag.ContinueOnError)
	o.bind(fs)

	err := fs.Parse([]string{
		"-u", "https://t.me/JPD233/2522?comment=12466",
		"--url", "https://t.me/c/1234567890/42",
	})
	if err != nil {
		t.Fatalf("解析参数失败: %v", err)
	}

	want := []string{
		"https://t.me/JPD233/2522?comment=12466",
		"https://t.me/c/1234567890/42",
	}
	if !reflect.DeepEqual(o.URLs, want) {
		t.Fatalf("URLs = %#v，期望 %#v", o.URLs, want)
	}
}

// TestOptionsURLKeepsComma 确认链接里的逗号不会被当成多个值切开。
func TestOptionsURLKeepsComma(t *testing.T) {
	o := &options{}
	fs := pflag.NewFlagSet("download", pflag.ContinueOnError)
	o.bind(fs)

	const link = "https://t.me/somechannel/123?comment=1,2"
	if err := fs.Parse([]string{"-u", link}); err != nil {
		t.Fatalf("解析参数失败: %v", err)
	}
	if len(o.URLs) != 1 || o.URLs[0] != link {
		t.Fatalf("URLs = %#v，期望仅一条 %q", o.URLs, link)
	}
}
