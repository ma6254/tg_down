package link

import (
	"strings"
	"testing"
)

// TestParse 覆盖常见的链接形态与解析结果。
func TestParse(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want Target
	}{
		{
			name: "公开频道的评论链接",
			raw:  "https://t.me/JPD233/2522?comment=12454",
			want: Target{Username: "jpd233", MessageID: 2522, CommentID: 12454},
		},
		{
			name: "公开频道帖子",
			raw:  "https://t.me/somechannel/123",
			want: Target{Username: "somechannel", MessageID: 123},
		},
		{
			name: "省略协议",
			raw:  "t.me/somechannel/123",
			want: Target{Username: "somechannel", MessageID: 123},
		},
		{
			name: "telegram.me 域名",
			raw:  "https://telegram.me/somechannel/123",
			want: Target{Username: "somechannel", MessageID: 123},
		},
		{
			name: "网页预览的 s 前缀",
			raw:  "https://t.me/s/somechannel/123",
			want: Target{Username: "somechannel", MessageID: 123},
		},
		{
			name: "私有频道",
			raw:  "https://t.me/c/1234567890/42",
			want: Target{ChannelID: 1234567890, MessageID: 42},
		},
		{
			name: "私有频道话题",
			raw:  "https://t.me/c/1234567890/251015/251021",
			want: Target{ChannelID: 1234567890, TopicID: 251015, MessageID: 251021},
		},
		{
			name: "公开群话题",
			raw:  "https://t.me/iFreeKnow/45662/55005",
			want: Target{Username: "ifreeknow", TopicID: 45662, MessageID: 55005},
		},
		{
			name: "thread 查询串",
			raw:  "https://t.me/myhostloc/1485524?thread=1485523",
			want: Target{Username: "myhostloc", MessageID: 1485524, TopicID: 1485523},
		},
		{
			name: "结尾斜杠",
			raw:  "https://t.me/somechannel/123/",
			want: Target{Username: "somechannel", MessageID: 123},
		},
		{
			name: "大写用户名转小写",
			raw:  "https://t.me/SomeChannel/123",
			want: Target{Username: "somechannel", MessageID: 123},
		},
		{
			name: "私有频道的评论链接",
			raw:  "https://t.me/c/1234567890/42?comment=99",
			want: Target{ChannelID: 1234567890, MessageID: 42, CommentID: 99},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.raw)
			if err != nil {
				t.Fatalf("Parse(%q) 报错: %v", tt.raw, err)
			}
			tt.want.Raw = strings.TrimSpace(tt.raw)
			if got != tt.want {
				t.Fatalf("Parse(%q) = %+v，期望 %+v", tt.raw, got, tt.want)
			}
			if got.IsComment() != (got.CommentID > 0) {
				t.Fatalf("IsComment 与 CommentID 不一致: %+v", got)
			}
			if got.IsPrivate() != (got.ChannelID > 0) {
				t.Fatalf("IsPrivate 与 ChannelID 不一致: %+v", got)
			}
		})
	}
}

// TestParseErrors 覆盖应当被拒绝的输入与错误文案。
func TestParseErrors(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{"空链接", "   ", "链接为空"},
		{"不是 Telegram 域名", "https://example.com/somechannel/123", "不是 Telegram"},
		{"邀请链接", "https://t.me/+AbCdEfGh", "邀请链接"},
		{"joinchat 链接", "https://t.me/joinchat/AbCdEfGh", "邀请链接"},
		{"只有频道没有消息", "https://t.me/somechannel", "消息链接格式"},
		{"缺少消息 ID", "https://t.me/c/1234567890", "频道链接格式"},
		{"频道 ID 不是数字", "https://t.me/c/abc/42", "频道 ID"},
		{"消息 ID 为零", "https://t.me/somechannel/0", "消息 ID"},
		{"消息 ID 不是数字", "https://t.me/somechannel/abc", "消息 ID"},
		{"评论 ID 不是数字", "https://t.me/somechannel/123?comment=abc", "评论消息 ID"},
		{"tg 协议", "tg://resolve?domain=somechannel&post=1", "tg://"},
		{"只有域名", "https://t.me/", "没有消息路径"},
		{"用户名是纯数字", "https://t.me/123456/789", "用户名"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.raw)
			if err == nil {
				t.Fatalf("Parse(%q) 期望报错，实际通过", tt.raw)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("错误 %q 未包含 %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestParseAll 确认单条失败不会影响其它链接，且顺序保持不变。
func TestParseAll(t *testing.T) {
	results := ParseAll([]string{
		"https://t.me/somechannel/1",
		"not-a-link",
		" https://t.me/c/1234567890/2 ",
	})

	if len(results) != 3 {
		t.Fatalf("结果数量 = %d，期望 3", len(results))
	}
	if results[0].Err != nil {
		t.Fatalf("第 1 条不应报错: %v", results[0].Err)
	}
	if results[1].Err == nil {
		t.Fatal("第 2 条应当报错")
	}
	if results[2].Err != nil {
		t.Fatalf("第 3 条不应报错: %v", results[2].Err)
	}
	if results[2].Target.ChannelID != 1234567890 || results[2].Target.MessageID != 2 {
		t.Fatalf("第 3 条解析结果不正确: %+v", results[2].Target)
	}
	if results[2].Raw != "https://t.me/c/1234567890/2" {
		t.Fatalf("Raw 未去除首尾空白: %q", results[2].Raw)
	}
}

// TestTargetKey 确认去重标识能区分评论，并且忽略写法差异（大小写、话题路径）。
func TestTargetKey(t *testing.T) {
	post, err := Parse("https://t.me/JPD233/2522")
	if err != nil {
		t.Fatal(err)
	}
	samePost, err := Parse("t.me/jpd233/2522?thread=9")
	if err != nil {
		t.Fatal(err)
	}
	comment, err := Parse("https://t.me/JPD233/2522?comment=12454")
	if err != nil {
		t.Fatal(err)
	}
	private, err := Parse("https://t.me/c/1234567890/2522")
	if err != nil {
		t.Fatal(err)
	}

	if post.Key() != samePost.Key() {
		t.Errorf("同一帖子的不同写法 Key 不一致: %q vs %q", post.Key(), samePost.Key())
	}
	if post.Key() == comment.Key() {
		t.Errorf("帖子与评论的 Key 不应相同: %q", post.Key())
	}
	if post.Key() == private.Key() {
		t.Errorf("公开频道与私有频道的 Key 不应相同: %q", post.Key())
	}
}

// TestTargetURL 确认规范链接的拼装规则。
func TestTargetURL(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"https://t.me/JPD233/2522", "https://t.me/jpd233/2522"},
		{"https://t.me/JPD233/2522?comment=12454", "https://t.me/jpd233/2522?comment=12454"},
		{"https://t.me/c/1234567890/42", "https://t.me/c/1234567890/42"},
		// 话题路径在规范链接里省略，消息 ID 已足够定位。
		{"https://t.me/somechannel/45662/55005", "https://t.me/somechannel/55005"},
	}

	for _, tt := range tests {
		target, err := Parse(tt.raw)
		if err != nil {
			t.Fatalf("Parse(%q) 报错: %v", tt.raw, err)
		}
		if got := target.URL(); got != tt.want {
			t.Errorf("Parse(%q).URL() = %q，期望 %q", tt.raw, got, tt.want)
		}
	}
}
