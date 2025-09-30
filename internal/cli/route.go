package cli

import (
    "context"
    "fmt"
    "regexp"
    "sort"
    "strings"
    "time"

    "github.com/spf13/cobra"
    "github.com/spf13/viper"
    "kongctl/internal/kong"
)

var (
    routeService string
    routeName    string
    routePaths   []string
    routeMethods []string
    routeHosts   []string
    routePathHandling string
)

var routeCmd = &cobra.Command{
    Use:   "route",
    Short: "管理 Route 资源",
}

var routeSyncCmd = &cobra.Command{
    Use:   "sync",
    Short: "创建或更新 Route（幂等）",
    Example: `# 最小示例：为 user-service 挂载一个 GET 路由
kongctl route sync --service user-service --paths /v1/users --methods GET

# 增加 hosts 并显示差异
kongctl route sync --service user-service --paths /v1/ping --hosts api.example.com --diff

# 自动生成 route 名称（基于 service/paths/methods）
kongctl route sync --service user-service --paths /v1/orders --methods GET,POST

# 指定路径处理版本（v0/v1）
kongctl route sync --service user-service --paths /v1 --methods GET --path-handling v1 --diff`,
    RunE: func(cmd *cobra.Command, args []string) error {
        if routeService == "" || len(routePaths) == 0 {
            return fmt.Errorf("必须提供 --service 与 --paths")
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

        name := routeName
        if name == "" {
            name = defaultRouteName(routeService, routePaths, routeMethods)
        }
        // 规范化与校验 path-handling
        ph := strings.ToLower(strings.TrimSpace(routePathHandling))
        if ph != "" && ph != "v0" && ph != "v1" {
            return fmt.Errorf("--path-handling 仅支持 v0 或 v1：%s", routePathHandling)
        }

        desired := kong.Route{
            Name:    name,
            Hosts:   routeHosts,
            Paths:   routePaths,
            Methods: toUpper(routeMethods),
            PathHandling: ph,
        }
        sp := true
        desired.StripPath = &sp
        desired.Service.Name = routeService

        cur, exists, err := client.GetRoute(ctx, name)
        if err != nil {
            return err
        }
        if showDiff {
            if !exists {
                PrintInfo(cmd, "📝 Diff: 新建 Route %s", name)
            } else {
                PrintInfo(cmd, "📝 Diff:")
                cmd.Print(diffSlice("hosts", cur.Hosts, desired.Hosts))
                cmd.Print(diffSlice("paths", cur.Paths, desired.Paths))
                cmd.Print(diffSlice("methods", cur.Methods, desired.Methods))
                if ph != "" {
                    curPH := strings.ToLower(cur.PathHandling)
                    if curPH != ph {
                        cmd.Printf("path_handling: %s -> %s\n", curPH, ph)
                    } else {
                        cmd.Printf("path_handling: %s\n", colorInfo("无变更"))
                    }
                }
            }
            if dryRun {
                PrintInfo(cmd, "[dry-run] 将同步 Route：name=%s service=%s", name, routeService)
                return nil
            }
        }

        action, _, err := client.CreateOrUpdateRoute(ctx, desired)
        if err != nil {
            return err
        }
        PrintSuccess(cmd, "已%sed Route：name=%s service=%s", actionCN(action), name, routeService)
        return nil
    },
}

func init() {
    routeCmd.AddCommand(routeSyncCmd)
    routeSyncCmd.Flags().StringVar(&routeService, "service", "", "关联 Service 名称，例：--service user-service")
    routeSyncCmd.Flags().StringVar(&routeName, "name", "", "Route 名称（留空自动生成），例：--name user-list")
    routeSyncCmd.Flags().StringSliceVar(&routePaths, "paths", nil, "匹配路径，逗号分隔或多次传入，例：--paths /v1/ping,/v1/users")
    routeSyncCmd.Flags().StringSliceVar(&routeMethods, "methods", nil, "HTTP 方法列表，例：--methods GET,POST")
    routeSyncCmd.Flags().StringSliceVar(&routeHosts, "hosts", nil, "主机名列表，例：--hosts api.example.com")
    routeSyncCmd.Flags().StringVar(&routePathHandling, "path-handling", "", "路径匹配规则：v0 或 v1（默认沿用 Kong 端）")
    routeSyncCmd.Flags().BoolVar(&dryRun, "dry-run", false, "仅显示计划，不实际变更，例：--dry-run --diff")
    routeSyncCmd.Flags().BoolVar(&showDiff, "diff", false, "显示差异，例：--diff")
}

func toUpper(xs []string) []string {
    out := make([]string, 0, len(xs))
    for _, x := range xs {
        out = append(out, strings.ToUpper(x))
    }
    return out
}

func actionCN(a string) string {
    switch a {
    case "create":
        return "创建"
    case "update":
        return "更新"
    default:
        return "同步"
    }
}

var nonWord = regexp.MustCompile(`[^A-Za-z0-9]+`)

func defaultRouteName(service string, paths, methods []string) string {
    p := strings.Join(paths, "-")
    p = nonWord.ReplaceAllString(p, "-")
    for strings.HasPrefix(p, "-") { p = strings.TrimPrefix(p, "-") }
    for strings.HasSuffix(p, "-") { p = strings.TrimSuffix(p, "-") }
    m := toUpper(methods)
    sort.Strings(m)
    if len(m) == 0 { m = []string{"ANY"} }
    return fmt.Sprintf("%s-%s-%s", service, p, strings.Join(m, "+"))
}

func diffSlice(field string, cur, want []string) string {
    a := map[string]bool{}
    b := map[string]bool{}
    for _, x := range cur { a[x] = true }
    for _, x := range want { b[x] = true }
    var del, add []string
    for x := range a { if !b[x] { del = append(del, x) } }
    for x := range b { if !a[x] { add = append(add, x) } }
    sort.Strings(del); sort.Strings(add)
    if len(del)==0 && len(add)==0 { return fmt.Sprintf("%s: %s\n", field, colorInfo("无变更")) }
    var sb strings.Builder
    sb.WriteString(field+":\n")
    for _, x := range del { sb.WriteString(colorWarn("- "+x)+"\n") }
    for _, x := range add { sb.WriteString(colorSuccess("+ "+x)+"\n") }
    return sb.String()
}
