package kong

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "strings"
)

type Service struct {
    ID       string `json:"id,omitempty"`
    Name     string `json:"name,omitempty"`
    URL      string `json:"url,omitempty"`
    Protocol string `json:"protocol,omitempty"`
    Host     string `json:"host,omitempty"`
    Port     int    `json:"port,omitempty"`
    Path     string `json:"path,omitempty"`
    Retries        int `json:"retries,omitempty"`
    ConnectTimeout int `json:"connect_timeout,omitempty"`
    ReadTimeout    int `json:"read_timeout,omitempty"`
    WriteTimeout   int `json:"write_timeout,omitempty"`
}

type serviceList struct {
    Data []Service `json:"data"`
}

// GetService 通过名称查询 Service（若不存在返回 (nil, false, nil)）
func (c *Client) GetService(ctx context.Context, name string) (*Service, bool, error) {
    var svc Service
    resp, err := c.do(ctx, http.MethodGet, "/services/"+name, nil)
    if err != nil {
        return nil, false, err
    }
    defer resp.Body.Close()
    if resp.StatusCode == http.StatusNotFound {
        return nil, false, nil
    }
    if resp.StatusCode/100 != 2 {
        return nil, false, fmt.Errorf("HTTP %d", resp.StatusCode)
    }
    data, _ := io.ReadAll(resp.Body)
    ct := resp.Header.Get("Content-Type")
    if ct != "" && !strings.Contains(strings.ToLower(ct), "json") || (len(data) > 0 && bytes.HasPrefix(bytes.TrimSpace(data), []byte("<"))) {
        snippet := strings.TrimSpace(string(data))
        if len(snippet) > 256 { snippet = snippet[:256] + "..." }
        return nil, false, fmt.Errorf("响应非 JSON（Content-Type=%s）。请检查 --admin-url 是否指向 Kong Admin API。响应片段：%s", ct, snippet)
    }
    if err := json.Unmarshal(data, &svc); err != nil {
        snippet := strings.TrimSpace(string(data))
        if len(snippet) > 256 { snippet = snippet[:256] + "..." }
        return nil, false, fmt.Errorf("解析 JSON 失败：%v。请检查 --admin-url 是否正确。响应片段：%s", err, snippet)
    }
    return &svc, true, nil
}

// ListServices 列出所有 Service（简单版，不处理分页，默认 size=1000）
func (c *Client) ListServices(ctx context.Context) ([]Service, error) {
    resp, err := c.do(ctx, http.MethodGet, "/services?size=1000", nil)
    if err != nil { return nil, err }
    defer resp.Body.Close()
    if resp.StatusCode/100 != 2 { return nil, fmt.Errorf("HTTP %d", resp.StatusCode) }
    data, _ := io.ReadAll(resp.Body)
    ct := resp.Header.Get("Content-Type")
    if ct != "" && !strings.Contains(strings.ToLower(ct), "json") || (len(data) > 0 && bytes.HasPrefix(bytes.TrimSpace(data), []byte("<"))) {
        snippet := strings.TrimSpace(string(data))
        if len(snippet) > 256 { snippet = snippet[:256] + "..." }
        return nil, fmt.Errorf("响应非 JSON（Content-Type=%s）。请检查 --admin-url 是否指向 Kong Admin API。响应片段：%s", ct, snippet)
    }
    var lst serviceList
    if err := json.Unmarshal(data, &lst); err != nil {
        snippet := strings.TrimSpace(string(data))
        if len(snippet) > 256 { snippet = snippet[:256] + "..." }
        return nil, fmt.Errorf("解析 JSON 失败：%v。请检查 --admin-url 是否正确。响应片段：%s", err, snippet)
    }
    return lst.Data, nil
}

// CreateOrUpdateService 幂等创建/更新
func (c *Client) CreateOrUpdateService(ctx context.Context, name, url string) (action string, svc Service, err error) {
    if name == "" || url == "" {
        return "", Service{}, fmt.Errorf("name 与 url 不能为空")
    }
    if cur, ok, err := c.GetService(ctx, name); err != nil {
        return "", Service{}, err
    } else if !ok {
        // 创建
        payload := Service{Name: name, URL: url}
        if err := c.doJSON(ctx, http.MethodPost, "/services", payload, &svc); err != nil {
            return "", Service{}, err
        }
        return "create", svc, nil
    } else {
        // 更新：若 URL 相同则跳过
        // 注意：GET 返回可能没有 url 字段，Kong 会拆成 protocol/host/port/path；这里直接 patch url
        payload := map[string]any{"url": url}
        // 简易策略：直接 PATCH，若无变更 Kong 会返回 200
        if err := c.doJSON(ctx, http.MethodPatch, "/services/"+cur.Name, payload, &svc); err != nil {
            return "", Service{}, err
        }
        return "update", svc, nil
    }
}

// CreateOrUpdateServiceViaUpstream 使用 Upstream 名称配置 Service
func (c *Client) CreateOrUpdateServiceViaUpstream(ctx context.Context, name, upstreamName, protocol string, port int, path string) (action string, svc Service, err error) {
    if name == "" || upstreamName == "" {
        return "", Service{}, fmt.Errorf("name 与 upstreamName 不能为空")
    }
    if protocol == "" { protocol = "http" }
    if port == 0 { if protocol == "https" { port = 443 } else { port = 80 } }

    if _, ok, err := c.GetService(ctx, name); err != nil {
        return "", Service{}, err
    } else if !ok {
        payload := Service{Name: name, Protocol: protocol, Host: upstreamName, Port: port, Path: path}
        if err := c.doJSON(ctx, http.MethodPost, "/services", payload, &svc); err != nil {
            return "", Service{}, err
        }
        return "create", svc, nil
    }
    payload := map[string]any{
        "protocol": protocol,
        "host": upstreamName,
        "port": port,
        "path": path,
    }
    if err := c.doJSON(ctx, http.MethodPatch, "/services/"+name, payload, &svc); err != nil {
        return "", Service{}, err
    }
    return "update", svc, nil
}

// UpdateServiceExtras 针对常用可选字段做 PATCH（仅当参数>0时才下发）
func (c *Client) UpdateServiceExtras(ctx context.Context, name string, retries, connectTimeout, readTimeout, writeTimeout int) (svc Service, err error) {
    payload := map[string]any{}
    if retries > 0 { payload["retries"] = retries }
    if connectTimeout > 0 { payload["connect_timeout"] = connectTimeout }
    if readTimeout > 0 { payload["read_timeout"] = readTimeout }
    if writeTimeout > 0 { payload["write_timeout"] = writeTimeout }
    if len(payload) == 0 {
        return Service{}, nil
    }
    if err := c.doJSON(ctx, http.MethodPatch, "/services/"+name, payload, &svc); err != nil {
        return Service{}, err
    }
    return svc, nil
}
