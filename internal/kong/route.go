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

type Route struct {
    ID      string   `json:"id,omitempty"`
    Name    string   `json:"name,omitempty"`
    Hosts   []string `json:"hosts,omitempty"`
    Paths   []string `json:"paths,omitempty"`
    Methods []string `json:"methods,omitempty"`
    Protocols []string `json:"protocols,omitempty"`
    PreserveHost *bool  `json:"preserve_host,omitempty"`
    RegexPriority int   `json:"regex_priority,omitempty"`
    HTTPSRedirectStatusCode int `json:"https_redirect_status_code,omitempty"`
    RequestBuffering *bool `json:"request_buffering,omitempty"`
    ResponseBuffering *bool `json:"response_buffering,omitempty"`
    Headers map[string][]string `json:"headers,omitempty"`
    Snis   []string `json:"snis,omitempty"`
    Tags   []string `json:"tags,omitempty"`
    PathHandling string `json:"path_handling,omitempty"`
    StripPath *bool  `json:"strip_path,omitempty"`
    Service struct {
        ID string `json:"id,omitempty"`
        Name string `json:"name,omitempty"`
    } `json:"service,omitempty"`
}

type routeList struct {
    Data []Route `json:"data"`
}

func (c *Client) GetRoute(ctx context.Context, name string) (*Route, bool, error) {
    var rt Route
    resp, err := c.do(ctx, http.MethodGet, "/routes/"+name, nil)
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
    if err := json.Unmarshal(data, &rt); err != nil {
        snippet := strings.TrimSpace(string(data))
        if len(snippet) > 256 { snippet = snippet[:256] + "..." }
        return nil, false, fmt.Errorf("解析 JSON 失败：%v。请检查 --admin-url 是否正确。响应片段：%s", err, snippet)
    }
    return &rt, true, nil
}

// ListRoutes 列出所有 Route（简单版，不处理分页，默认 size=1000）
func (c *Client) ListRoutes(ctx context.Context) ([]Route, error) {
    resp, err := c.do(ctx, http.MethodGet, "/routes?size=1000", nil)
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
    var lst routeList
    if err := json.Unmarshal(data, &lst); err != nil {
        snippet := strings.TrimSpace(string(data))
        if len(snippet) > 256 { snippet = snippet[:256] + "..." }
        return nil, fmt.Errorf("解析 JSON 失败：%v。请检查 --admin-url 是否正确。响应片段：%s", err, snippet)
    }
    return lst.Data, nil
}

// CreateOrUpdateRoute 幂等创建/更新路由。
// 需要先传入 Service 信息（至少包含 ID 或 Name）。
func (c *Client) CreateOrUpdateRoute(ctx context.Context, desired Route) (action string, rt Route, err error) {
    if desired.Name == "" {
        return "", Route{}, fmt.Errorf("route 需要 name")
    }
    // 若 service 只有 Name，转为 ID
    if desired.Service.ID == "" && desired.Service.Name != "" {
        svc, ok, err := c.GetService(ctx, desired.Service.Name)
        if err != nil {
            return "", Route{}, err
        }
        if !ok {
            return "", Route{}, fmt.Errorf("关联的 Service 不存在：%s", desired.Service.Name)
        }
        desired.Service.ID = svc.ID
    }
    if desired.Service.ID == "" {
        return "", Route{}, fmt.Errorf("route 需要关联 service id 或 name")
    }

    if cur, ok, err := c.GetRoute(ctx, desired.Name); err != nil {
        return "", Route{}, err
    } else if !ok {
        // 创建
        if err := c.doJSON(ctx, http.MethodPost, "/routes", desired, &rt); err != nil {
            return "", Route{}, err
        }
        return "create", rt, nil
    } else {
        // 更新（直接 PATCH 目标字段）
        payload := map[string]any{
            "hosts": desired.Hosts,
            "paths": desired.Paths,
            "methods": desired.Methods,
        }
        if len(desired.Protocols) > 0 { payload["protocols"] = desired.Protocols }
        if desired.PreserveHost != nil { payload["preserve_host"] = *desired.PreserveHost }
        if desired.RegexPriority != 0 { payload["regex_priority"] = desired.RegexPriority }
        if desired.HTTPSRedirectStatusCode != 0 { payload["https_redirect_status_code"] = desired.HTTPSRedirectStatusCode }
        if desired.RequestBuffering != nil { payload["request_buffering"] = *desired.RequestBuffering }
        if desired.ResponseBuffering != nil { payload["response_buffering"] = *desired.ResponseBuffering }
        if len(desired.Headers) > 0 { payload["headers"] = desired.Headers }
        if len(desired.Snis) > 0 { payload["snis"] = desired.Snis }
        if len(desired.Tags) > 0 { payload["tags"] = desired.Tags }
        if desired.PathHandling != "" {
            payload["path_handling"] = desired.PathHandling
        }
        if desired.StripPath != nil {
            payload["strip_path"] = *desired.StripPath
        }
        if desired.Service.ID != "" {
            payload["service"] = map[string]any{"id": desired.Service.ID}
        }
        if err := c.doJSON(ctx, http.MethodPatch, "/routes/"+cur.Name, payload, &rt); err != nil {
            return "", Route{}, err
        }
        return "update", rt, nil
    }
}
