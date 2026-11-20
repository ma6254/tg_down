package thumbcache

import (
	"path/filepath"
	"testing"

	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// TestKeyStable 确认键只由媒体身份决定，与 token/尺寸共用同一份缓存。
func TestKeyStable(t *testing.T) {
	thumb := mtproto.Thumb{Kind: "photo", FileID: 42, SizeType: "m"}
	key := Key(thumb)
	if key == "" || key != Key(mtproto.Thumb{Kind: "photo", FileID: 42, SizeType: "m"}) {
		t.Fatalf("同一媒体应得到稳定键: %q", key)
	}
	if key == Key(mtproto.Thumb{Kind: "photo", FileID: 42, SizeType: "x"}) {
		t.Error("不同尺寸类型应视为不同缓存")
	}
	if key == Key(mtproto.Thumb{Kind: "document", FileID: 42, SizeType: "m"}) {
		t.Error("照片与文档不应共用缓存")
	}
}

// TestPutGet 覆盖写入、读取与路径分片。
func TestPutGet(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())
	key := Key(mtproto.Thumb{Kind: "photo", FileID: 7, SizeType: "m"})

	if _, ok := Get(key); ok {
		t.Fatal("未写入时不应命中")
	}

	path, err := Path(key)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(filepath.Dir(path)) != dir {
		t.Errorf("缓存路径 = %q，期望位于 %q 的两级分片下", path, dir)
	}

	if err := Put(key, []byte("jpeg-bytes")); err != nil {
		t.Fatal(err)
	}
	data, ok := Get(key)
	if !ok || string(data) != "jpeg-bytes" {
		t.Fatalf("Get = %q, %v", data, ok)
	}

	// 覆盖写也走同一路径。
	if err := Put(key, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if data, _ := Get(key); string(data) != "new" {
		t.Errorf("覆盖后 = %q", data)
	}
}

// TestPutEmpty 空内容不落盘。
func TestPutEmpty(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())
	if err := Put(Key(mtproto.Thumb{Kind: "photo", FileID: 1, SizeType: "m"}), nil); err == nil {
		t.Error("空内容应返回错误")
	}
}

// TestPathInvalidKey 非法键不产生路径。
func TestPathInvalidKey(t *testing.T) {
	if _, err := Path(""); err == nil {
		t.Error("空键应返回错误")
	}
}
