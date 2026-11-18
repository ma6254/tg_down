package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/ma6254/tg_down/internal/build"
)

// versionCmd 打印版本与构建信息，构建时由 -ldflags 注入。
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "打印版本与构建信息",
	Args:  cobra.NoArgs,
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Printf("tg_down %s\n", build.Version)
		fmt.Printf("commit:   %s\n", build.Commit)
		fmt.Printf("built:    %s\n", build.BuildTime)
		fmt.Printf("go:       %s\n", build.GoVersion())
		fmt.Printf("platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	},
}
