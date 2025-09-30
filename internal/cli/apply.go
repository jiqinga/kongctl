package cli

import (
    "context"
    "fmt"
    "os"
    "path/filepath"
    "strings"
    "time"

    "github.com/spf13/cobra"
    "github.com/spf13/viper"
    "gopkg.in/yaml.v3"
    aplan "kongctl/internal/apply"
    "kongctl/internal/kong"
)

// applySpec 定义通过文件批量创建的资源结构
type applySpec struct {
    Upstreams []applyUpstream `yaml:"upstreams" json:"upstreams"`
    Services  []applyService  `yaml:"services"  json:"services"`
    Routes    []applyRoute    `yaml:"routes"    json:"routes"`
}

type applyUpstream struct {
    Name    string         `yaml:"name" json:"name"`
    Targets []applyTarget  `yaml:"targets" json:"targets"`
}

type applyTarget struct {
    Target string `yaml:"target" json:"target"` // host:port
    Weight int    `yaml:"weight" json:"weight"`
}

type applyService struct {
    Name     string        `yaml:"name" json:"name"`
    URL      string        `yaml:"url" json:"url"`
    Upstream string        `yaml:"upstream" json:"upstream"`
    Protocol string        `yaml:"protocol" json:"protocol"`
    Port     int           `yaml:"port" json:"port"`
    Path     string        `yaml:"path" json:"path"`
    Retries        int     `yaml:"retries" json:"retries"`
    ConnectTimeout int     `yaml:"connect_timeout" json:"connect_timeout"`
    ReadTimeout    int     `yaml:"read_timeout" json:"read_timeout"`
    WriteTimeout   int     `yaml:"write_timeout" json:"write_timeout"`
    Targets  []applyTarget `yaml:"targets" json:"targets"` // 可选：便捷在此 service 的 upstream 下创建 targets
}

type applyRoute struct {
    Name      string   `yaml:"name" json:"name"`
    Service   string   `yaml:"service" json:"service"`
    Hosts     []string `yaml:"hosts" json:"hosts"`
    Paths     []string `yaml:"paths" json:"paths"`
    Methods   []string `yaml:"methods" json:"methods"`
    StripPath *bool    `yaml:"strip_path" json:"strip_path"`
    PathHandling string `yaml:"path_handling" json:"path_handling"`
    Protocols   []string            `yaml:"protocols" json:"protocols"`
    PreserveHost *bool              `yaml:"preserve_host" json:"preserve_host"`
    RegexPriority int               `yaml:"regex_priority" json:"regex_priority"`
    HTTPSRedirectStatusCode int     `yaml:"https_redirect_status_code" json:"https_redirect_status_code"`
    RequestBuffering *bool          `yaml:"request_buffering" json:"request_buffering"`
    ResponseBuffering *bool         `yaml:"response_buffering" json:"response_buffering"`
    Headers map[string][]string     `yaml:"headers" json:"headers"`
    Snis    []string                `yaml:"snis" json:"snis"`
    Tags    []string                `yaml:"tags" json:"tags"`
    // 简写支持：仅给出 route 时，自动创建同名前缀的 service/upstream
    ServiceName  string        `yaml:"service_name" json:"service_name"`
    UpstreamName string        `yaml:"upstream_name" json:"upstream_name"`
    Backend      routeBackend  `yaml:"backend" json:"backend"`
}

type routeBackend struct {
    Protocol string        `yaml:"protocol" json:"protocol"`
    Port     int           `yaml:"port" json:"port"`
    Path     string        `yaml:"path" json:"path"`
    Targets  []applyTarget `yaml:"targets" json:"targets"`
}

// autoRouteInfo 用于记录 route 简写自动生成的 service/upstream 信息
type autoRouteInfo struct {
    RouteName    string
    ServiceName  string
    UpstreamName string
    Targets      []applyTarget
}

// sliceSetEqual 判断两个字符串切片（作为集合）是否相等
func sliceSetEqual(a, b []string) bool {
    if len(a) != len(b) { return false }
    m := map[string]int{}
    for _, x := range a { m[x]++ }
    for _, x := range b {
        if m[x] == 0 { return false }
        m[x]--
    }
    for _, v := range m { if v != 0 { return false } }
    return true
}

// mapStringSliceEqual 比较 map[string][]string （值作为集合，不计顺序/重复）
func mapStringSliceEqual(a, b map[string][]string) bool {
    if len(a) != len(b) { return false }
    for k, va := range a {
        vb, ok := b[k]
        if !ok { return false }
        if !sliceSetEqual(va, vb) { return false }
    }
    return true
}

// diffMapStringSlice 生成差异文本（简单展示键级对比与新增/删除）
func diffMapStringSlice(field string, cur, want map[string][]string) string {
    var sb strings.Builder
    sb.WriteString(field+":\n")
    // 删除的键
    for k := range cur {
        if _, ok := want[k]; !ok {
            sb.WriteString(colorWarn("- "+k)+"\n")
        }
    }
    // 新增或变更
    for k := range want {
        vcur, ok := cur[k]
        if !ok {
            sb.WriteString(colorSuccess("+ "+k+": "+strings.Join(want[k], ", "))+"\n")
            continue
        }
        if !sliceSetEqual(vcur, want[k]) {
            sb.WriteString(colorWarn("- "+k+": "+strings.Join(vcur, ", "))+"\n")
            sb.WriteString(colorSuccess("+ "+k+": "+strings.Join(want[k], ", "))+"\n")
        }
    }
    return sb.String()
}

var (
    applyFile    string
    applyNoColor bool
    applyASCII   bool
    applyCompact bool
    applyOverwrite bool
)

var applyCmd = &cobra.Command{
    Use:   "apply",
    Short: "从文件批量创建/更新 Route、Service、Upstream 等",
    Long:  "从 YAML/JSON 文件读取定义，幂等创建/更新 Upstream、Target、Service、Route 等资源。",
    Example: `# 完整写法（含 upstream / services / routes）
kongctl apply -f examples/apply.yaml

# 简写：顶层为 routes 列表，仅定义路由并自动创建 service/upstream
kongctl apply -f examples/route-simple.yaml

# 预览计划（彩色、分层显示），并显示字段级差异
kongctl apply -f examples/route-simple.yaml --dry-run --diff

# 使用 ASCII 与紧凑模式（隐藏无变化项）
kongctl apply -f examples/route-simple.yaml --dry-run --ascii --compact`,
    RunE: func(cmd *cobra.Command, args []string) error {
        if applyFile == "" {
            return fmt.Errorf("必须通过 -f/--file 指定配置文件")
        }

        content, err := os.ReadFile(applyFile)
        if err != nil {
            return fmt.Errorf("读取文件失败：%w", err)
        }

        // 支持三种顶层结构：
        // 1) 对象：{upstreams/services/routes}
        // 2) 列表：[...] 视为 routes 简写
        // 3) 单对象：{name, paths, ...} 视为单个 route 简写
        var spec applySpec
        errTop := yaml.Unmarshal(content, &spec)
        if errTop != nil || (len(spec.Upstreams) == 0 && len(spec.Services) == 0 && len(spec.Routes) == 0) {
            // 尝试以 routes 列表解析
            var routes []applyRoute
            if errList := yaml.Unmarshal(content, &routes); errList == nil && len(routes) > 0 {
                spec.Routes = routes
            } else {
                // 尝试以单个 route 解析
                var r applyRoute
                if errOne := yaml.Unmarshal(content, &r); errOne == nil && (r.Name != "" || len(r.Paths) > 0 || len(r.Hosts) > 0 || len(r.Methods) > 0 || r.Service != "" || len(r.Backend.Targets) > 0 || r.Backend.Protocol != "" || r.Backend.Port != 0 || r.Backend.Path != "") {
                    spec.Routes = []applyRoute{r}
                } else if errTop != nil {
                    return fmt.Errorf("解析文件失败（支持 YAML/JSON）。可提供顶层对象 {routes: [...]}，或直接提供 route 列表/单个 route。原始错误：%w", errTop)
                } else {
                    return fmt.Errorf("配置为空或未识别到任何资源，请提供 routes/ services/ upstreams 或使用简写列表")
                }
            }
        }

        cfg := kong.Config{
            AdminURL:      viper.GetString("admin_url"),
            Token:         viper.GetString("token"),
            TLSSkipVerify: viper.GetBool("tls_skip_verify"),
            Timeout:       15 * time.Second,
        }
        if cfg.AdminURL == "" {
            return fmt.Errorf("请通过 --admin-url 或 KONGCTL_ADMIN_URL 指定 Admin API 地址；或运行 'kongctl init --admin-url <url>' 持久化配置")
        }
        client := kong.NewClient(cfg)
        ctx, cancel := context.WithTimeout(cmd.Context(), cfg.Timeout)
        defer cancel()
        var plan aplan.Plan

        // 1) Upstreams + Targets
        for _, up := range spec.Upstreams {
            if up.Name == "" { return fmt.Errorf("upstreams[].name 不能为空") }
            if dryRun {
                if _, ok, err := client.GetUpstream(ctx, up.Name); err == nil {
                    act := "create"; if ok { act = "none" }
                    plan.Items = append(plan.Items, aplan.Change{Kind: "Upstream", Name: up.Name, Action: act})
                } else {
                    plan.Items = append(plan.Items, aplan.Change{Kind: "Upstream", Name: up.Name, Action: "create"})
                }
            } else if showDiff {
                PrintInfo(cmd, "确保 Upstream：%s", up.Name)
            }
            if !dryRun {
                // 仅在不存在时创建；存在则不覆盖配置
                if _, ok, err := client.GetUpstream(ctx, up.Name); err != nil {
                    return err
                } else if !ok {
                    if _, _, err := client.CreateOrUpdateUpstream(ctx, up.Name); err != nil { return err }
                } else if applyOverwrite {
                    // 当前 Upstream 没有可变更字段，CreateOrUpdateUpstream 也不会修改现有可配置项；
                    // 若未来扩展需要 PATCH，可在此处启用覆盖。
                    if _, _, err := client.CreateOrUpdateUpstream(ctx, up.Name); err != nil { return err }
                }
            }
            for _, t := range up.Targets {
                w := t.Weight
                if w == 0 { w = 100 }
                if dryRun {
                    if list, err := client.ListTargets(ctx, up.Name); err == nil {
                        action := "create"
                        for i := range list {
                            if list[i].Target == t.Target && (list[i].Weight == w) { action = "none"; break }
                        }
                        plan.Items = append(plan.Items, aplan.Change{Kind: "Target", Name: up.Name+"/"+t.Target, Action: action})
                    } else {
                        plan.Items = append(plan.Items, aplan.Change{Kind: "Target", Name: up.Name+"/"+t.Target, Action: "create"})
                    }
                } else if showDiff {
                    PrintInfo(cmd, "确保 Target：%s (weight=%d) -> %s", t.Target, w, up.Name)
                }
                if !dryRun {
                    // 若已存在且权重不同，视为覆盖更新：默认跳过，除非启用 --overwrite
                    list, err := client.ListTargets(ctx, up.Name)
                    if err != nil { return err }
                    exists := false
                    sameWeight := false
                    for i := range list {
                        if list[i].Target == t.Target {
                            exists = true
                            if list[i].Weight == w || w == 0 { sameWeight = true }
                            break
                        }
                    }
                    if !exists {
                        if _, err := client.EnsureTarget(ctx, up.Name, t.Target, w); err != nil { return err }
                    } else if sameWeight {
                        // no-op
                    } else if applyOverwrite {
                        if _, err := client.EnsureTarget(ctx, up.Name, t.Target, w); err != nil { return err }
                    } else {
                        PrintWarn(cmd, "已存在 Target：%s，检测到权重变更（将跳过，启用 --overwrite 可覆盖）", t.Target)
                    }
                }
            }
        }

        // 2) Services（可直接 URL，或通过 upstream+protocol/port/path）
        for _, s := range spec.Services {
            if s.Name == "" { return fmt.Errorf("services[].name 不能为空") }
            if s.Upstream != "" {
                // 先确保 upstream
                if dryRun {
                    if _, ok, err := client.GetUpstream(ctx, s.Upstream); err == nil {
                        act := "create"; if ok { act = "none" }
                        plan.Items = append(plan.Items, aplan.Change{Kind: "Upstream", Name: s.Upstream, Action: act})
                    } else {
                        plan.Items = append(plan.Items, aplan.Change{Kind: "Upstream", Name: s.Upstream, Action: "create"})
                    }
                } else if showDiff {
                    PrintInfo(cmd, "确保 Upstream：%s（service=%s）", s.Upstream, s.Name)
                }
                if !dryRun {
                    if _, ok, err := client.GetUpstream(ctx, s.Upstream); err != nil { return err } else if !ok {
                        if _, _, err := client.CreateOrUpdateUpstream(ctx, s.Upstream); err != nil { return err }
                    } else if applyOverwrite {
                        if _, _, err := client.CreateOrUpdateUpstream(ctx, s.Upstream); err != nil { return err }
                    }
                }
                // 若 service 节点中包含 targets，则在该 upstream 下确保
                for _, t := range s.Targets {
                    w := t.Weight; if w == 0 { w = 100 }
                    if dryRun {
                        if list, err := client.ListTargets(ctx, s.Upstream); err == nil {
                            action := "create"
                            for i := range list { if list[i].Target == t.Target && list[i].Weight == w { action = "none"; break } }
                            plan.Items = append(plan.Items, aplan.Change{Kind: "Target", Name: s.Upstream+"/"+t.Target, Action: action})
                        } else {
                            plan.Items = append(plan.Items, aplan.Change{Kind: "Target", Name: s.Upstream+"/"+t.Target, Action: "create"})
                        }
                    } else if showDiff {
                        PrintInfo(cmd, "确保 Target：%s (weight=%d) -> %s", t.Target, w, s.Upstream)
                    }
                    if !dryRun {
                        list, err := client.ListTargets(ctx, s.Upstream)
                        if err != nil { return err }
                        exists := false
                        sameWeight := false
                        for i := range list { if list[i].Target == t.Target { exists = true; if list[i].Weight == w || w == 0 { sameWeight = true }; break } }
                        if !exists {
                            if _, err := client.EnsureTarget(ctx, s.Upstream, t.Target, w); err != nil { return err }
                        } else if sameWeight {
                            // no-op
                        } else if applyOverwrite {
                            if _, err := client.EnsureTarget(ctx, s.Upstream, t.Target, w); err != nil { return err }
                        } else {
                            PrintWarn(cmd, "已存在 Target：%s，检测到权重变更（将跳过，启用 --overwrite 可覆盖）", t.Target)
                        }
                    }
                }
                // 应用 Service
                proto := s.Protocol
                if proto == "" { proto = "http" }
                port := s.Port
                if port == 0 {
                    if proto == "https" { port = 443 } else { port = 80 }
                }
                if dryRun {
                    if cur, ok, err := client.GetService(ctx, s.Name); err == nil {
                        action := "create"
                        if ok {
                            action = "none"
                            if cur.Host != s.Upstream || cur.Protocol != proto || cur.Port != port || (cur.Path != s.Path) {
                                action = "update"
                            }
                            if s.Retries > 0 && cur.Retries != s.Retries { action = "update" }
                            if s.ConnectTimeout > 0 && cur.ConnectTimeout != s.ConnectTimeout { action = "update" }
                            if s.ReadTimeout > 0 && cur.ReadTimeout != s.ReadTimeout { action = "update" }
                            if s.WriteTimeout > 0 && cur.WriteTimeout != s.WriteTimeout { action = "update" }
                        }
                        diff := ""
                        if ok {
                            if cur.Host != s.Upstream { diff += fmt.Sprintf("host: %s -> %s\n", cur.Host, s.Upstream) }
                            if cur.Protocol != proto { diff += fmt.Sprintf("protocol: %s -> %s\n", cur.Protocol, proto) }
                            if cur.Port != port { diff += fmt.Sprintf("port: %d -> %d\n", cur.Port, port) }
                            if cur.Path != s.Path { diff += fmt.Sprintf("path: %s -> %s\n", cur.Path, s.Path) }
                            if s.Retries > 0 && cur.Retries != s.Retries { diff += fmt.Sprintf("retries: %d -> %d\n", cur.Retries, s.Retries) }
                            if s.ConnectTimeout > 0 && cur.ConnectTimeout != s.ConnectTimeout { diff += fmt.Sprintf("connect_timeout: %d -> %d\n", cur.ConnectTimeout, s.ConnectTimeout) }
                            if s.ReadTimeout > 0 && cur.ReadTimeout != s.ReadTimeout { diff += fmt.Sprintf("read_timeout: %d -> %d\n", cur.ReadTimeout, s.ReadTimeout) }
                            if s.WriteTimeout > 0 && cur.WriteTimeout != s.WriteTimeout { diff += fmt.Sprintf("write_timeout: %d -> %d\n", cur.WriteTimeout, s.WriteTimeout) }
                        }
                        plan.Items = append(plan.Items, aplan.Change{Kind: "Service", Name: s.Name, Action: action, Diff: diff})
                    } else {
                        plan.Items = append(plan.Items, aplan.Change{Kind: "Service", Name: s.Name, Action: "create"})
                    }
                } else if showDiff {
                    PrintInfo(cmd, "同步 Service：%s -> upstream=%s (%s:%d path=%s)", s.Name, s.Upstream, proto, port, s.Path)
                }
                if !dryRun {
                    // 仅在不存在时创建；若存在且有差异，需 --overwrite 才更新
                    if cur, ok, err := client.GetService(ctx, s.Name); err != nil { return err } else if !ok {
                        action, _, err := client.CreateOrUpdateServiceViaUpstream(ctx, s.Name, s.Upstream, proto, port, s.Path)
                        if err != nil { return err }
                        PrintSuccess(cmd, "已%sed Service：%s（upstream=%s）", actionCN(action), s.Name, s.Upstream)
                        // 新建后若指定了扩展字段，则补丁更新
                        if s.Retries > 0 || s.ConnectTimeout > 0 || s.ReadTimeout > 0 || s.WriteTimeout > 0 {
                            if _, err := client.UpdateServiceExtras(ctx, s.Name, s.Retries, s.ConnectTimeout, s.ReadTimeout, s.WriteTimeout); err != nil { return err }
                        }
                    } else {
                        changed := cur.Host != s.Upstream || cur.Protocol != proto || cur.Port != port || (cur.Path != s.Path)
                        // 扩展字段差异
                        extrasChanged := (s.Retries > 0 && cur.Retries != s.Retries) ||
                            (s.ConnectTimeout > 0 && cur.ConnectTimeout != s.ConnectTimeout) ||
                            (s.ReadTimeout > 0 && cur.ReadTimeout != s.ReadTimeout) ||
                            (s.WriteTimeout > 0 && cur.WriteTimeout != s.WriteTimeout)
                        if changed {
                            if applyOverwrite {
                                action, _, err := client.CreateOrUpdateServiceViaUpstream(ctx, s.Name, s.Upstream, proto, port, s.Path)
                                if err != nil { return err }
                                PrintSuccess(cmd, "已%sed Service：%s（upstream=%s）", actionCN(action), s.Name, s.Upstream)
                            } else {
                                PrintWarn(cmd, "检测到 Service 变更但未启用覆盖：%s（跳过，使用 --overwrite 应用变更）", s.Name)
                            }
                        }
                        if extrasChanged {
                            if applyOverwrite {
                                if _, err := client.UpdateServiceExtras(ctx, s.Name, s.Retries, s.ConnectTimeout, s.ReadTimeout, s.WriteTimeout); err != nil { return err }
                                PrintSuccess(cmd, "已更新 Service 额外参数：%s", s.Name)
                            } else {
                                PrintWarn(cmd, "检测到 Service 额外参数变更但未启用覆盖：%s（跳过，使用 --overwrite 应用变更）", s.Name)
                            }
                        }
                    }
                }
                continue
            }
            // 通过 URL
            if s.URL == "" {
                return fmt.Errorf("services[%s] 需要提供 url 或 upstream", s.Name)
            }
            if dryRun {
                if cur, ok, err := client.GetService(ctx, s.Name); err == nil {
                    action := "create"
                    diff := ""
                    if ok {
                        action = "none"
                        curURL := reconstructURL(cur)
                        if curURL != s.URL { action = "update"; diff = fmt.Sprintf("url: %s -> %s\n", curURL, s.URL) }
                        if s.Retries > 0 && cur.Retries != s.Retries { action = "update"; diff += fmt.Sprintf("retries: %d -> %d\n", cur.Retries, s.Retries) }
                        if s.ConnectTimeout > 0 && cur.ConnectTimeout != s.ConnectTimeout { action = "update"; diff += fmt.Sprintf("connect_timeout: %d -> %d\n", cur.ConnectTimeout, s.ConnectTimeout) }
                        if s.ReadTimeout > 0 && cur.ReadTimeout != s.ReadTimeout { action = "update"; diff += fmt.Sprintf("read_timeout: %d -> %d\n", cur.ReadTimeout, s.ReadTimeout) }
                        if s.WriteTimeout > 0 && cur.WriteTimeout != s.WriteTimeout { action = "update"; diff += fmt.Sprintf("write_timeout: %d -> %d\n", cur.WriteTimeout, s.WriteTimeout) }
                    }
                    plan.Items = append(plan.Items, aplan.Change{Kind: "Service", Name: s.Name, Action: action, Diff: diff})
                } else {
                    plan.Items = append(plan.Items, aplan.Change{Kind: "Service", Name: s.Name, Action: "create"})
                }
            } else if showDiff {
                PrintInfo(cmd, "同步 Service：name=%s url=%s", s.Name, s.URL)
            }
            if !dryRun {
                if cur, ok, err := client.GetService(ctx, s.Name); err != nil { return err } else if !ok {
                    action, _, err := client.CreateOrUpdateService(ctx, s.Name, s.URL)
                    if err != nil { return err }
                    if action == "create" {
                        PrintSuccess(cmd, "已创建 Service：name=%s", s.Name)
                    } else {
                        PrintSuccess(cmd, "已更新 Service：name=%s", s.Name)
                    }
                    // 新建后若指定了扩展字段，则补丁更新
                    if s.Retries > 0 || s.ConnectTimeout > 0 || s.ReadTimeout > 0 || s.WriteTimeout > 0 {
                        if _, err := client.UpdateServiceExtras(ctx, s.Name, s.Retries, s.ConnectTimeout, s.ReadTimeout, s.WriteTimeout); err != nil { return err }
                    }
                } else {
                    curURL := reconstructURL(cur)
                    extrasChanged := (s.Retries > 0 && cur.Retries != s.Retries) ||
                        (s.ConnectTimeout > 0 && cur.ConnectTimeout != s.ConnectTimeout) ||
                        (s.ReadTimeout > 0 && cur.ReadTimeout != s.ReadTimeout) ||
                        (s.WriteTimeout > 0 && cur.WriteTimeout != s.WriteTimeout)
                    if curURL != s.URL {
                        if applyOverwrite {
                            action, _, err := client.CreateOrUpdateService(ctx, s.Name, s.URL)
                            if err != nil { return err }
                            if action == "create" {
                                PrintSuccess(cmd, "已创建 Service：name=%s", s.Name)
                            } else {
                                PrintSuccess(cmd, "已更新 Service：name=%s", s.Name)
                            }
                        } else {
                            PrintWarn(cmd, "检测到 Service URL 变更但未启用覆盖：%s（跳过，使用 --overwrite 应用变更）", s.Name)
                        }
                    }
                    if extrasChanged {
                        if applyOverwrite {
                            if _, err := client.UpdateServiceExtras(ctx, s.Name, s.Retries, s.ConnectTimeout, s.ReadTimeout, s.WriteTimeout); err != nil { return err }
                            PrintSuccess(cmd, "已更新 Service 额外参数：%s", s.Name)
                        } else {
                            PrintWarn(cmd, "检测到 Service 额外参数变更但未启用覆盖：%s（跳过，使用 --overwrite 应用变更）", s.Name)
                        }
                    }
                }
            }
        }

        // 记录由 route 简写自动生成的名字，用于层级展示时避免在顶层重复
        autoSvcSet := map[string]bool{}
        autoUpSet := map[string]bool{}

        // 3) Routes（支持简写：缺省 service 时，自动创建 service/upstream）
        var autoInfos []autoRouteInfo
        for _, r := range spec.Routes {
            // 计算最终的 route 名称
            name := r.Name
            // 若缺省 route 名称且提供了 service，则按原规则生成
            if name == "" && r.Service != "" { name = defaultRouteName(r.Service, r.Paths, r.Methods) }

            // 简写路径：未显式给出 service 时，自动创建 service/upstream
            if r.Service == "" {
                if name == "" {
                    return fmt.Errorf("route 未提供 name，且缺少 service，无法推导")
                }
                svcName := r.ServiceName
                if svcName == "" { svcName = name + "-service" }
                upName := r.UpstreamName
                if upName == "" { upName = name + "-upstream" }
                autoSvcSet[svcName] = true
                autoUpSet[upName] = true
                autoInfos = append(autoInfos, autoRouteInfo{RouteName: name, ServiceName: svcName, UpstreamName: upName, Targets: r.Backend.Targets})

                // 先确保 upstream 与 targets
                if dryRun {
                    if _, ok, err := client.GetUpstream(ctx, upName); err == nil {
                        act := "create"; if ok { act = "none" }
                        plan.Items = append(plan.Items, aplan.Change{Kind: "Upstream", Name: upName, Action: act})
                    } else {
                        plan.Items = append(plan.Items, aplan.Change{Kind: "Upstream", Name: upName, Action: "create"})
                    }
                } else if showDiff {
                PrintInfo(cmd, "确保 Upstream：%s（route=%s 简写）", upName, name)
                }
                if !dryRun {
                    if _, ok, err := client.GetUpstream(ctx, upName); err != nil { return err } else if !ok {
                        if _, _, err := client.CreateOrUpdateUpstream(ctx, upName); err != nil { return err }
                    } else if applyOverwrite {
                        if _, _, err := client.CreateOrUpdateUpstream(ctx, upName); err != nil { return err }
                    }
                }
                for _, t := range r.Backend.Targets {
                    w := t.Weight; if w == 0 { w = 100 }
                    if dryRun {
                        if list, err := client.ListTargets(ctx, upName); err == nil {
                            action := "create"
                            for i := range list { if list[i].Target == t.Target && list[i].Weight == w { action = "none"; break } }
                            plan.Items = append(plan.Items, aplan.Change{Kind: "Target", Name: upName+"/"+t.Target, Action: action})
                        } else {
                            plan.Items = append(plan.Items, aplan.Change{Kind: "Target", Name: upName+"/"+t.Target, Action: "create"})
                        }
                    } else if showDiff {
                        PrintInfo(cmd, "确保 Target：%s (weight=%d) -> %s", t.Target, w, upName)
                    }
                    if !dryRun {
                        list, err := client.ListTargets(ctx, upName)
                        if err != nil { return err }
                        exists := false
                        sameWeight := false
                        for i := range list { if list[i].Target == t.Target { exists = true; if list[i].Weight == w || w == 0 { sameWeight = true }; break } }
                        if !exists {
                            if _, err := client.EnsureTarget(ctx, upName, t.Target, w); err != nil { return err }
                        } else if sameWeight {
                            // no-op
                        } else if applyOverwrite {
                            if _, err := client.EnsureTarget(ctx, upName, t.Target, w); err != nil { return err }
                        } else {
                            PrintWarn(cmd, "已存在 Target：%s，检测到权重变更（将跳过，启用 --overwrite 可覆盖）", t.Target)
                        }
                    }
                }

                // 再创建/更新 service 指向该 upstream
                proto := r.Backend.Protocol; if proto == "" { proto = "http" }
                port := r.Backend.Port; if port == 0 { if proto == "https" { port = 443 } else { port = 80 } }
                path := r.Backend.Path

                if dryRun {
                    if cur, ok, err := client.GetService(ctx, svcName); err == nil {
                        action := "create"
                        if ok {
                            action = "none"
                            if cur.Host != upName || cur.Protocol != proto || cur.Port != port || (cur.Path != path) {
                                action = "update"
                            }
                        }
                        diff := ""
                        if ok {
                            if cur.Host != upName { diff += fmt.Sprintf("host: %s -> %s\n", cur.Host, upName) }
                            if cur.Protocol != proto { diff += fmt.Sprintf("protocol: %s -> %s\n", cur.Protocol, proto) }
                            if cur.Port != port { diff += fmt.Sprintf("port: %d -> %d\n", cur.Port, port) }
                            if cur.Path != path { diff += fmt.Sprintf("path: %s -> %s\n", cur.Path, path) }
                        }
                        plan.Items = append(plan.Items, aplan.Change{Kind: "Service", Name: svcName, Action: action, Diff: diff})
                    } else {
                        plan.Items = append(plan.Items, aplan.Change{Kind: "Service", Name: svcName, Action: "create"})
                    }
                } else if showDiff {
                    PrintInfo(cmd, "同步 Service：%s -> upstream=%s (%s:%d path=%s)", svcName, upName, proto, port, path)
                }
                if !dryRun {
                    if cur, ok, err := client.GetService(ctx, svcName); err != nil { return err } else if !ok {
                        action, _, err := client.CreateOrUpdateServiceViaUpstream(ctx, svcName, upName, proto, port, path)
                        if err != nil { return err }
                        PrintSuccess(cmd, "已%sed Service：%s（auto, upstream=%s）", actionCN(action), svcName, upName)
                    } else {
                        changed := cur.Host != upName || cur.Protocol != proto || cur.Port != port || (cur.Path != path)
                        if changed {
                            if applyOverwrite {
                                action, _, err := client.CreateOrUpdateServiceViaUpstream(ctx, svcName, upName, proto, port, path)
                                if err != nil { return err }
                                PrintSuccess(cmd, "已%sed Service：%s（auto, upstream=%s）", actionCN(action), svcName, upName)
                            } else {
                                PrintWarn(cmd, "检测到 Service 变更但未启用覆盖：%s（跳过，使用 --overwrite 应用变更）", svcName)
                            }
                        }
                    }
                }

                // 最终 route 仍然需要 service 名称
                r.Service = svcName
            }

            // 常规 route 同步
            if name == "" { name = defaultRouteName(r.Service, r.Paths, r.Methods) }
            // 校验 path_handling（若提供）
            ph := strings.ToLower(strings.TrimSpace(r.PathHandling))
            if ph != "" && ph != "v0" && ph != "v1" {
                return fmt.Errorf("routes[].path_handling 仅支持 v0 或 v1：%s", r.PathHandling)
            }

            desired := kong.Route{
                Name:    name,
                Hosts:   r.Hosts,
                Paths:   r.Paths,
                Methods: toUpper(r.Methods),
                PathHandling: ph,
            }
            if len(r.Protocols) > 0 { desired.Protocols = r.Protocols }
            if r.PreserveHost != nil { desired.PreserveHost = r.PreserveHost }
            if r.RegexPriority != 0 { desired.RegexPriority = r.RegexPriority }
            if r.HTTPSRedirectStatusCode != 0 { desired.HTTPSRedirectStatusCode = r.HTTPSRedirectStatusCode }
            if r.RequestBuffering != nil { desired.RequestBuffering = r.RequestBuffering }
            if r.ResponseBuffering != nil { desired.ResponseBuffering = r.ResponseBuffering }
            if len(r.Headers) > 0 { desired.Headers = r.Headers }
            if len(r.Snis) > 0 { desired.Snis = r.Snis }
            if len(r.Tags) > 0 { desired.Tags = r.Tags }
            if r.StripPath != nil { desired.StripPath = r.StripPath } else { sp := true; desired.StripPath = &sp }
            desired.Service.Name = r.Service

            if dryRun {
                if cur, ok, err := client.GetRoute(ctx, name); err == nil {
                    action := "create"
                    diff := ""
                    if ok {
                        action = "none"
                        changed := false
                        if !sliceSetEqual(cur.Hosts, desired.Hosts) { changed = true; diff += diffSlice("hosts", cur.Hosts, desired.Hosts) }
                        if !sliceSetEqual(cur.Paths, desired.Paths) { changed = true; diff += diffSlice("paths", cur.Paths, desired.Paths) }
                        if !sliceSetEqual(toUpper(cur.Methods), desired.Methods) { changed = true; diff += diffSlice("methods", cur.Methods, desired.Methods) }
                        if len(r.Protocols) > 0 {
                            if !sliceSetEqual(cur.Protocols, desired.Protocols) { changed = true; diff += diffSlice("protocols", cur.Protocols, desired.Protocols) }
                        }
                        curPH := strings.ToLower(cur.PathHandling)
                        desPH := strings.ToLower(desired.PathHandling)
                        if desPH != "" && curPH != desPH { changed = true; diff += fmt.Sprintf("path_handling: %s -> %s\n", curPH, desPH) }
                        if r.PreserveHost != nil {
                            curPHo := false; if cur.PreserveHost != nil { curPHo = *cur.PreserveHost }
                            desPHo := false; if desired.PreserveHost != nil { desPHo = *desired.PreserveHost }
                            if curPHo != desPHo { changed = true; diff += fmt.Sprintf("preserve_host: %v -> %v\n", curPHo, desPHo) }
                        }
                        if r.RegexPriority != 0 {
                            if cur.RegexPriority != desired.RegexPriority { changed = true; diff += fmt.Sprintf("regex_priority: %d -> %d\n", cur.RegexPriority, desired.RegexPriority) }
                        }
                        if r.HTTPSRedirectStatusCode != 0 {
                            if cur.HTTPSRedirectStatusCode != desired.HTTPSRedirectStatusCode { changed = true; diff += fmt.Sprintf("https_redirect_status_code: %d -> %d\n", cur.HTTPSRedirectStatusCode, desired.HTTPSRedirectStatusCode) }
                        }
                        if r.RequestBuffering != nil {
                            curRB := false; if cur.RequestBuffering != nil { curRB = *cur.RequestBuffering }
                            desRB := false; if desired.RequestBuffering != nil { desRB = *desired.RequestBuffering }
                            if curRB != desRB { changed = true; diff += fmt.Sprintf("request_buffering: %v -> %v\n", curRB, desRB) }
                        }
                        if r.ResponseBuffering != nil {
                            curRB := false; if cur.ResponseBuffering != nil { curRB = *cur.ResponseBuffering }
                            desRB := false; if desired.ResponseBuffering != nil { desRB = *desired.ResponseBuffering }
                            if curRB != desRB { changed = true; diff += fmt.Sprintf("response_buffering: %v -> %v\n", curRB, desRB) }
                        }
                        if len(r.Headers) > 0 {
                            if !mapStringSliceEqual(cur.Headers, desired.Headers) { changed = true; diff += diffMapStringSlice("headers", cur.Headers, desired.Headers) }
                        }
                        if len(r.Snis) > 0 {
                            if !sliceSetEqual(cur.Snis, desired.Snis) { changed = true; diff += diffSlice("snis", cur.Snis, desired.Snis) }
                        }
                        if len(r.Tags) > 0 {
                            if !sliceSetEqual(cur.Tags, desired.Tags) { changed = true; diff += diffSlice("tags", cur.Tags, desired.Tags) }
                        }
                        curSP := false; if cur.StripPath != nil { curSP = *cur.StripPath }
                        desSP := false; if desired.StripPath != nil { desSP = *desired.StripPath }
                        if curSP != desSP { changed = true; diff += fmt.Sprintf("strip_path: %v -> %v\n", curSP, desSP) }
                        if cur.Service.Name != desired.Service.Name && desired.Service.Name != "" { changed = true; diff += fmt.Sprintf("service: %s -> %s\n", cur.Service.Name, desired.Service.Name) }
                        if changed { action = "update" }
                    }
                    plan.Items = append(plan.Items, aplan.Change{Kind: "Route", Name: name, Action: action, Diff: diff})
                } else {
                    plan.Items = append(plan.Items, aplan.Change{Kind: "Route", Name: name, Action: "create"})
                }
            } else if showDiff {
                PrintInfo(cmd, "同步 Route：name=%s service=%s", name, r.Service)
            }
            if !dryRun {
                if cur, ok, err := client.GetRoute(ctx, name); err != nil { return err } else if !ok {
                    action, _, err := client.CreateOrUpdateRoute(ctx, desired)
                    if err != nil { return err }
                    PrintSuccess(cmd, "已%sed Route：name=%s service=%s", actionCN(action), name, r.Service)
                } else {
                    // 计算是否变更
                    changed := false
                    if !sliceSetEqual(cur.Hosts, desired.Hosts) { changed = true }
                    if !sliceSetEqual(cur.Paths, desired.Paths) { changed = true }
                    if !sliceSetEqual(toUpper(cur.Methods), desired.Methods) { changed = true }
                    if len(r.Protocols) > 0 && !sliceSetEqual(cur.Protocols, desired.Protocols) { changed = true }
                    curPH := strings.ToLower(cur.PathHandling)
                    desPH := strings.ToLower(desired.PathHandling)
                    if desPH != "" && curPH != desPH { changed = true }
                    if r.PreserveHost != nil {
                        curPHo := false; if cur.PreserveHost != nil { curPHo = *cur.PreserveHost }
                        desPHo := false; if desired.PreserveHost != nil { desPHo = *desired.PreserveHost }
                        if curPHo != desPHo { changed = true }
                    }
                    if r.RegexPriority != 0 && cur.RegexPriority != desired.RegexPriority { changed = true }
                    if r.HTTPSRedirectStatusCode != 0 && cur.HTTPSRedirectStatusCode != desired.HTTPSRedirectStatusCode { changed = true }
                    if r.RequestBuffering != nil {
                        curRB := false; if cur.RequestBuffering != nil { curRB = *cur.RequestBuffering }
                        desRB := false; if desired.RequestBuffering != nil { desRB = *desired.RequestBuffering }
                        if curRB != desRB { changed = true }
                    }
                    if r.ResponseBuffering != nil {
                        curRB := false; if cur.ResponseBuffering != nil { curRB = *cur.ResponseBuffering }
                        desRB := false; if desired.ResponseBuffering != nil { desRB = *desired.ResponseBuffering }
                        if curRB != desRB { changed = true }
                    }
                    if len(r.Headers) > 0 && !mapStringSliceEqual(cur.Headers, desired.Headers) { changed = true }
                    if len(r.Snis) > 0 && !sliceSetEqual(cur.Snis, desired.Snis) { changed = true }
                    if len(r.Tags) > 0 && !sliceSetEqual(cur.Tags, desired.Tags) { changed = true }
                    curSP := false; if cur.StripPath != nil { curSP = *cur.StripPath }
                    desSP := false; if desired.StripPath != nil { desSP = *desired.StripPath }
                    if curSP != desSP { changed = true }
                    if cur.Service.Name != desired.Service.Name && desired.Service.Name != "" { changed = true }
                    if changed {
                        if applyOverwrite {
                            action, _, err := client.CreateOrUpdateRoute(ctx, desired)
                            if err != nil { return err }
                            PrintSuccess(cmd, "已%sed Route：name=%s service=%s", actionCN(action), name, r.Service)
                        } else {
                            PrintWarn(cmd, "检测到 Route 变更但未启用覆盖：%s（跳过，使用 --overwrite 应用变更）", name)
                        }
                    }
                }
            }
        }

        if dryRun {
            printHierPlan(cmd, plan, spec, autoInfos, autoSvcSet, autoUpSet, showDiff)
            if !applyOverwrite {
                PrintInfo(cmd, "提示：当前未启用覆盖更新（--overwrite）。执行时仅创建缺失资源，不修改已存在的远程配置。")
            }
            cmd.Println("[dry-run] 以上为计划操作（未实际变更）✅")
        }
        return nil
    },
}

func init() {
    rootCmd.AddCommand(applyCmd)
    // 子命令：生成示例 YAML
    applyCmd.AddCommand(applyExampleCmd)
    applyCmd.Flags().StringVarP(&applyFile, "file", "f", "", "配置文件路径（YAML/JSON），例：-f examples/apply.yaml")
    applyCmd.Flags().BoolVar(&dryRun, "dry-run", false, "仅显示计划，不实际变更（例：--dry-run --diff）")
    applyCmd.Flags().BoolVar(&showDiff, "diff", false, "显示操作摘要与字段差异（配合 --dry-run）")
    applyCmd.Flags().BoolVar(&applyNoColor, "no-color", false, "禁用彩色输出")
    applyCmd.Flags().BoolVar(&applyASCII, "ascii", false, "使用 ASCII 输出（避免 Unicode 图形字符）")
    applyCmd.Flags().BoolVar(&applyCompact, "compact", false, "紧凑模式：隐藏无变化项（none）")
    applyCmd.Flags().BoolVar(&applyOverwrite, "overwrite", false, "允许覆盖远程已有配置（默认只创建，不更新）")
}

// ----- apply example 子命令 -----

var (
    exampleType   string
    exampleOutput string
    exampleNoComments bool
    exampleForce  bool
)

var applyExampleCmd = &cobra.Command{
    Use:   "example",
    Short: "生成带注释的 apply 示例 YAML",
    Long:  "生成各种 apply 示例（full/route-simple/routes-simple/route-basic）的 YAML 模板，默认输出到标准输出，可通过 -o 保存到文件。",
    Example: `# 生成完整示例（upstreams/services/routes）到控制台
kongctl apply example --type full

# 生成 routes 简写示例到文件（若已存在需 --force）
kongctl apply example --type routes-simple -o examples/route-simple.yaml --force

# 生成最简路由（引用已存在 service）且不包含注释
kongctl apply example --type route-basic --no-comments

# 生成仓库中 examples/route-simple.yaml 风格的多路由模板
kongctl apply example --type route-simple -o my-routes.yaml`,
    RunE: func(cmd *cobra.Command, args []string) error {
        t := strings.ToLower(strings.TrimSpace(exampleType))
        if t == "" { t = "full" }
        var content string
        switch t {
        case "full":
            content = exampleYAMLFull()
        case "route-simple":
            content = exampleYAMLRouteSimpleRepo()
        case "routes-simple":
            content = exampleYAMLSimpleRoutes()
        case "route-basic":
            content = exampleYAMLRouteBasic()
        default:
            return fmt.Errorf("不支持的 --type：%s（可选：full、routes-simple、route-basic）", exampleType)
        }
        if exampleNoComments {
            // 过滤注释行（保留 shebang 风格为空）
            var sb strings.Builder
            for _, line := range strings.Split(content, "\n") {
                lt := strings.TrimSpace(line)
                if strings.HasPrefix(lt, "#") { continue }
                sb.WriteString(line)
                sb.WriteByte('\n')
            }
            content = sb.String()
        }
        if exampleOutput == "" || exampleOutput == "-" {
            cmd.Print(content)
            return nil
        }
        // 写入文件
        if err := os.MkdirAll(filepath.Dir(exampleOutput), 0o755); err != nil {
            return fmt.Errorf("创建目录失败：%w", err)
        }
        if !exampleForce {
            if _, err := os.Stat(exampleOutput); err == nil {
                return fmt.Errorf("目标文件已存在：%s（使用 --force 覆盖）", exampleOutput)
            }
        }
        if err := os.WriteFile(exampleOutput, []byte(content), 0o644); err != nil {
            return fmt.Errorf("写入示例失败：%w", err)
        }
        PrintSuccess(cmd, "示例已生成：%s（type=%s）", exampleOutput, t)
        return nil
    },
}

func init() {
    applyExampleCmd.Flags().StringVar(&exampleType, "type", "full", "示例类型：full、route-simple、routes-simple、route-basic")
    applyExampleCmd.Flags().StringVarP(&exampleOutput, "output", "o", "", "输出文件路径（留空输出到控制台）")
    applyExampleCmd.Flags().BoolVar(&exampleNoComments, "no-comments", false, "移除注释，仅输出纯 YAML")
    applyExampleCmd.Flags().BoolVar(&exampleForce, "force", false, "覆盖已存在文件")
}

func exampleYAMLFull() string {
    return `# 通过 kongctl apply -f <file> 应用
# 完整示例：包含 upstreams / services / routes 三类资源

upstreams:
  - name: user-service-upstream   # 上游命名；与 Service 通过 host 关联
    targets:                      # 将后端实例注册为 target（host:port）
      - target: user-svc-1:8080
        weight: 100               # 权重，0~1000（未设置默认 100）
      - target: user-svc-2:8080
        weight: 100

services:
  - name: user-service            # Service 名称
    upstream: user-service-upstream # 关联 upstream 名（生成的 Service.host 即此值）
    protocol: http                # 上游协议（默认 http）
    port: 8080                    # 上游端口（http 默认 80；https 默认 443）
    path: /api                    # 上游基础路径，可为空
    retries: 5                    # 可选：重试次数
    connect_timeout: 60000        # 可选：连接超时（毫秒）
    read_timeout: 60000           # 可选：读取超时（毫秒）
    write_timeout: 60000          # 可选：写入超时（毫秒）

routes:
  - name: user-list               # Route 名称
    service: user-service         # 绑定的 Service 名称
    hosts: ["api.example.com"]    # 可选：按 Host 过滤
    paths: ["/v1/users"]          # 路径匹配（支持多个）
    methods: ["GET"]              # 方法匹配（可选）
    protocols: ["http", "https"]  # 可选：限定协议
    path_handling: v1             # v0/v1（Kong 3.x 等价 v1）
    strip_path: true              # 是否在转发前去掉匹配前缀
    preserve_host: false          # 是否保留原始 Host 头
    regex_priority: 0             # 正则优先级（更高优先）
    https_redirect_status_code: 0 # https 重定向状态码（如 426/301/302/307/308）
    request_buffering: true       # 请求缓冲
    response_buffering: true      # 响应缓冲
    headers:                      # 可选：按请求头匹配（键到值列表）
      X-Env: ["prod"]
    tags: ["team:user", "env:prod"] # 可选：给资源打标签
`
}

func exampleYAMLSimpleRoutes() string {
    return `# 顶层为 routes 列表（简写）：仅定义路由，自动生成 <name>-service 与 <name>-upstream
# - 未显式提供 service 时：根据 backend 自动创建 service 与 upstream，并把 targets 挂到 upstream。
# - 可通过 service_name/upstream_name 自定义自动生成的名称。

- name: demo-route                         # 路由名称；未提供 service 时将生成 demo-route-service / demo-route-upstream
  hosts: ["api.example.com"]               # 可选：按 Host 过滤；省略表示不限制主机
  paths: ["/demo"]                         # 路径匹配；v1 仅匹配路径段边界，/demo 不会匹配 /demox
  methods: ["GET", "POST"]                 # 可选：HTTP 方法过滤；省略表示任意方法
  protocols: ["http", "https"]             # 可选：限定协议；默认 http/https
  path_handling: v1                        # 路径处理版本（建议 v1）；v0 为前缀匹配，可能误匹配 /foobar
  strip_path: true                         # 去除匹配前缀再转发给上游
  preserve_host: false                     # 将上游 Host 设为 service.host（false）；true 则保留客户端原始 Host
  # service_name: custom-svc               # 可选：自定义自动创建的 service 名称
  # upstream_name: custom-up               # 可选：自定义自动创建的 upstream 名称
  backend:                                 # 描述上游（用于自动创建 service/upstream）
    protocol: http                         # 上游协议（默认 http）
    port: 8080                             # 上游端口（http 默认 80；https 默认 443）
    path: /api                             # 上游基础路径（会与 strip_path 后余下路径拼接）
    targets:                               # 后端实例列表（host:port）
      - target: demo-svc-1:8080
        weight: 100                        # 权重（0~1000；未指定默认 100）
      - target: demo-svc-2:8080
        weight: 100
`
}

func exampleYAMLRouteBasic() string {
    return `# 仅定义 Route，绑定到已存在的 Service
# 适合已有 Service 时，追加一条路径或主机匹配（不会创建 service/upstream）

routes:
  - name: echo-root                 # 路由名称
    service: echo                   # 必填：已存在的 Service 名称
    hosts: ["example.com"]          # 可选：Host 过滤；省略则不限制主机
    paths: ["/"]                    # 路径匹配；v1 仅匹配路径段边界
    methods: ["GET", "HEAD"]        # 可选：方法过滤；省略表示任意方法
    protocols: ["http", "https"]     # 可选：限定协议
    path_handling: v1               # v0/v1；推荐 v1（不误匹配 /foobar）
    strip_path: false               # 是否去除匹配前缀；根路径通常保留为 false
    # preserve_host: false          # 可选：是否保留原始 Host 头
    # headers:                      # 可选：按请求头匹配
    #   X-Debug: ["1"]
    # tags: ["team:core"]          # 可选：打标签
`
}

// exampleYAMLRouteSimpleRepo 输出与仓库 examples/route-simple.yaml 风格一致的多路由简写模板
func exampleYAMLRouteSimpleRepo() string {
    return `# 顶层为一个 routes 列表（简写）：仅定义路由，自动创建 <name>-service 与 <name>-upstream 并挂载 targets
# - 未显式提供 service 时：根据 backend 创建 service/upstream，并写入协议/端口/基础路径。
# - 可用 path_handling 控制路径匹配边界：推荐 v1（按路径段匹配，不误匹配 /foobar）。
# - 可按需补充 hosts/protocols/preserve_host 等字段。

# --- 感知平台服务 ---
- name: perceptual-platform-server-route      # 路由名称
  paths: ["/serv/perceptual-platform-server"] # 路径匹配前缀
  methods: ["GET"]                            # 方法过滤（省略则为任意）
  path_handling: v1                           # v0/v1；建议 v1
  strip_path: true                            # 去除匹配前缀再转发
  # hosts: ["api.example.com"]                # 可选：按 Host 过滤
  # protocols: ["http", "https"]              # 可选：限定协议
  # preserve_host: false                       # 可选：是否保留原始 Host
  # service_name: perceptual-service           # 可选：自定义自动创建的 service 名称
  # upstream_name: perceptual-upstream         # 可选：自定义自动创建的 upstream 名称
  backend:                                     # 上游描述（用于自动创建 service/upstream）
    protocol: http                             # 上游协议
    port: 80                                   # 上游端口
    path: /                                    # 上游基础路径（与余下路径拼接）
    targets:                                   # 后端实例列表
      - target: perceptual-platform-server-server:23663
        weight: 100

# --- 服务目录 ---
- name: services
  paths: ["/services"]
  methods: ["GET"]
  path_handling: v1
  strip_path: true
  backend:
    protocol: http
    port: 80
    path: /
    targets:
      - target: euoap-atom:80
        weight: 100

# --- 调度执行器 ---
- name: attemper-executor-route
  paths: ["/serv/attemper-executor"]
  methods: ["GET"]
  path_handling: v1
  strip_path: true
  backend:
    protocol: http
    port: 80
    path: /
    targets:
      - target: attemper-executor:5212
        weight: 100

# --- 故障预处理 ---
- name: euoap-fault-pre-route
  paths: ["/serv/fault-pre/"]                 # 注意尾随斜杠；v1 下 /serv/fault-pre 与 /serv/fault-pre/ 行为不同
  methods: ["GET"]
  path_handling: v1
  strip_path: true
  backend:
    protocol: http
    port: 80
    path: /
    targets:
      - target: fault-preprocessing-web:28088
        weight: 100

# --- 认证 ---
- name: auth-route
  paths: ["/auth-euoap"]
  methods: ["GET"]
  path_handling: v1
  strip_path: true
  backend:
    protocol: http
    port: 80
    path: /
    targets:
      - target: auth:80
        weight: 100

# --- 调度 Web ---
- name: attemper-web-route
  paths: ["/serv/attemper-web"]
  methods: ["GET"]
  path_handling: v1
  strip_path: true
  backend:
    protocol: http
    port: 80
    path: /
    targets:
      - target: attemper-web:5210
        weight: 100
`
}

// ----- 层级化 Dry-Run 展示 -----

func printHierPlan(cmd *cobra.Command, plan aplan.Plan, spec applySpec, autoInfos []autoRouteInfo, autoSvcSet, autoUpSet map[string]bool, withDiff bool) {
    useColor := !(applyNoColor || viper.GetBool("no_color"))
    ascii := applyASCII
    compact := applyCompact
    p := func(indent int, format string, args ...any) {
        cmd.Printf("%s%s\n", strings.Repeat("  ", indent), fmt.Sprintf(format, args...))
    }
    // color helpers
    c := func(s, code string) string { if !useColor { return s }; return code + s + "\033[0m" }
    header := func(s string) string { return c(s, "\033[36;1m") }      // bold cyan
    accent := func(s string) string { return c(s, "\033[35;1m") }      // bold magenta
    subtle := func(s string) string { return c(s, "\033[90m") }        // gray
    actColor := func(a string) string {
        switch a {
        case "create":
            if ascii { return c("创建", "\033[32m") }
            return c("创建 ✨", "\033[32m") // green
        case "update":
            if ascii { return c("更新", "\033[33m") }
            return c("更新 ♻️", "\033[33m") // yellow
        case "none":
            return c("无变化", "\033[90m") // gray
        default:
            return a
        }
    }
    diffColor := func(line string) string {
        if !useColor { return line }
        s := strings.TrimSpace(line)
        if strings.HasPrefix(s, "+ ") || strings.HasPrefix(s, "+") {
            return "\033[32m" + line + "\033[0m"
        }
        if strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "-") {
            return "\033[31m" + line + "\033[0m"
        }
        return line
    }
    kindIcon := func(kind string) string {
        if ascii {
            switch kind {
            case "Upstream": return "[U]"
            case "Target": return "[T]"
            case "Service": return "[S]"
            case "Route": return "[R]"
            default: return "[*]"
            }
        }
        switch kind {
        case "Upstream": return "🌐"
        case "Target": return "🎯"
        case "Service": return "🧩"
        case "Route": return "🛣️"
        default: return "•"
        }
    }
    sep := func() {
        if ascii { p(0, strings.Repeat("=", 40)) } else { p(0, strings.Repeat("─", 40)) }
    }
    find := func(kind, name string) *aplan.Change {
        for i := range plan.Items {
            if plan.Items[i].Kind == kind && plan.Items[i].Name == name {
                return &plan.Items[i]
            }
        }
        return nil
    }
    findTargets := func(prefix string) []aplan.Change {
        out := []aplan.Change{}
        for _, it := range plan.Items {
            if it.Kind == "Target" && strings.HasPrefix(it.Name, prefix+"/") {
                out = append(out, it)
            }
        }
        return out
    }

    // summary header
    p(0, header("变更计划："))
    sep()
    // 汇总计数
    type cnt struct{ c, u, n int }
    var cntUp, cntSvc, cntRt, cntTgt cnt

    // 顶层 Upstreams（排除由简写自动生成的）
    if len(spec.Upstreams) > 0 {
        p(1, header("Upstreams:"))
        for _, up := range spec.Upstreams {
            ch := find("Upstream", up.Name)
            action := "none"; if ch != nil && ch.Action != "" { action = ch.Action }
            switch action { case "create": cntUp.c++; case "update": cntUp.u++; default: cntUp.n++ }
            if compact && action == "none" && len(up.Targets) == 0 { continue }
            p(2, "%s %s (%s)", kindIcon("Upstream"), up.Name, actColor(action))
            // targets from spec
            if len(up.Targets) > 0 { p(3, subtle("Targets:")) }
            for _, t := range up.Targets {
                // find plan result for this target
                tname := up.Name + "/" + t.Target
                taction := "none"
                for _, tc := range findTargets(up.Name) { if tc.Name == tname && tc.Action != "" { taction = tc.Action; break } }
                switch taction { case "create": cntTgt.c++; case "update": cntTgt.u++; default: cntTgt.n++ }
                if !(compact && taction == "none") { p(4, "%s %s (%s)", kindIcon("Target"), t.Target, actColor(taction)) }
            }
        }
        sep()
    }

    // 顶层 Services（排除由简写自动生成的）
    if len(spec.Services) > 0 {
        p(1, header("Services:"))
        for _, s := range spec.Services {
            ch := find("Service", s.Name)
            action := "none"; if ch != nil && ch.Action != "" { action = ch.Action }
            switch action { case "create": cntSvc.c++; case "update": cntSvc.u++; default: cntSvc.n++ }
            if compact && action == "none" && (ch == nil || strings.TrimSpace(ch.Diff) == "") && len(s.Targets) == 0 { continue }
            p(2, "%s %s (%s)", kindIcon("Service"), s.Name, actColor(action))
            if withDiff && ch != nil && strings.TrimSpace(ch.Diff) != "" {
                for _, line := range strings.Split(strings.TrimSpace(ch.Diff), "\n") {
                    if strings.TrimSpace(line) == "" { continue }
                    p(3, "%s", diffColor("- "+line))
                }
            }
            // If service carries targets in spec, show them under its upstream (if provided)
            if s.Upstream != "" && len(s.Targets) > 0 {
                p(3, subtle(fmt.Sprintf("Targets (Upstream %s):", s.Upstream)))
                for _, t := range s.Targets {
                    tname := s.Upstream + "/" + t.Target
                    taction := "none"
                    for _, tc := range findTargets(s.Upstream) { if tc.Name == tname && tc.Action != "" { taction = tc.Action; break } }
                    switch taction { case "create": cntTgt.c++; case "update": cntTgt.u++; default: cntTgt.n++ }
                    if !(compact && taction == "none") { p(4, "%s %s (%s)", kindIcon("Target"), t.Target, actColor(taction)) }
                }
            }
        }
        sep()
    }

    // Routes（包含简写的嵌套展示）
    if len(spec.Routes) > 0 {
        p(1, header("Routes:"))
        routePrinted := false
        for _, r := range spec.Routes {
            name := r.Name
            if name == "" && r.Service != "" { name = defaultRouteName(r.Service, r.Paths, r.Methods) }
            if name == "" { continue }
            ch := find("Route", name)
            action := "none"; if ch != nil && ch.Action != "" { action = ch.Action }
            switch action { case "create": cntRt.c++; case "update": cntRt.u++; default: cntRt.n++ }
            if compact && action == "none" && (ch == nil || strings.TrimSpace(ch.Diff) == "") && len(r.Backend.Targets) == 0 { continue }
            // route-level separator between different routes (accent color)
            if routePrinted {
                if ascii { p(2, accent(strings.Repeat("=", 40))) } else { p(2, accent(strings.Repeat("━", 40))) }
            }
            p(2, "%s %s (%s)", kindIcon("Route"), name, actColor(action))
            if withDiff && ch != nil && ch.Diff != "" {
                for _, line := range strings.Split(strings.TrimSpace(ch.Diff), "\n") {
                    if strings.TrimSpace(line) == "" { continue }
                    p(3, "%s", diffColor(line))
                }
            }
            // 若为简写，嵌套其 service 和 upstream
            if r.Service == "" {
                // 查找对应 auto 信息
                var svcName, upName string
                for _, ai := range autoInfos {
                    if ai.RouteName == name { svcName, upName = ai.ServiceName, ai.UpstreamName; break }
                }
                if svcName != "" {
                    if sch := find("Service", svcName); sch != nil {
                        p(3, "%s Service: %s (%s)", kindIcon("Service"), svcName, actColor(sch.Action))
                        if withDiff && strings.TrimSpace(sch.Diff) != "" {
                            for _, line := range strings.Split(strings.TrimSpace(sch.Diff), "\n") {
                                if strings.TrimSpace(line) == "" { continue }
                                p(4, "%s", diffColor(line))
                            }
                        }
                    } else {
                        p(3, "%s Service: %s (%s)", kindIcon("Service"), svcName, actColor("none"))
                    }
                }
                if upName != "" {
                    if uch := find("Upstream", upName); uch != nil {
                        p(3, "%s Upstream: %s (%s)", kindIcon("Upstream"), upName, actColor(uch.Action))
                    } else {
                        p(3, "%s Upstream: %s (%s)", kindIcon("Upstream"), upName, actColor("none"))
                    }
                    // targets from spec backend
                    if len(r.Backend.Targets) > 0 { p(4, subtle("Targets:")) }
                    for _, t := range r.Backend.Targets {
                        tname := upName + "/" + t.Target
                        taction := "none"
                        for _, tc := range findTargets(upName) { if tc.Name == tname && tc.Action != "" { taction = tc.Action; break } }
                        if !(compact && taction == "none") { p(5, "%s %s (%s)", kindIcon("Target"), t.Target, actColor(taction)) }
                    }
                }
            }
            routePrinted = true
        }
        sep()
    }

    // 汇总（基于 plan 重新准确统计，包含简写自动生成项）
    cntUp, cntSvc, cntRt, cntTgt = cnt{}, cnt{}, cnt{}, cnt{}
    for _, it := range plan.Items {
        action := it.Action
        switch it.Kind {
        case "Upstream":
            if action == "create" { cntUp.c++ } else if action == "update" { cntUp.u++ } else { cntUp.n++ }
        case "Service":
            if action == "create" { cntSvc.c++ } else if action == "update" { cntSvc.u++ } else { cntSvc.n++ }
        case "Route":
            if action == "create" { cntRt.c++ } else if action == "update" { cntRt.u++ } else { cntRt.n++ }
        case "Target":
            if action == "create" { cntTgt.c++ } else if action == "update" { cntTgt.u++ } else { cntTgt.n++ }
        }
    }
    colNum := func(n int, a string) string {
        s := fmt.Sprintf("%d", n)
        if !useColor { return s }
        switch a {
        case "create": return c(s, "\033[32;1m") // bold green
        case "update": return c(s, "\033[33;1m") // bold yellow
        case "none":   return c(s, "\033[90m")   // gray
        }
        return s
    }
    p(0, header("汇总："))
    p(1, "Upstreams: 创建 %s，更新 %s，无变化 %s", colNum(cntUp.c, "create"), colNum(cntUp.u, "update"), colNum(cntUp.n, "none"))
    p(1, "Services: 创建 %s，更新 %s，无变化 %s", colNum(cntSvc.c, "create"), colNum(cntSvc.u, "update"), colNum(cntSvc.n, "none"))
    p(1, "Routes:   创建 %s，更新 %s，无变化 %s", colNum(cntRt.c, "create"), colNum(cntRt.u, "update"), colNum(cntRt.n, "none"))
    p(1, "Targets:  创建 %s，更新 %s，无变化 %s", colNum(cntTgt.c, "create"), colNum(cntTgt.u, "update"), colNum(cntTgt.n, "none"))
    if !ascii {
        p(0, subtle("提示：可使用 --no-color 关闭颜色，--ascii 使用 ASCII，--compact 隐藏无变化项"))
    } else {
        p(0, "提示：可使用 --no-color 关闭颜色，--ascii 使用 ASCII，--compact 隐藏无变化项")
    }
}
