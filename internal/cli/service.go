package cli

import (
    "context"
    "fmt"
    "net/url"
    "strconv"
    "strings"
    "time"

    "github.com/spf13/cobra"
    "github.com/spf13/viper"
    "kongctl/internal/kong"
)

var (
    svcName string
    svcURL  string
    svcPath string
    dryRun  bool
    showDiff bool
    autoUpstream bool
    svcUpstream string
    targetWeight int
)

var serviceCmd = &cobra.Command{
    Use:   "service",
    Short: "管理 Service 资源",
}

var serviceSyncCmd = &cobra.Command{
    Use:   "sync",
    Short: "创建或更新 Service（幂等）",
    Example: `# 方式一：直接使用 URL 指向上游
kongctl service sync --name echo --url http://httpbin.org

# 方式二：自动创建 Upstream 并将 Service 指向它（默认 upstream=<name>-upstream）
kongctl service sync --name user --url http://user-svc:8080 --auto-upstream

# 自定义 upstream 与路径、权重，并展示差异
kongctl service sync --name user --upstream user-up --path /api --weight 100 --auto-upstream --diff

# 仅预览
kongctl service sync --name user --url http://user-svc:8080 --dry-run --diff`,
    RunE: func(cmd *cobra.Command, args []string) error {
        if svcName == "" || svcURL == "" {
            return fmt.Errorf("必须提供 --name 与 --url")
        }
        cfg := kong.Config{
            AdminURL:      viper.GetString("admin_url"),
            Token:         viper.GetString("token"),
            TLSSkipVerify: viper.GetBool("tls_skip_verify"),
            Timeout:       10 * time.Second,
        }
        if cfg.AdminURL == "" {
            return fmt.Errorf("请通过 --admin-url 或 KONGCTL_ADMIN_URL 指定 Admin API 地址；或运行 'kongctl init --admin-url <url>' 持久化配置")
        }
        client := kong.NewClient(cfg)
        ctx, cancel := context.WithTimeout(cmd.Context(), cfg.Timeout)
        defer cancel()

        // 解析 URL
        u, err := url.Parse(svcURL)
        if err != nil { return fmt.Errorf("无效的 --url：%w", err) }
        proto := u.Scheme
        host := u.Hostname()
        p := u.Port()
        port := 0
        if p != "" { port, _ = strconv.Atoi(p) }
        if port == 0 {
            if proto == "https" { port = 443 } else { port = 80 }
        }
        path := u.EscapedPath()
        if svcPath != "" { // 允许通过 --path 覆盖 URL 中的路径
            if !strings.HasPrefix(svcPath, "/") { svcPath = "/" + svcPath }
            path = svcPath
        }

        // 自动 Upstream 名称
        upName := svcUpstream
        if upName == "" { upName = svcName + "-upstream" }
        target := host+":"+strconv.Itoa(port)

        // diff 显示
        cur, exists, err := client.GetService(ctx, svcName)
        if err != nil { return err }
        if autoUpstream {
            if showDiff {
                PrintInfo(cmd, "📝 Diff: Service")
                if !exists {
                    cmd.Printf("%s\n", colorInfo(fmt.Sprintf("+ service: %s (host=%s port=%d path=%s protocol=%s)", svcName, upName, port, path, proto)))
                    cmd.Printf("%s\n", colorInfo(fmt.Sprintf("+ upstream: %s", upName)))
                    cmd.Printf("%s\n", colorInfo(fmt.Sprintf("+ target: %s (weight=%d)", target, targetWeightIfSet())))
                } else {
                    cmd.Printf("%s\n", colorWarn(fmt.Sprintf("- service.host: %s", cur.Host)))
                    cmd.Printf("%s\n", colorInfo(fmt.Sprintf("+ service.host: %s", upName)))
                    cmd.Printf("%s\n", colorWarn(fmt.Sprintf("- service.protocol: %s", cur.Protocol)))
                    cmd.Printf("%s\n", colorInfo(fmt.Sprintf("+ service.protocol: %s", proto)))
                    cmd.Printf("%s\n", colorWarn(fmt.Sprintf("- service.port: %d", cur.Port)))
                    cmd.Printf("%s\n", colorInfo(fmt.Sprintf("+ service.port: %d", port)))
                    if strings.TrimSpace(cur.Path) != strings.TrimSpace(path) {
                        cmd.Printf("%s\n", colorWarn(fmt.Sprintf("- service.path: %s", cur.Path)))
                        cmd.Printf("%s\n", colorInfo(fmt.Sprintf("+ service.path: %s", path)))
                    }
                }
                if dryRun {
                    PrintInfo(cmd, "[dry-run] 将创建/更新 Upstream 与 Service：%s -> %s (%s)", svcName, upName, target)
                    return nil
                }
            }

            // 确保 Upstream 与 Target
            if _, _, err := client.CreateOrUpdateUpstream(ctx, upName); err != nil { return err }
            if _, err := client.EnsureTarget(ctx, upName, target, targetWeightIfSet()); err != nil { return err }

            // 绑定 Service 到 Upstream
            action, _, err := client.CreateOrUpdateServiceViaUpstream(ctx, svcName, upName, proto, port, path)
            if err != nil { return err }
            PrintSuccess(cmd, "已%sed Service：%s，关联 Upstream：%s（target=%s）", actionCN(action), svcName, upName, target)
            return nil
        }

        // 非自动 Upstream：使用 URL 直接同步 Service
        if showDiff {
            if !exists {
                PrintInfo(cmd, "📝 Diff: 新建 Service")
                cmd.Printf("%s\n", colorInfo(fmt.Sprintf("+ name: %s", svcName)))
                cmd.Printf("%s\n", colorInfo(fmt.Sprintf("+ url: %s", svcURL)))
            } else {
                curURL := reconstructURL(cur)
                if curURL == svcURL {
                    PrintInfo(cmd, "📝 Diff: 无字段变更")
                } else {
                    PrintInfo(cmd, "📝 Diff:")
                    cmd.Printf("%s\n", colorWarn(fmt.Sprintf("- url: %s", curURL)))
                    cmd.Printf("%s\n", colorInfo(fmt.Sprintf("+ url: %s", svcURL)))
                }
            }
            if dryRun {
                PrintInfo(cmd, "[dry-run] 将同步 Service：name=%s url=%s", svcName, svcURL)
                return nil
            }
        }
        action, _, err := client.CreateOrUpdateService(ctx, svcName, svcURL)
        if err != nil { return err }
        switch action {
        case "create":
            PrintSuccess(cmd, "已创建 Service：name=%s url=%s", svcName, svcURL)
        case "update":
            PrintSuccess(cmd, "已更新 Service：name=%s url=%s", svcName, svcURL)
        default:
            PrintSuccess(cmd, "已同步 Service：name=%s url=%s", svcName, svcURL)
        }
        return nil
    },
}

func init() {
    serviceCmd.AddCommand(serviceSyncCmd)
    serviceSyncCmd.Flags().StringVar(&svcName, "name", "", "Service 名称，例：echo 或 user")
    serviceSyncCmd.Flags().StringVar(&svcURL, "url", "", "上游 URL，例：http://httpbin.org 或 http://backend:8080")
    serviceSyncCmd.Flags().StringVar(&svcPath, "path", "", "上游基础路径（可覆盖 URL 中的路径），例：/api 或 v1；自动补前导 /")
    serviceSyncCmd.Flags().BoolVar(&dryRun, "dry-run", false, "仅显示计划，不实际变更，例：--dry-run --diff")
    serviceSyncCmd.Flags().BoolVar(&showDiff, "diff", false, "显示差异，例：--diff")
    autoUpstream = true
    serviceSyncCmd.Flags().BoolVar(&autoUpstream, "auto-upstream", autoUpstream, "自动创建 Upstream 并将 Service 指向它，例：--auto-upstream")
    serviceSyncCmd.Flags().StringVar(&svcUpstream, "upstream", "", "Upstream 名称（未提供则默认 name-upstream），例：--upstream user-up")
    serviceSyncCmd.Flags().IntVar(&targetWeight, "weight", 100, "首个 target 权重（默认 100），例：--weight 100")
}

func reconstructURL(s *kong.Service) string {
    if s == nil {
        return ""
    }
    proto := s.Protocol
    host := s.Host
    port := s.Port
    path := s.Path
    if proto == "" || host == "" {
        return ""
    }
    needPort := (proto == "http" && port != 80) || (proto == "https" && port != 443)
    url := fmt.Sprintf("%s://%s", proto, host)
    if needPort && port != 0 {
        url = fmt.Sprintf("%s:%d", url, port)
    }
    if path != "" {
        if path[0] != '/' { path = "/" + path }
        url += path
    }
    return url
}

func targetWeightIfSet() int {
    if targetWeight <= 0 { return 100 }
    return targetWeight
}
