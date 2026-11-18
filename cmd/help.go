package cmd

import (
	"strings"

	"github.com/spf13/cobra"
)

// usageTemplate 与 cobra v1.10.2 的 defaultUsageTemplate 保持一致，
// 只在 Available Commands / Additional Commands 的说明后插入别称后缀。
// 升级 cobra 时需要对照其 defaultUsageTemplate 同步更新本模板。
const usageTemplate = `Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

Available Commands:{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{aliasSuffix .}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{aliasSuffix .}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

Additional Commands:{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{aliasSuffix .}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

Additional help topics:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`

func init() {
	cobra.AddTemplateFunc("aliasSuffix", func(c *cobra.Command) string {
		if len(c.Aliases) == 0 {
			return ""
		}
		return "（别名 " + strings.Join(c.Aliases, ", ") + "）"
	})
}

// applyUsageTemplate 让 root 及其子命令使用带别称的帮助模板。
// cobra 的子命令会回退到父命令的模板，因此只需设置在 root 上。
func applyUsageTemplate(root *cobra.Command) {
	root.SetUsageTemplate(usageTemplate)
}
