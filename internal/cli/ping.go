package cli

import (
    "context"
    "fmt"
    "time"

    "github.com/spf13/cobra"
    "github.com/spf13/viper"
    "kongctl/internal/kong"
)

var pingCmd = &cobra.Command{
    Use:   "ping",
    Short: "连通性自检（访问 Admin API）🏓",
    Example: `# 使用已配置的 Admin URL（推荐搭配 kongctl init）
kongctl ping

# 临时指定 Admin URL
kongctl ping --admin-url http://localhost:8001`,
    RunE: func(cmd *cobra.Command, args []string) error {
        adminURL := viper.GetString("admin_url")
        if adminURL == "" {
            return fmt.Errorf("请通过 --admin-url 或 KONGCTL_ADMIN_URL 指定 Admin API 地址；或运行 'kongctl init --admin-url <url>' 持久化配置")
        }
        cfg := kong.Config{
            AdminURL:      adminURL,
            Token:         viper.GetString("token"),
            Workspace:     viper.GetString("workspace"),
            TLSSkipVerify: viper.GetBool("tls_skip_verify"),
            Timeout:       5 * time.Second,
        }
        client := kong.NewClient(cfg)
        ctx, cancel := context.WithTimeout(cmd.Context(), cfg.Timeout)
        defer cancel()

        if err := client.Ping(ctx); err != nil {
            return fmt.Errorf("连接失败：%v", err)
        }
        PrintSuccess(cmd, "连通正常")
        return nil
    },
}
