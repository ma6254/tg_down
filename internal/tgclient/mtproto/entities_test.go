package mtproto

import (
	"testing"

	"github.com/gotd/td/tg"
)

// TestMessageLinks 覆盖 text_link、纯 URL 实体与去重。
func TestMessageLinks(t *testing.T) {
	msg := &tg.Message{Message: "第1集 | 第2集 | 官网"}
	msg.SetEntities([]tg.MessageEntityClass{
		&tg.MessageEntityTextURL{Offset: 0, Length: 3, URL: "https://t.me/JPD233/2522?comment=1"},
		&tg.MessageEntityTextURL{Offset: 6, Length: 3, URL: "https://t.me/JPD233/2522?comment=2"},
		// 同一地址重复出现时只保留一条。
		&tg.MessageEntityTextURL{Offset: 6, Length: 3, URL: "https://t.me/JPD233/2522?comment=2"},
		&tg.MessageEntityURL{Offset: 12, Length: 2},
	})

	links := messageLinks(msg)
	if len(links) != 3 {
		t.Fatalf("链接数量 = %d，期望 3: %+v", len(links), links)
	}

	want := []MessageLink{
		{Text: "第1集", URL: "https://t.me/JPD233/2522?comment=1"},
		{Text: "第2集", URL: "https://t.me/JPD233/2522?comment=2"},
		{Text: "官网", URL: "官网"},
	}
	for i := range want {
		if links[i] != want[i] {
			t.Errorf("第 %d 条 = %+v，期望 %+v", i+1, links[i], want[i])
		}
	}
}

// TestMessageLinksUTF16 确认 emoji 之类的双码元字符不会切错片段。
func TestMessageLinksUTF16(t *testing.T) {
	const target = "https://t.me/x/1"
	msg := &tg.Message{Message: "😀" + target}
	msg.SetEntities([]tg.MessageEntityClass{
		&tg.MessageEntityURL{Offset: 2, Length: len(target)},
	})

	links := messageLinks(msg)
	if len(links) != 1 || links[0].URL != target {
		t.Fatalf("links = %+v，期望 %q", links, target)
	}
}

// TestUTF16Slice 覆盖非法偏移。
func TestUTF16Slice(t *testing.T) {
	units := []uint16{'a', 'b', 'c'}
	if got := utf16Slice(units, 0, 2); got != "ab" {
		t.Errorf("utf16Slice 正常范围 = %q", got)
	}
	if got := utf16Slice(units, 2, 5); got != "" {
		t.Errorf("越界应返回空串，实际 %q", got)
	}
	if got := utf16Slice(units, -1, 1); got != "" {
		t.Errorf("负偏移应返回空串，实际 %q", got)
	}
}
