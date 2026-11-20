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

// TestMessageEntities 覆盖常见实体类型与附加字段，并确认排序为「起点升序、跨度降序」。
func TestMessageEntities(t *testing.T) {
	// UTF-16 计长：看=1，空格=1，😀=2，空格=1，这段代码=4。
	msg := &tg.Message{Message: "看 😀 这段代码"}
	msg.SetEntities([]tg.MessageEntityClass{
		&tg.MessageEntityPre{Offset: 5, Length: 4, Language: "go"},
		&tg.MessageEntityCustomEmoji{Offset: 2, Length: 2, DocumentID: 42},
		&tg.MessageEntityTextURL{Offset: 0, Length: 1, URL: "https://t.me/x/1"},
		&tg.MessageEntityBold{Offset: 0, Length: 1},
	})

	got := messageEntities(msg)
	want := []MessageEntity{
		{Type: "bold", Offset: 0, Length: 1},
		{Type: "text_link", Offset: 0, Length: 1, URL: "https://t.me/x/1"},
		{Type: "custom_emoji", Offset: 2, Length: 2, DocumentID: 42},
		{Type: "pre", Offset: 5, Length: 4, Language: "go"},
	}
	if len(got) != len(want) {
		t.Fatalf("实体数量 = %d，期望 %d：%+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 个实体 = %+v，期望 %+v", i+1, got[i], want[i])
		}
	}
}

// TestMessageEntitiesClamp 确认越界实体被裁剪、完全越界的被丢弃、重复实体只留一条。
func TestMessageEntitiesClamp(t *testing.T) {
	msg := &tg.Message{Message: "abc"}
	msg.SetEntities([]tg.MessageEntityClass{
		&tg.MessageEntityBold{Offset: 0, Length: 3},
		&tg.MessageEntityBold{Offset: 0, Length: 3},   // 重复
		&tg.MessageEntityItalic{Offset: 2, Length: 5}, // 跨右边界
		&tg.MessageEntityCode{Offset: 4, Length: 2},   // 完全在正文之外
	})

	got := messageEntities(msg)
	want := []MessageEntity{
		{Type: "bold", Offset: 0, Length: 3},
		{Type: "italic", Offset: 2, Length: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("实体数量 = %d，期望 %d：%+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 个实体 = %+v，期望 %+v", i+1, got[i], want[i])
		}
	}
}

// TestMessageEntitiesFormattedDate 确认日期实体的格式标记折叠成单个枚举。
func TestMessageEntitiesFormattedDate(t *testing.T) {
	msg := &tg.Message{Message: "2026-11-20"}
	msg.SetEntities([]tg.MessageEntityClass{
		&tg.MessageEntityFormattedDate{Offset: 0, Length: 10, Date: 1794931200, Relative: true},
		&tg.MessageEntityFormattedDate{Offset: 0, Length: 10, Date: 1794931200, LongTime: true, ShortDate: true},
		&tg.MessageEntityFormattedDate{Offset: 0, Length: 10, Date: 1794931200},
	})

	got := messageEntities(msg)
	if len(got) != 3 {
		t.Fatalf("实体数量 = %d，期望 3：%+v", len(got), got)
	}
	wantLayouts := []string{"relative", "time_long", ""}
	for i, want := range wantLayouts {
		if got[i].Type != "date_time" || got[i].DateFormat != want {
			t.Errorf("第 %d 个实体 = %+v，期望日期格式 %q", i+1, got[i], want)
		}
	}
}

// TestTrimMessage 确认裁剪首尾空白后实体偏移整体平移，跨界实体被裁剪。
func TestTrimMessage(t *testing.T) {
	// UTF-16 计长：\n=1，两个空格=2，abc=3，两个空格=2，\n=1，共 9。
	text := "\n  abc  \n"
	entities := []MessageEntity{
		{Type: "bold", Offset: 3, Length: 3},   // 正好是 abc
		{Type: "italic", Offset: 0, Length: 2}, // 落在被裁掉的空白里
		{Type: "code", Offset: 7, Length: 2},   // 起点已在正文之外
	}

	trimmed, got := trimMessage(text, entities)
	if trimmed != "abc" {
		t.Fatalf("裁剪结果 = %q，期望 abc", trimmed)
	}
	if len(got) != 1 || got[0].Type != "bold" || got[0].Offset != 0 || got[0].Length != 3 {
		t.Fatalf("实体 = %+v，期望只剩偏移 0、长度 3 的 bold", got)
	}
}

// TestTrimMessageNoEntities 确认没有实体时只做裁剪，不额外分配切片。
func TestTrimMessageNoEntities(t *testing.T) {
	trimmed, entities := trimMessage("  hello \n", nil)
	if trimmed != "hello" {
		t.Errorf("裁剪结果 = %q，期望 hello", trimmed)
	}
	if entities != nil {
		t.Errorf("实体 = %+v，期望 nil", entities)
	}
}
