package mtproto

import (
	"testing"

	"github.com/gotd/td/tg"
)

// TestProfileFromFull 覆盖生日与简介的提取。
func TestProfileFromFull(t *testing.T) {
	full := &tg.UsersUserFull{FullUser: tg.UserFull{About: "hello"}}
	full.FullUser.SetBirthday(tg.Birthday{Day: 12, Month: 5, Year: 1990})

	info := profileFromFull(full)
	if info.About != "hello" {
		t.Errorf("About = %q", info.About)
	}
	if !info.HasBirthday || info.BirthDay != 12 || info.BirthMonth != 5 || info.BirthYear != 1990 {
		t.Errorf("生日解析错误: %+v", info)
	}
}

// TestProfileFromFullNoBirthday 确认未公开生日时字段为默认值。
func TestProfileFromFullNoBirthday(t *testing.T) {
	full := &tg.UsersUserFull{FullUser: tg.UserFull{}}

	info := profileFromFull(full)
	if info.HasBirthday {
		t.Errorf("不应有生日: %+v", info)
	}
}
