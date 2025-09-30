package cli

import (
    "context"
    "fmt"
    "time"

    "github.com/spf13/cobra"
    "github.com/spf13/viper"
    "kongctl/internal/kong"
)

var (
    upstreamName string
)

var upstreamCmd = &cobra.Command{
    Use:   "upstream",
    Short: "管理 Upstream（负载均衡上游）",
}

var upstreamSyncCmd = &cobra.Command{
    Use:   "sync",
    Short: "创建或更新 Upstream（幂等）",
    Example: `# 创建或确保存在一个名为 user-service-upstream 的上游
kongctl upstream sync --name user-service-upstream`,
    RunE: func(cmd *cobra.Command, args []string) error {
        if upstreamName == "" { return fmt.Errorf("必须提供 --name") }
        cfg := kong.Config{
            AdminURL:      viper.GetString("admin_url"),
            Token:         viper.GetString("token"),
            TLSSkipVerify: viper.GetBool("tls_skip_verify"),
            Timeout:       10 * time.Second,
        }
        if cfg.AdminURL == "" { return fmt.Errorf("请通过 --admin-url 或 KONGCTL_ADMIN_URL 指定 Admin API 地址；或运行 'kongctl init --admin-url <url>' 持久化配置") }
        client := kong.NewClient(cfg)
        ctx, cancel := context.WithTimeout(cmd.Context(), cfg.Timeout)
        defer cancel()
        action, _, err := client.CreateOrUpdateUpstream(ctx, upstreamName)
        if err != nil { return err }
        if action == "create" {
            PrintSuccess(cmd, "已创建 Upstream：%s", upstreamName)
        } else {
            PrintSuccess(cmd, "已更新 Upstream：%s", upstreamName)
        }
        return nil
    },
}

func init() {
    upstreamCmd.AddCommand(upstreamSyncCmd)
    upstreamSyncCmd.Flags().StringVar(&upstreamName, "name", "", "Upstream 名称，例：user-service-upstream")
}
