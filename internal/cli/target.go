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
    tgtUpstream string
    tgtAddress  string
    tgtWeight   int
)

var targetCmd = &cobra.Command{
    Use:   "target",
    Short: "管理 Upstream 的 Target（后端节点）",
}

var targetAddCmd = &cobra.Command{
    Use:   "add",
    Short: "向 Upstream 添加 Target",
    Example: `# 向 user-service-upstream 添加一个后端节点
kongctl target add --upstream user-service-upstream --target user-svc-1:8080 --weight 100`,
    RunE: func(cmd *cobra.Command, args []string) error {
        if tgtUpstream == "" || tgtAddress == "" {
            return fmt.Errorf("必须提供 --upstream 与 --target")
        }
        if tgtWeight == 0 { tgtWeight = 100 }
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
        if _, err := client.AddTarget(ctx, tgtUpstream, tgtAddress, tgtWeight); err != nil {
            return err
        }
        PrintSuccess(cmd, "已添加 Target：%s (weight=%d) 到 Upstream：%s", tgtAddress, tgtWeight, tgtUpstream)
        return nil
    },
}

func init() {
    targetCmd.AddCommand(targetAddCmd)
    targetAddCmd.Flags().StringVar(&tgtUpstream, "upstream", "", "Upstream 名称，例：user-service-upstream")
    targetAddCmd.Flags().StringVar(&tgtAddress, "target", "", "后端地址 host:port，例：10.0.0.1:8080 或 app:8080")
    targetAddCmd.Flags().IntVar(&tgtWeight, "weight", 100, "权重（默认 100），例：--weight 100")
}
