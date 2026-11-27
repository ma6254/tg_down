package mtproto

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAcquireValidatesOptions 确认参数不全时不发起任何网络连接。
func TestAcquireValidatesOptions(t *testing.T) {
	cases := []struct {
		name    string
		session Session
		want    string
	}{
		{"缺少 api 凭据", Session{SessionPath: "x.json"}, "缺少 api 凭据"},
		{
			"缺少 session 路径",
			Session{App: App{ID: 1, Hash: "h"}},
			"缺少 session 路径",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := tc.session.acquire(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v，期望包含 %q", err, tc.want)
			}
		})
	}
}

// TestAcquireMissingSessionFile 确认 session 文件不存在时给出可操作提示。
func TestAcquireMissingSessionFile(t *testing.T) {
	session := Session{
		App:         App{ID: 1, Hash: "h"},
		SessionPath: filepath.Join(t.TempDir(), "missing.json"),
	}
	_, _, err := session.acquire(context.Background())
	if err == nil || !strings.Contains(err.Error(), "未找到 session 文件") {
		t.Fatalf("err = %v，期望提示重新登录", err)
	}
}

// TestCloseConnectionsEmpty 确认池子为空时关闭操作直接返回。
func TestCloseConnectionsEmpty(t *testing.T) {
	closeEntries(func(poolKey) bool { return true })
	CloseConnections()
}

// TestSessionFingerprint 确认按内容判断 session 是否变化。
//
// gotd 每次连接都会重写 session 文件但内容不变，按 mtime 判断会让每个请求
// 都误判成「换了账号」并重建连接，所以这里必须按内容哈希。
func TestSessionFingerprint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")

	if _, err := sessionFingerprint(path); err == nil {
		t.Fatal("文件不存在时应当报错")
	}

	if err := os.WriteFile(path, []byte(`{"dc":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := sessionFingerprint(path)
	if err != nil {
		t.Fatalf("读取指纹: %v", err)
	}

	// 同内容重写（gotd 的正常行为）指纹不变。
	if err := os.WriteFile(path, []byte(`{"dc":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	same, err := sessionFingerprint(path)
	if err != nil {
		t.Fatalf("读取指纹: %v", err)
	}
	if first != same {
		t.Error("内容相同但指纹变化，会导致连接被无谓重建")
	}

	// 内容变化（重新登录换了 auth key）指纹必须变。
	if err := os.WriteFile(path, []byte(`{"dc":2,"auth_key":"new"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := sessionFingerprint(path)
	if err != nil {
		t.Fatalf("读取指纹: %v", err)
	}
	if first == changed {
		t.Error("内容变化但指纹没变，会继续用旧账号的连接")
	}
}
