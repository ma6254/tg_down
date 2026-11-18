package mtproto

import (
	"fmt"
	"strings"
)

// 内置应用名。
const (
	// AppDesktop 使用 Telegram Desktop 的公开应用凭据，零配置即可连接。
	AppDesktop = "desktop"
	// AppCustom 表示使用用户自备的 api_id / api_hash。
	AppCustom = "custom"
)

// App 是一组 Telegram 应用凭据。
type App struct {
	ID   int
	Hash string
}

// presetApps 是内置应用凭据表。
//
// desktop 的凭据来自开源的 Telegram Desktop（2040），在公开文档与源码中本就可见，
// 因此不是真正的密钥；条款上不建议第三方冒用，这里只作为零配置兜底，
// 需要更稳妥的配额与可追溯性时请用 --app custom 换成自己申请的应用。
var presetApps = map[string]App{
	AppDesktop: {ID: 2040, Hash: "b18441a1ff607e10a989891a5462e627"},
}

// PresetApp 返回内置应用的凭据。
func PresetApp(name string) (App, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		name = AppDesktop
	}
	app, ok := presetApps[name]
	if !ok {
		return App{}, fmt.Errorf("未知应用 %q：内置仅支持 %s，自备凭据请用 %s", name, AppDesktop, AppCustom)
	}
	return app, nil
}

// PresetNames 返回内置应用名，供帮助信息使用。
func PresetNames() []string {
	names := make([]string, 0, len(presetApps))
	for name := range presetApps {
		names = append(names, name)
	}
	return names
}
