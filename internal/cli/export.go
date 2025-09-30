package cli

import (
    "context"
    "fmt"
    "os"
    "sort"
    "strings"
    "time"

    "github.com/spf13/cobra"
    "github.com/spf13/viper"
    "gopkg.in/yaml.v3"
    "kongctl/internal/kong"
)

var (
    exportOutput string
    exportShorthand bool
    exportIncludeOrphans bool
)

// exportCmd 导出远程 Kong 配置为本地 YAML，结构与 apply 兼容
var exportCmd = &cobra.Command{
    Use:   "export",
    Short: "导出远程 Kong 配置为 YAML（与 apply 兼容）",
    Example: `# 导出全部（输出到标准输出）
kongctl export

# 导出到文件
kongctl export -o kong-export.yaml

# 以 routes 简写导出（将 service/upstream 折叠到 backend）
kongctl export --shorthand -o routes.yaml`,
    RunE: func(cmd *cobra.Command, args []string) error {
        cfg := kong.Config{
            AdminURL:      viper.GetString("admin_url"),
            Token:         viper.GetString("token"),
            TLSSkipVerify: viper.GetBool("tls_skip_verify"),
            Timeout:       20 * time.Second,
        }
        if cfg.AdminURL == "" {
            return fmt.Errorf("请通过 --admin-url 或 KONGCTL_ADMIN_URL 指定 Admin API 地址；或运行 'kongctl init --admin-url <url>' 持久化配置")
        }
        client := kong.NewClient(cfg)
        ctx, cancel := context.WithTimeout(cmd.Context(), cfg.Timeout)
        defer cancel()

        // 1) 列出 upstreams 与 targets
        ups, err := client.ListUpstreams(ctx)
        if err != nil { return err }
        upNames := map[string]bool{}
        specUps := make([]applyUpstream, 0, len(ups))
        upTargets := make(map[string][]applyTarget, len(ups))
        for _, up := range ups {
            if strings.TrimSpace(up.Name) == "" { continue }
            upNames[up.Name] = true
            ats, err := client.ListTargets(ctx, up.Name)
            if err != nil { return err }
            targets := make([]applyTarget, 0, len(ats))
            for _, t := range ats {
                if strings.TrimSpace(t.Target) == "" { continue }
                targets = append(targets, applyTarget{Target: t.Target, Weight: t.Weight})
            }
            specUps = append(specUps, applyUpstream{Name: up.Name, Targets: targets})
            upTargets[up.Name] = targets
        }
        sort.Slice(specUps, func(i, j int) bool { return specUps[i].Name < specUps[j].Name })

        // 2) 列出 services
        svcs, err := client.ListServices(ctx)
        if err != nil { return err }
        specSvcs := make([]applyService, 0, len(svcs))
        svcID2Name := map[string]string{}
        svcByName := make(map[string]kong.Service, len(svcs))
        svcByID := make(map[string]kong.Service, len(svcs))
        for _, s := range svcs {
            svcID2Name[s.ID] = s.Name
            svcByName[s.Name] = s
            if s.ID != "" { svcByID[s.ID] = s }
            as := applyService{
                Name:           s.Name,
                Retries:        s.Retries,
                ConnectTimeout: s.ConnectTimeout,
                ReadTimeout:    s.ReadTimeout,
                WriteTimeout:   s.WriteTimeout,
            }
            // 优先导出为 Upstream 形式（若 Host 刚好是某个 upstream 名称）
            if s.Host != "" && upNames[s.Host] {
                as.Upstream = s.Host
                if s.Protocol != "" { as.Protocol = s.Protocol }
                if s.Port != 0 { as.Port = s.Port }
                if s.Path != "" { as.Path = s.Path }
            } else if s.URL != "" {
                as.URL = s.URL
            } else {
                // 回退为 URL 形式
                url := reconstructURL(&kong.Service{
                    Protocol: s.Protocol,
                    Host:     s.Host,
                    Port:     s.Port,
                    Path:     s.Path,
                })
                if url != "" { as.URL = url }
            }
            specSvcs = append(specSvcs, as)
        }
        sort.Slice(specSvcs, func(i, j int) bool { return specSvcs[i].Name < specSvcs[j].Name })

        // 3) 列出 routes
        rts, err := client.ListRoutes(ctx)
        if err != nil { return err }
        specRts := make([]applyRoute, 0, len(rts))
        rtByName := make(map[string]kong.Route, len(rts))
        for _, r := range rts {
            if r.Name != "" { rtByName[r.Name] = r }
            ar := applyRoute{
                Name:      r.Name,
                Hosts:     r.Hosts,
                Paths:     r.Paths,
                Methods:   r.Methods,
                PathHandling: strings.ToLower(strings.TrimSpace(r.PathHandling)),
                Protocols: r.Protocols,
                RegexPriority: r.RegexPriority,
                HTTPSRedirectStatusCode: r.HTTPSRedirectStatusCode,
                Headers: r.Headers,
                Snis:    r.Snis,
                Tags:    r.Tags,
            }
            if r.PreserveHost != nil { v := *r.PreserveHost; ar.PreserveHost = &v }
            if r.RequestBuffering != nil { v := *r.RequestBuffering; ar.RequestBuffering = &v }
            if r.ResponseBuffering != nil { v := *r.ResponseBuffering; ar.ResponseBuffering = &v }
            if r.StripPath != nil { v := *r.StripPath; ar.StripPath = &v }
            // 关联 service 名称优先
            if r.Service.Name != "" {
                ar.Service = r.Service.Name
            } else if r.Service.ID != "" {
                if name, ok := svcID2Name[r.Service.ID]; ok { ar.Service = name }
            }
            specRts = append(specRts, ar)
        }
        sort.Slice(specRts, func(i, j int) bool { return specRts[i].Name < specRts[j].Name })

        // 若选择简写导出：将 service/upstream 折叠到 route.backend，输出顶层 routes 列表
        if exportShorthand {
            // 定义仅用于导出的简写结构（带 omitempty 以获得更简洁的 YAML）
            type exportBackend struct {
                Protocol string        `yaml:"protocol,omitempty"`
                Port     int           `yaml:"port,omitempty"`
                Path     string        `yaml:"path,omitempty"`
                Targets  []applyTarget `yaml:"targets,omitempty"`
            }
            type exportRoute struct {
                Name      string   `yaml:"name"`
                Hosts     []string `yaml:"hosts"`
                Paths     []string `yaml:"paths"`
                Methods   []string `yaml:"methods"`
                StripPath *bool    `yaml:"strip_path,omitempty"`
                PathHandling string `yaml:"path_handling,omitempty"`
                Protocols   []string            `yaml:"protocols"`
                PreserveHost *bool              `yaml:"preserve_host,omitempty"`
                RegexPriority int               `yaml:"regex_priority"`
                HTTPSRedirectStatusCode int     `yaml:"https_redirect_status_code,omitempty"`
                RequestBuffering *bool          `yaml:"request_buffering,omitempty"`
                ResponseBuffering *bool         `yaml:"response_buffering,omitempty"`
                Headers map[string][]string     `yaml:"headers"`
                Snis    []string                `yaml:"snis"`
                Tags    []string                `yaml:"tags"`
                Backend  exportBackend          `yaml:"backend"`
            }
            type shorthandBundle struct {
                Routes    []exportRoute  `yaml:"routes"`
                Upstreams []applyUpstream `yaml:"upstreams,omitempty"`
            }
            exp := make([]exportRoute, 0, len(specRts))
            usedUp := map[string]bool{}
            for _, rt := range specRts {
                er := exportRoute{
                    Name:            rt.Name,
                    Hosts:           rt.Hosts,
                    Paths:           rt.Paths,
                    Methods:         rt.Methods,
                    StripPath:       rt.StripPath,
                    PathHandling:    rt.PathHandling,
                    Protocols:       rt.Protocols,
                    PreserveHost:    rt.PreserveHost,
                    RegexPriority:   rt.RegexPriority,
                    HTTPSRedirectStatusCode: rt.HTTPSRedirectStatusCode,
                    RequestBuffering: rt.RequestBuffering,
                    ResponseBuffering: rt.ResponseBuffering,
                    Headers:         rt.Headers,
                    Snis:            rt.Snis,
                    Tags:            rt.Tags,
                }
                // 归一化空集合，避免输出 null；以 [] / {} 形式呈现
                if er.Hosts == nil { er.Hosts = []string{} }
                if er.Paths == nil { er.Paths = []string{} }
                if er.Methods == nil { er.Methods = []string{} }
                if er.Protocols == nil { er.Protocols = []string{} }
                if er.Headers == nil { er.Headers = map[string][]string{} }
                if er.Snis == nil { er.Snis = []string{} }
                if er.Tags == nil { er.Tags = []string{} }
                // 推断 backend：基于 Route->Service->Upstream 关系
                // 1) 找到 Service
                var svc kong.Service
                var hasSvc bool
                if rt.Service != "" {
                    if s, ok := svcByName[rt.Service]; ok { svc = s; hasSvc = true }
                } else if r0, ok := rtByName[rt.Name]; ok {
                    // 若导出时未能解析到 service 名，尝试使用 ID 匹配
                    if r0.Service.ID != "" {
                        if s, ok2 := svcByID[r0.Service.ID]; ok2 { svc = s; hasSvc = true }
                    }
                }
                if hasSvc {
                    upName := svc.Host
                    if upNames[upName] {
                        er.Backend.Protocol = svc.Protocol
                        er.Backend.Port = svc.Port
                        er.Backend.Path = svc.Path
                        if ts := upTargets[upName]; len(ts) > 0 { er.Backend.Targets = ts }
                        usedUp[upName] = true
                    }
                }
                // backend.targets 也归一化为空 slice
                if er.Backend.Targets == nil { er.Backend.Targets = []applyTarget{} }
                exp = append(exp, er)
            }
            if exportIncludeOrphans {
                // 仅保留未被 routes 引用的 upstreams
                orphans := make([]applyUpstream, 0)
                for _, up := range specUps {
                    if !usedUp[up.Name] {
                        // 确保 targets 非空指针（空也输出 []）
                        if up.Targets == nil { up.Targets = []applyTarget{} }
                        orphans = append(orphans, up)
                    }
                }
                bundle := shorthandBundle{Routes: exp, Upstreams: orphans}
                out, err := yaml.Marshal(bundle)
                if err != nil { return err }
                if exportOutput == "" || exportOutput == "-" {
                    cmd.Println(string(out))
                    PrintSuccess(cmd, "已导出 routes 简写并附加未引用的 upstreams 到标准输出（--shorthand --include-orphans）")
                    return nil
                }
                if err := os.WriteFile(exportOutput, out, 0644); err != nil {
                    return fmt.Errorf("写入文件失败：%w", err)
                }
                PrintSuccess(cmd, "已导出 routes 简写并附加未引用的 upstreams 到：%s", exportOutput)
                return nil
            } else {
                out, err := yaml.Marshal(exp)
                if err != nil { return err }
                if exportOutput == "" || exportOutput == "-" {
                    cmd.Println(string(out))
                    if len(exp) > 0 {
                        PrintSuccess(cmd, "已导出 routes 简写到标准输出（--shorthand）")
                    } else {
                        PrintSuccess(cmd, "已导出到标准输出（--shorthand）")
                    }
                    return nil
                }
                if err := os.WriteFile(exportOutput, out, 0644); err != nil {
                    return fmt.Errorf("写入文件失败：%w", err)
                }
                PrintSuccess(cmd, "已导出 routes 简写到：%s", exportOutput)
                return nil
            }
        }

        // 组合为 apply 兼容结构（完整形式）
        spec := applySpec{Upstreams: specUps, Services: specSvcs, Routes: specRts}

        out, err := yaml.Marshal(spec)
        if err != nil { return err }

        if exportOutput == "" || exportOutput == "-" {
            cmd.Println(string(out))
            PrintSuccess(cmd, "已导出配置到标准输出（可重定向保存）")
            return nil
        }
        if err := os.WriteFile(exportOutput, out, 0644); err != nil {
            return fmt.Errorf("写入文件失败：%w", err)
        }
        PrintSuccess(cmd, "已导出配置到：%s", exportOutput)
        return nil
    },
}

func init() {
    rootCmd.AddCommand(exportCmd)
    exportCmd.Flags().StringVarP(&exportOutput, "output", "o", "", "输出文件路径（默认输出到标准输出），例：-o kong.yaml")
    exportCmd.Flags().BoolVar(&exportShorthand, "shorthand", false, "以 routes 简写导出（将 service/upstream 折叠到 backend）")
    exportCmd.Flags().BoolVar(&exportIncludeOrphans, "include-orphans", false, "在 --shorthand 模式下，附加未被路由引用的 upstreams（顶层 upstreams 列表）")
}
