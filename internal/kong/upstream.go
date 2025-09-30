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

type Upstream struct {
    ID   string `json:"id,omitempty"`
    Name string `json:"name,omitempty"`
}

type upstreamList struct { Data []Upstream `json:"data"` }

func (c *Client) GetUpstream(ctx context.Context, name string) (*Upstream, bool, error) {
    var up Upstream
    resp, err := c.do(ctx, http.MethodGet, "/upstreams/"+name, nil)
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
    if err := json.Unmarshal(data, &up); err != nil {
        snippet := strings.TrimSpace(string(data))
        if len(snippet) > 256 { snippet = snippet[:256] + "..." }
        return nil, false, fmt.Errorf("解析 JSON 失败：%v。请检查 --admin-url 是否正确。响应片段：%s", err, snippet)
    }
    return &up, true, nil
}

func (c *Client) CreateOrUpdateUpstream(ctx context.Context, name string) (string, Upstream, error) {
    if name == "" {
        return "", Upstream{}, fmt.Errorf("upstream 名称不能为空")
    }
    if _, ok, err := c.GetUpstream(ctx, name); err != nil {
        return "", Upstream{}, err
    } else if !ok {
        payload := Upstream{Name: name}
        var out Upstream
        if err := c.doJSON(ctx, http.MethodPost, "/upstreams", payload, &out); err != nil {
            return "", Upstream{}, err
        }
        return "create", out, nil
    }
    // 简化：存在则认为已同步（如需变更哈希策略可扩展 PATCH）
    return "update", Upstream{Name: name}, nil
}

// ListUpstreams 列出所有 Upstream（简单版，不处理分页，默认 size=1000）
func (c *Client) ListUpstreams(ctx context.Context) ([]Upstream, error) {
    resp, err := c.do(ctx, http.MethodGet, "/upstreams?size=1000", nil)
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
    var lst upstreamList
    if err := json.Unmarshal(data, &lst); err != nil {
        snippet := strings.TrimSpace(string(data))
        if len(snippet) > 256 { snippet = snippet[:256] + "..." }
        return nil, fmt.Errorf("解析 JSON 失败：%v。请检查 --admin-url 是否正确。响应片段：%s", err, snippet)
    }
    return lst.Data, nil
}
