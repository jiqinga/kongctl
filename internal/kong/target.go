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

type Target struct {
    ID     string `json:"id,omitempty"`
    Target string `json:"target"` // host:port
    Weight int    `json:"weight,omitempty"`
}

func (c *Client) AddTarget(ctx context.Context, upstreamName, target string, weight int) (Target, error) {
    if upstreamName == "" || target == "" {
        return Target{}, fmt.Errorf("必须提供 upstream 与 target")
    }
    payload := Target{Target: target, Weight: weight}
    var out Target
    if err := c.doJSON(ctx, http.MethodPost, "/upstreams/"+upstreamName+"/targets", payload, &out); err != nil {
        return Target{}, err
    }
    return out, nil
}

type targetList struct { Data []Target `json:"data"` }

func (c *Client) ListTargets(ctx context.Context, upstreamName string) ([]Target, error) {
    resp, err := c.do(ctx, http.MethodGet, "/upstreams/"+upstreamName+"/targets", nil)
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
    var tl targetList
    if err := json.Unmarshal(data, &tl); err != nil {
        snippet := strings.TrimSpace(string(data))
        if len(snippet) > 256 { snippet = snippet[:256] + "..." }
        return nil, fmt.Errorf("解析 JSON 失败：%v。请检查 --admin-url 是否正确。响应片段：%s", err, snippet)
    }
    return tl.Data, nil
}

// EnsureTarget 若不存在则添加；若存在且权重不同，再添加同名 Target 以覆盖（Kong 将采用最新记录）。
func (c *Client) EnsureTarget(ctx context.Context, upstreamName, target string, weight int) (added bool, err error) {
    list, err := c.ListTargets(ctx, upstreamName)
    if err != nil { return false, err }
    for i := range list {
        if list[i].Target == target && (list[i].Weight == weight || weight == 0) {
            return false, nil
        }
    }
    _, err = c.AddTarget(ctx, upstreamName, target, weight)
    if err != nil { return false, err }
    return true, nil
}
