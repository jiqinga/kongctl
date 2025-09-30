package cli

import (
    "fmt"
    "os"

    "github.com/spf13/cobra"
    "github.com/spf13/viper"
)

var (
    version = "0.1.0"
    cfgFile string
)

// 根命令
var rootCmd = &cobra.Command{
    Use:   "kongctl",
    Short: "Kong 管理命令行工具（OpenAPI/Service/Route 等）",
    Long:  "Kong 管理命令行工具：支持基于 OpenAPI 与 Admin API 的幂等创建/更新 Service、Route、Upstream、Target 与常用插件。",
    SilenceUsage:  true,  // 出错时不显示 usage/help
    SilenceErrors: true,  // 交由自定义 Execute 统一打印错误
    Example: `# 1) 首次配置（写入 ~/.kongctl/config.yaml）
kongctl init --admin-url http://localhost:8001 --token <KONG_ADMIN_TOKEN>

# 2) 连通性自检
kongctl ping

# 3) 从文件批量应用（支持 YAML/JSON）
kongctl apply -f examples/apply.yaml
kongctl apply -f examples/route-simple.yaml --dry-run --diff

# 4) 单资源操作
kongctl service sync --name echo --url http://httpbin.org
kongctl route sync --service user-service --paths /v1/users --methods GET
kongctl upstream sync --name user-service-upstream
kongctl target add --upstream user-service-upstream --target user-svc-1:8080 --weight 100`,
}

// Execute 入口
func Execute() {
    if err := rootCmd.Execute(); err != nil {
        fmt.Fprintf(os.Stderr, "%s\n", ErrorMessage(err.Error()))
        // 不返回非零退出码，避免 shell 显示 "exit status 1"
        return
    }
}

func init() {
    cobra.OnInitialize(initConfig)
    // 使用自定义中文 completion，禁用默认英文版
    rootCmd.CompletionOptions.DisableDefaultCmd = true

    // 全局配置项
    rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "配置文件路径（默认：~/.kongctl/config.yaml），例：--config ./kongctl.yaml")
    rootCmd.PersistentFlags().String("admin-url", "", "Kong Admin API 地址，例：http://localhost:8001 或 https://kong-admin:8444")
    rootCmd.PersistentFlags().String("token", "", "Kong Admin Token（可选），例：--token $KONG_ADMIN_TOKEN")
    rootCmd.PersistentFlags().String("workspace", "", "Kong Workspace（可选），例：--workspace default")
    rootCmd.PersistentFlags().Bool("tls-skip-verify", false, "跳过 TLS 证书校验（不建议生产使用），例：--tls-skip-verify")
    rootCmd.PersistentFlags().Bool("no-color", false, "禁用彩色输出（环境变量 NO_COLOR 亦可生效），例：--no-color")

    // 绑定 Viper
    _ = viper.BindPFlag("admin_url", rootCmd.PersistentFlags().Lookup("admin-url"))
    _ = viper.BindPFlag("token", rootCmd.PersistentFlags().Lookup("token"))
    _ = viper.BindPFlag("workspace", rootCmd.PersistentFlags().Lookup("workspace"))
    _ = viper.BindPFlag("tls_skip_verify", rootCmd.PersistentFlags().Lookup("tls-skip-verify"))
    _ = viper.BindPFlag("no_color", rootCmd.PersistentFlags().Lookup("no-color"))

    // 环境变量：KONGCTL_ADMIN_URL 等
    viper.SetEnvPrefix("KONGCTL")
    viper.AutomaticEnv()

    // 子命令装配
    rootCmd.AddCommand(pingCmd)
    rootCmd.AddCommand(initCmd)
    rootCmd.AddCommand(serviceCmd)
    rootCmd.AddCommand(routeCmd)
    rootCmd.AddCommand(upstreamCmd)
    rootCmd.AddCommand(targetCmd)
    rootCmd.AddCommand(completionCmd)
    rootCmd.AddCommand(versionCmd)

}

func initConfig() {
    // 配置加载顺序：flag > env > file
    if cfgFile != "" {
        viper.SetConfigFile(cfgFile)
    } else {
        home, _ := os.UserHomeDir()
        viper.AddConfigPath(home + "/.kongctl")
        viper.SetConfigName("config")
        viper.SetConfigType("yaml")
    }
    _ = viper.ReadInConfig() // 文件不存在也不报错
}
