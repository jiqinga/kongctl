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

func (c *Client) endpoint(path string) string {
    return strings.TrimRight(c.cfg.AdminURL, "/") + path
}

func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
    var reader io.Reader
    if body != nil {
        b, err := json.Marshal(body)
        if err != nil {
            return nil, err
        }
        reader = bytes.NewReader(b)
    }
    req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), reader)
    if err != nil {
        return nil, err
    }
    if body != nil {
        req.Header.Set("Content-Type", "application/json")
    }
    if c.cfg.Token != "" {
        req.Header.Set("Kong-Admin-Token", c.cfg.Token)
        req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
    }
    return c.client.Do(req)
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any) error {
    resp, err := c.do(ctx, method, path, body)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode/100 != 2 {
        b, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
    }
    if out != nil {
        // 读取响应体以便在非 JSON 情况下提供友好错误提示
        data, _ := io.ReadAll(resp.Body)
        ct := resp.Header.Get("Content-Type")
        // 粗略判断：Content-Type 非 JSON 或内容疑似 HTML
        if ct != "" && !strings.Contains(strings.ToLower(ct), "json") || (len(data) > 0 && (bytes.HasPrefix(bytes.TrimSpace(data), []byte("<")))) {
            snippet := strings.TrimSpace(string(data))
            if len(snippet) > 256 { snippet = snippet[:256] + "..." }
            return fmt.Errorf("响应非 JSON（Content-Type=%s）。请检查 --admin-url 是否指向 Kong Admin API。响应片段：%s", ct, snippet)
        }
        if len(bytes.TrimSpace(data)) == 0 {
            return nil
        }
        if err := json.Unmarshal(data, out); err != nil {
            snippet := strings.TrimSpace(string(data))
            if len(snippet) > 256 { snippet = snippet[:256] + "..." }
            return fmt.Errorf("解析 JSON 失败：%v。请检查 --admin-url 是否正确。响应片段：%s", err, snippet)
        }
    }
    return nil
}
