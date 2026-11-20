package serve

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
	"github.com/ma6254/tg_down/internal/utils"
)

// TestAvatarCacheRoundTrip 覆盖头像文件的路径、读写与 TTL 判断。
func TestAvatarCacheRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TG_DOWN_HOME", home)

	path, err := avatarCachePath(7)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(filepath.Dir(path)) != home {
		t.Errorf("头像路径 = %q，期望位于 %q 下", path, home)
	}
	if _, ok := utils.ReadFileBytes(path); ok {
		t.Fatal("未写入时不应命中")
	}

	if err := utils.WriteFileAtomic(path, []byte("jpeg-bytes")); err != nil {
		t.Fatal(err)
	}
	data, ok := utils.ReadFileBytes(path)
	if !ok || string(data) != "jpeg-bytes" {
		t.Fatalf("读取 = %q，%v", data, ok)
	}
	if !avatarFresh(path) {
		t.Error("刚写入的缓存应视为新鲜")
	}

	// 把 mtime 改到 25 小时前：仍可读取，但不再新鲜。
	old := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if avatarFresh(path) {
		t.Error("超过 TTL 后不应视为新鲜")
	}
	if _, ok := utils.ReadFileBytes(path); !ok {
		t.Error("过期文件仍应能作为兜底读取")
	}
}

// TestPeerAvatarCachePath 确认群/频道/用户头像落在 avatar/ 下并按 peer+photo 命名。
func TestPeerAvatarCachePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TG_DOWN_HOME", home)

	peer := mtproto.Peer{Kind: mtproto.PeerKindChannel, ID: 100}
	path, err := peerAvatarCachePath(peer, 200)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "avatar", "peer-channel-100-200.jpg")
	if path != want {
		t.Errorf("path = %q，期望 %q", path, want)
	}

	if err := utils.WriteFileAtomic(path, []byte("jpeg")); err != nil {
		t.Fatal(err)
	}
	if data, ok := utils.ReadFileBytes(path); !ok || string(data) != "jpeg" {
		t.Errorf("读取 = %q，%v", data, ok)
	}
}

// TestLoadAvatarMigratesLegacy 确认旧版本按本地 ID 命名的文件会迁到 user_id 下。
func TestLoadAvatarMigratesLegacy(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())

	legacy, err := avatarCachePath(9)
	if err != nil {
		t.Fatal(err)
	}
	if err := utils.WriteFileAtomic(legacy, []byte("old-avatar")); err != nil {
		t.Fatal(err)
	}

	path, err := avatarCachePath(355927778)
	if err != nil {
		t.Fatal(err)
	}
	data, ok := loadAvatar(9, 355927778, path)
	if !ok || string(data) != "old-avatar" {
		t.Fatalf("loadAvatar = %q，%v", data, ok)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Error("旧文件应已迁移")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("新路径没有文件: %v", err)
	}
}
