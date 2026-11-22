//go:build !webdist

package webembed

import "testing"

// TestDistUnavailable 确认普通构建没有内嵌前端。
func TestDistUnavailable(t *testing.T) {
	if dist, ok := Dist(); ok || dist != nil {
		t.Fatalf("未启用 webdist 时应没有内嵌产物，实际 %v", dist)
	}
}
