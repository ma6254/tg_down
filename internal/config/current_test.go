package config

import (
	"strings"
	"testing"
)

// TestValidateAccountID 覆盖当前账号 ID 的取值校验。
func TestValidateAccountID(t *testing.T) {
	for _, raw := range []string{"1", " 42 ", "999"} {
		if err := ValidateAccountID(raw); err != nil {
			t.Errorf("ValidateAccountID(%q) = %v，期望通过", raw, err)
		}
	}
	for _, raw := range []string{"", "abc", "0", "-1", "+8613800000000", "1.5"} {
		if err := ValidateAccountID(raw); err == nil {
			t.Errorf("ValidateAccountID(%q) 应报错", raw)
		}
	}
}

// TestCurrentAccountID 覆盖当前账号的读写与清除。
func TestCurrentAccountID(t *testing.T) {
	store := openStore(t)

	if _, ok, err := store.CurrentAccountID(); err != nil || ok {
		t.Fatalf("未设置时应 ok=false，实际 ok=%v err=%v", ok, err)
	}

	if err := store.SetCurrentAccountID(7); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	id, ok, err := store.CurrentAccountID()
	if err != nil || !ok {
		t.Fatalf("读取失败: ok=%v err=%v", ok, err)
	}
	if id != 7 {
		t.Errorf("id = %d，期望 7", id)
	}

	// 非法 ID 不应覆盖旧值。
	if err := store.SetCurrentAccountID(0); err == nil {
		t.Error("ID 0 应报错")
	}
	if id, _, _ := store.CurrentAccountID(); id != 7 {
		t.Errorf("非法写入后 id = %d", id)
	}

	if err := store.ClearCurrentAccountID(); err != nil {
		t.Fatalf("清除失败: %v", err)
	}
	if _, ok, err := store.CurrentAccountID(); err != nil || ok {
		t.Errorf("清除后应 ok=false，实际 ok=%v err=%v", ok, err)
	}
}

// TestCurrentAccountIDInvalidSetting 确认库里存了非法值时报错。
func TestCurrentAccountIDInvalidSetting(t *testing.T) {
	store := openStore(t)
	if err := store.db.SetSetting(KeyCurrentAccountID, "abc"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	_, _, err := store.CurrentAccountID()
	if err == nil {
		t.Fatal("非法存储值应报错")
	}
	if !strings.Contains(err.Error(), "不是合法账号 ID") {
		t.Errorf("错误信息 = %q", err.Error())
	}
}
