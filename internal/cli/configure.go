package cli

import (
    "bytes"
    "context"
    "crypto/tls"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "os"
    "path/filepath"
    "strings"
    "time"

    "github.com/spf13/cobra"
)

var (
    flagAdminURL string
    flagToken    string
    flagWorkspace string
)

var initCmd = &cobra.Command{
    Use:   "init",
    Short: "初始化并写入本地配置（~/.kongctl/config.yaml）",
    SilenceUsage:  true,
    SilenceErrors: true,
    Example: `# 写入 Admin API 地址与 Token（建议本地开发使用）
kongctl init --admin-url http://localhost:8001 --token <KONG_ADMIN_TOKEN>

# 指定 Workspace（可选）
kongctl init --admin-url http://localhost:8001 --workspace default

# 自动检测：不提供 --admin-url 时，尝试探测常见地址
kongctl init`,
    RunE: func(cmd *cobra.Command, args []string) error {
        // 构建候选地址列表：优先 flag，其次环境变量，最后常见地址
        add := func(list *[]string, u string) { if u == "" { return }; for _, x := range *list { if x == u { return } }; *list = append(*list, u) }
        var candidates []string
        add(&candidates, flagAdminURL)
        add(&candidates, os.Getenv("KONGCTL_ADMIN_URL"))
        add(&candidates, os.Getenv("KONG_ADMIN_URL"))
        add(&candidates, "http://localhost:8001")
        add(&candidates, "https://localhost:8444")
        add(&candidates, "http://127.0.0.1:8001")
        add(&candidates, "http://host.docker.internal:8001")
        add(&candidates, "http://kong:8001")

        // 逐个检测可达性（不强制 2xx，只要能连通即可）
        provided := flagAdminURL
        var detected string
        for _, u := range candidates {
            if u == "" { continue }
            ctx, cancel := context.WithTimeout(cmd.Context(), 1500*time.Millisecond)
            ok := probeAdminURL(ctx, u, flagToken)
            cancel()
            if ok {
                detected = u
                break
            }
        }
        if detected == "" {
            if provided != "" {
                // 尝试规范化显示
                disp := provided
                if !strings.HasPrefix(disp, "http://") && !strings.HasPrefix(disp, "https://") {
                    disp = "http://" + disp
                }
                return fmt.Errorf("无法连接到指定地址或目标非 Kong Admin API：%s。请检查端口是否为 Admin API（通常为 8001/8444），或确认服务可达。", disp)
            }
            return fmt.Errorf("未能探测到有效的 Kong Admin API 地址。请通过 --admin-url 指定，或设置环境变量 KONGCTL_ADMIN_URL")
        }
        if flagAdminURL != "" && detected != flagAdminURL {
            PrintWarn(cmd, "提供的 --admin-url 不可达，已自动选择可用地址：%s", detected)
        } else if flagAdminURL != "" {
            PrintInfo(cmd, "已验证 Admin API 地址：%s", detected)
        } else {
            PrintInfo(cmd, "已自动检测 Admin API 地址：%s", detected)
        }
        flagAdminURL = detected
        home, err := os.UserHomeDir()
        if err != nil {
            return err
        }
        dir := filepath.Join(home, ".kongctl")
        _ = os.MkdirAll(dir, 0o755)
        file := filepath.Join(dir, "config.yaml")
        content := fmt.Sprintf("admin_url: %s\ntoken: %s\nworkspace: %s\n", flagAdminURL, flagToken, flagWorkspace)
        if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
            return err
        }
        PrintSuccess(cmd, "已写入配置：%s", file)
        return nil
    },
}

func init() {
    initCmd.Flags().StringVar(&flagAdminURL, "admin-url", "", "Kong Admin API 地址，例：http://localhost:8001")
    initCmd.Flags().StringVar(&flagToken, "token", "", "Kong Admin Token，例：--token $KONG_ADMIN_TOKEN")
    initCmd.Flags().StringVar(&flagWorkspace, "workspace", "", "Workspace（可选），例：--workspace default")
}

// probeAdminURL 尝试访问 Admin API，返回是否连通（不要求 2xx，只要有响应即可）。
func probeAdminURL(ctx context.Context, baseURL, token string) bool {
    // 更严格的探测：必须能识别出 Kong Admin API
    u := baseURL
    if u == "" { return false }
    if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
        u = "http://" + u
    }
    // 为了初始化时尽可能成功，默认允许跳过证书校验（仅限探测）。
    tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec
    httpc := &http.Client{Transport: tr, Timeout: 1500 * time.Millisecond}

    // 依次尝试 /status 与 /
    paths := []string{"/status", "/"}
    for _, p := range paths {
        req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(u, "/")+p, nil)
        if token != "" {
            req.Header.Set("Kong-Admin-Token", token)
            req.Header.Set("Authorization", "Bearer "+token)
        }
        resp, err := httpc.Do(req)
        if err != nil { continue }

        // 读取少量响应体用于判断（限制 4KB）
        data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
        resp.Body.Close()

        // 先看响应头是否明显来自 Kong
        server := strings.ToLower(resp.Header.Get("Server"))
        via := strings.ToLower(resp.Header.Get("Via"))
        if strings.Contains(server, "kong") || strings.Contains(via, "kong") || resp.Header.Get("X-Kong-Admin-Latency") != "" {
            // 2xx、401、403 都接受（RBAC 场景常见）
            if resp.StatusCode/100 == 2 || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
                return true
            }
        }

        // 再看是否为 JSON 且结构类似 Kong 的输出
        ct := strings.ToLower(resp.Header.Get("Content-Type"))
        looksJSON := strings.Contains(ct, "json") || bytes.HasPrefix(bytes.TrimSpace(data), []byte("{"))
        if looksJSON {
            var obj map[string]any
            if err := json.Unmarshal(bytes.TrimSpace(data), &obj); err == nil {
                if p == "/" {
                    if _, ok := obj["version"]; ok {
                        return true
                    }
                    if _, ok := obj["configuration"]; ok {
                        return true
                    }
                }
                if p == "/status" {
                    if _, ok := obj["database"]; ok {
                        return true
                    }
                    if _, ok := obj["server"]; ok {
                        return true
                    }
                }
                // 有些反代会返回 401 JSON，且包含 message 字段；若上面头部已识别为 Kong，则已返回。
            }
        }
    }
    return false
}
