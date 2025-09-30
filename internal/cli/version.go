package cli

import (
    "fmt"
    "runtime"

    "github.com/spf13/cobra"
)

// version 变量在构建时通过 -ldflags -X 进行注入（见 GoReleaser 配置）
// 默认值用于本地未注入场景
var versionCmd = &cobra.Command{
    Use:   "version",
    Short: "显示版本信息",
    Run: func(cmd *cobra.Command, args []string) {
        v := version
        if v == "" { v = "dev" }
        fmt.Fprintf(cmd.OutOrStdout(), "kongctl version: %s\nGo: %s\nOS/Arch: %s/%s\n", v, runtime.Version(), runtime.GOOS, runtime.GOARCH)
    },
}
