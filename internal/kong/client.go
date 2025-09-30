package kong

import (
    "bytes"
    "context"
    "crypto/tls"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net/http"
    "strings"
    "time"
)

// Config 用于初始化客户端
type Config struct {
    AdminURL      string
    Token         string
    Workspace     string
    TLSSkipVerify bool
    Timeout       time.Duration
}

type Client struct {
    cfg    Config
    client *http.Client
}

func NewClient(cfg Config) *Client {
    // 若未指定协议，默认使用 http
    if cfg.AdminURL != "" && !strings.HasPrefix(cfg.AdminURL, "http://") && !strings.HasPrefix(cfg.AdminURL, "https://") {
        cfg.AdminURL = "http://" + cfg.AdminURL
    }
    tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.TLSSkipVerify}} //nolint:gosec
    return &Client{
        cfg: cfg,
        client: &http.Client{
            Transport: tr,
            Timeout:   cfg.Timeout,
        },
    }
}

// Ping 尝试访问 /status 或根路径，验证连通性
func (c *Client) Ping(ctx context.Context) error {
    paths := []string{"/status", "/"}
    var lastErr error
    for _, p := range paths {
        url := strings.TrimRight(c.cfg.AdminURL, "/") + p
        req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
        if c.cfg.Token != "" {
            req.Header.Set("Kong-Admin-Token", c.cfg.Token)
            req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
        }
        resp, err := c.client.Do(req)
        if err != nil {
            lastErr = err
            continue
        }
        data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
        resp.Body.Close()

        server := strings.ToLower(resp.Header.Get("Server"))
        via := strings.ToLower(resp.Header.Get("Via"))
        if strings.Contains(server, "kong") || strings.Contains(via, "kong") || resp.Header.Get("X-Kong-Admin-Latency") != "" {
            if resp.StatusCode/100 == 2 || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
                return nil
            }
            lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
            continue
        }

        ct := strings.ToLower(resp.Header.Get("Content-Type"))
        looksJSON := strings.Contains(ct, "json") || bytes.HasPrefix(bytes.TrimSpace(data), []byte("{"))
        if looksJSON {
            var obj map[string]any
            if err := json.Unmarshal(bytes.TrimSpace(data), &obj); err == nil {
                if p == "/" {
                    if _, ok := obj["version"]; ok {
                        return nil
                    }
                    if _, ok := obj["configuration"]; ok {
                        return nil
                    }
                }
                if p == "/status" {
                    if _, ok := obj["database"]; ok {
                        return nil
                    }
                    if _, ok := obj["server"]; ok {
                        return nil
                    }
                }
            }
        }
        lastErr = fmt.Errorf("目标非 Kong Admin API 或响应不可识别（HTTP %d）", resp.StatusCode)
    }
    return lastErr
}

// TODO: 未来可切换/扩展到官方 go-kong 客户端以支持更多资源
var ErrNotImplemented = errors.New("未实现")
