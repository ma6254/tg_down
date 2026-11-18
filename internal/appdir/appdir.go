// Package appdir 负责解析 tg_down 的运行数据目录。
//
// 默认使用用户主目录下的 .tg_down，可用环境变量 TG_DOWN_HOME 覆盖，
// 便于测试或把数据放到自定义位置（如 U 盘、同步目录）。
package appdir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DirName 是默认的数据目录名。
const DirName = ".tg_down"

// EnvHome 是指定数据目录的环境变量名。
const EnvHome = "TG_DOWN_HOME"

// DataDir 返回数据目录路径，不保证目录已存在。
func DataDir() (string, error) {
	if custom := strings.TrimSpace(os.Getenv(EnvHome)); custom != "" {
		return custom, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("解析用户主目录: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", errors.New("无法确定用户主目录，请设置 " + EnvHome)
	}
	return filepath.Join(home, DirName), nil
}

// EnsureDataDir 创建数据目录（权限 0700）并返回路径。
func EnsureDataDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("创建数据目录 %s: %w", dir, err)
	}
	return dir, nil
}

// DBPath 返回 SQLite 数据文件路径，并确保所在目录存在。
func DBPath() (string, error) {
	dir, err := EnsureDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "data.db"), nil
}
