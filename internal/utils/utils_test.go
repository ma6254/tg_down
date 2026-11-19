package utils

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// TestResolveProxy 确认代理只从配置表读取，未配置时返回空。
func TestResolveProxy(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())

	got, err := ResolveProxy()
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got != "" {
		t.Errorf("未配置时应为空，实际 %q", got)
	}

	db, err := OpenDatabase()
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	if err := config.NewStore(db).Set(config.KeyHTTPProxy, "http://127.0.0.1:7897"); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	db.Close()

	got, err = ResolveProxy()
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got != "http://127.0.0.1:7897" {
		t.Errorf("期望读取配置表代理，实际 %q", got)
	}

	// http_proxy_enabled=false 时即使配置了代理也应直连。
	db, err = OpenDatabase()
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	if err := config.NewStore(db).Set(config.KeyHTTPProxyEnabled, "false"); err != nil {
		t.Fatalf("关闭代理失败: %v", err)
	}
	db.Close()

	got, err = ResolveProxy()
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got != "" {
		t.Errorf("关闭代理后应直连，实际 %q", got)
	}

	db, err = OpenDatabase()
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	if err := config.NewStore(db).Set(config.KeyHTTPProxyEnabled, "true"); err != nil {
		t.Fatalf("启用代理失败: %v", err)
	}
	db.Close()

	got, err = ResolveProxy()
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got != "http://127.0.0.1:7897" {
		t.Errorf("重新启用后应使用代理，实际 %q", got)
	}
}

// TestOpenDatabaseConcurrent 覆盖空库首次并发打开/建表。
//
// 服务端每个请求都会 OpenDatabase；首次并发请求若同时 AutoMigrate，
// 曾出现 "index ... already exists" 导致 500。
func TestOpenDatabaseConcurrent(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())

	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			db, err := OpenDatabase()
			if err == nil {
				err = db.Close()
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("并发打开数据库失败: %v", err)
		}
	}
}

// TestIsNotFound 确认 gorm 错误判断。
func TestIsNotFound(t *testing.T) {
	if !IsNotFound(ErrRecordNotFound) {
		t.Error("应识别为未找到")
	}
	if IsNotFound(errors.New("other")) {
		t.Error("普通错误不应识别为未找到")
	}
}

// TestMaskPhone 覆盖带/不带加号与短号码。
func TestMaskPhone(t *testing.T) {
	cases := map[string]string{
		"+8613800000000": "+861****000",
		"8613800000000":  "+861****000",
		"+861":           "+861",
		"":               "",
	}
	for in, want := range cases {
		if got := MaskPhone(in); got != want {
			t.Errorf("MaskPhone(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestDigitsOnly 确认只保留数字。
func TestDigitsOnly(t *testing.T) {
	if got := DigitsOnly("+86 138-0000-0000"); got != "8613800000000" {
		t.Errorf("DigitsOnly = %q", got)
	}
	if got := DigitsOnly("abc"); got != "" {
		t.Errorf("DigitsOnly(abc) = %q", got)
	}
}

// TestFormatTime 覆盖零值与非零值。
func TestFormatTime(t *testing.T) {
	if got := FormatTime(time.Time{}); got != "未知" {
		t.Errorf("零值 = %q", got)
	}
	at := time.Date(2026, 11, 19, 0, 10, 0, 0, time.UTC)
	if got := FormatTime(at); got != "2026-11-19 00:10" {
		t.Errorf("FormatTime = %q", got)
	}
}

// TestDescribeDevice 确认设备描述包含关键字段。
func TestDescribeDevice(t *testing.T) {
	got := DescribeDevice(mtproto.Device{
		AppName: "tg_down", Platform: "Windows", Country: "CN",
		ActiveAt: time.Date(2026, 11, 19, 1, 2, 0, 0, time.UTC),
	})
	for _, want := range []string{"tg_down", "Windows", "CN", "2026-11-19 01:02"} {
		if !strings.Contains(got, want) {
			t.Errorf("描述 %q 缺少 %q", got, want)
		}
	}
	if got := DescribeDevice(mtproto.Device{}); !strings.Contains(got, "未知客户端") {
		t.Errorf("空设备描述 = %q", got)
	}
}

// TestAccountAttrs 确认属性拼装。
func TestAccountAttrs(t *testing.T) {
	if got := AccountAttrs(&mtproto.Account{}); got != "" {
		t.Errorf("无属性时 = %q", got)
	}
	if got := AccountAttrs(&mtproto.Account{Premium: true}); got != "Premium" {
		t.Errorf("Premium 属性 = %q", got)
	}
	if got := AccountAttrs(&mtproto.Account{Premium: true, Bot: true}); got != "Premium / Bot" {
		t.Errorf("组合属性 = %q", got)
	}
}

// TestOrDash 确认空值占位。
func TestOrDash(t *testing.T) {
	if got := OrDash(""); got != "-" {
		t.Errorf("OrDash(\"\") = %q", got)
	}
	if got := OrDash("x"); got != "x" {
		t.Errorf("OrDash(x) = %q", got)
	}
}
