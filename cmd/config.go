package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ma6254/tg_down/internal/config"
)

// configCmd 查看与修改系统配置。
var configCmd = &cobra.Command{
	Use:   "config",
	Short: "查看与修改系统配置",
	Long: `查看与修改保存在数据库里的系统配置。

当前支持的配置项：
  http_proxy   出站代理，登录与下载都走它

  tg_down config list
  tg_down config get http_proxy
  tg_down config set http_proxy http://127.0.0.1:7897
  tg_down config unset http_proxy
  tg_down config export                 # 默认 YAML
  tg_down config export -f json -o config.json

代理只从配置读取，没有命令行参数；未配置时直连。`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	},
}

var configListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "列出全部配置",
	Args:    cobra.NoArgs,
	RunE:    runConfigList,
}

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "读取配置项",
	Args:  cobra.ExactArgs(1),
	RunE:  runConfigGet,
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "写入配置项",
	Args:  cobra.ExactArgs(2),
	RunE:  runConfigSet,
}

var configUnsetCmd = &cobra.Command{
	Use:     "unset <key>",
	Aliases: []string{"delete", "remove"},
	Short:   "删除配置项",
	Args:    cobra.ExactArgs(1),
	RunE:    runConfigUnset,
}

// exportOptions 是 config export 的参数。
type exportOptions struct {
	Format string
	Output string
}

var exportOpts = &exportOptions{Format: "yaml"}

var configExportCmd = &cobra.Command{
	Use:   "export",
	Short: "导出配置为 yaml / json / toml",
	Long: `把配置文件导出成指定格式。

  tg_down config export                        # 默认 YAML，输出到终端
  tg_down config export --format json
  tg_down config export -f toml -o config.toml`,
	Args: cobra.NoArgs,
	RunE: runConfigExport,
}

// importOptions 是 config import 的参数。
type importOptions struct {
	Format  string
	Replace bool
}

var importOpts = &importOptions{}

var configImportCmd = &cobra.Command{
	Use:   "import <file>",
	Short: "从 yaml / json / toml 恢复配置",
	Long: `从文件恢复系统配置。

格式默认按扩展名推断（.json / .yaml / .yml / .toml），也可以用 --format 指定。
默认合并：只覆盖文件中出现的配置项；加 --replace 会先用文件重置已知配置项。

  tg_down config import config.yaml
  tg_down config import config.toml --replace
  tg_down config import backup.json --format json`,
	Args: cobra.ExactArgs(1),
	RunE: runConfigImport,
}

func init() {
	configExportCmd.Flags().StringVarP(&exportOpts.Format, "format", "f", exportOpts.Format, "导出格式：yaml / json / toml")
	configExportCmd.Flags().StringVarP(&exportOpts.Output, "output", "o", exportOpts.Output, "写入文件，留空输出到终端")

	configImportCmd.Flags().StringVarP(&importOpts.Format, "format", "f", importOpts.Format, "文件格式：yaml / json / toml，留空按扩展名推断")
	configImportCmd.Flags().BoolVar(&importOpts.Replace, "replace", importOpts.Replace, "用文件内容重置已知配置项")

	configCmd.AddCommand(configListCmd, configGetCmd, configSetCmd, configUnsetCmd, configExportCmd, configImportCmd)
}

// resolveProxy 从配置读取 http_proxy，未配置时返回空串（直连）。
func resolveProxy() (string, error) {
	db, err := openDatabase()
	if err != nil {
		return "", err
	}
	defer db.Close()

	value, ok, err := config.NewStore(db).Get(config.KeyHTTPProxy)
	if err != nil {
		return "", fmt.Errorf("读取 %s: %w", config.KeyHTTPProxy, err)
	}
	if !ok {
		return "", nil
	}
	return value, nil
}

func runConfigList(_ *cobra.Command, _ []string) error {
	db, err := openDatabase()
	if err != nil {
		return err
	}
	defer db.Close()

	entries, err := config.NewStore(db).List()
	if err != nil {
		return fmt.Errorf("读取配置: %w", err)
	}
	if len(entries) == 0 {
		fmt.Println("暂无配置（用 config set <key> <value> 添加）")
		return nil
	}

	fmt.Println("配置项：")
	for _, entry := range entries {
		note := ""
		if !entry.Known {
			note = "（未登记的键）"
		}
		fmt.Printf("  %-12s = %s%s\n", entry.Key, config.Mask(entry.Key, entry.Value), note)
	}
	return nil
}

func runConfigGet(_ *cobra.Command, args []string) error {
	key := args[0]

	db, err := openDatabase()
	if err != nil {
		return err
	}
	defer db.Close()

	value, ok, err := config.NewStore(db).Get(key)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("未配置 %s（可用 config set %s <value> 设置）", key, key)
	}
	fmt.Println(config.Mask(key, value))
	return nil
}

func runConfigSet(_ *cobra.Command, args []string) error {
	key, value := args[0], args[1]

	db, err := openDatabase()
	if err != nil {
		return err
	}
	defer db.Close()

	if err := config.NewStore(db).Set(key, value); err != nil {
		return err
	}
	fmt.Printf("已设置 %s = %s\n", key, config.Mask(key, value))
	return nil
}

func runConfigUnset(_ *cobra.Command, args []string) error {
	key := args[0]

	db, err := openDatabase()
	if err != nil {
		return err
	}
	defer db.Close()

	if err := config.NewStore(db).Unset(key); err != nil {
		return err
	}
	fmt.Printf("已删除配置 %s\n", key)
	return nil
}

// runConfigExport 把配置导出为指定格式。
func runConfigExport(_ *cobra.Command, _ []string) error {
	format, err := config.ParseFormat(exportOpts.Format)
	if err != nil {
		return err
	}

	db, err := openDatabase()
	if err != nil {
		return err
	}
	defer db.Close()

	data, err := config.NewStore(db).Export(format)
	if err != nil {
		return err
	}

	if exportOpts.Output == "" {
		_, err = os.Stdout.Write(data)
		return err
	}
	if err := os.WriteFile(exportOpts.Output, data, 0o600); err != nil {
		return fmt.Errorf("写入 %s: %w", exportOpts.Output, err)
	}
	fmt.Printf("已导出 %d 字节到 %s\n", len(data), exportOpts.Output)
	return nil
}

// runConfigImport 从文件恢复配置。
func runConfigImport(_ *cobra.Command, args []string) error {
	path := args[0]
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取 %s: %w", path, err)
	}

	var format config.Format
	if importOpts.Format == "" {
		format, err = config.FormatFromPath(path)
	} else {
		format, err = config.ParseFormat(importOpts.Format)
	}
	if err != nil {
		return err
	}

	db, err := openDatabase()
	if err != nil {
		return err
	}
	defer db.Close()

	store := config.NewStore(db)
	var result config.ImportResult
	if importOpts.Replace {
		result, err = store.Replace(format, data)
	} else {
		result, err = store.Import(format, data)
	}
	if err != nil {
		return err
	}

	fmt.Printf("已恢复配置：更新 %d 项", result.Updated)
	if result.Removed > 0 {
		fmt.Printf("，删除 %d 项", result.Removed)
	}
	if len(result.Skipped) > 0 {
		fmt.Printf("，跳过 %d 个未登记键（%s）", len(result.Skipped), strings.Join(result.Skipped, ", "))
	}
	fmt.Println()
	return nil
}
