package apisix

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client 通过 APISIX Admin API 下发路由（数据面·中继）。
// 二期使用：当接口「上线 + 中继」时同步 route/upstream，下线时删除。
type Client struct {
	admin    string // 例如 http://localhost:9180
	adminKey string
	http     *http.Client
}

func New(admin, adminKey string) *Client {
	return &Client{admin: admin, adminKey: adminKey, http: &http.Client{Timeout: 5 * time.Second}}
}

// Enabled 是否配置了 APISIX（未配置则路由同步为 no-op，一期直连模式下可用）。
func (c *Client) Enabled() bool { return c.admin != "" }

// UpsertRoute 将一个接口同步为 APISIX route。upstream 指向 frp 内网映射地址。
func (c *Client) UpsertRoute(ctx context.Context, routeID, method, path, upstream string, authRequired bool, rateLimit int) error {
	if !c.Enabled() {
		return nil
	}
	plugins := map[string]any{}
	if authRequired {
		plugins["key-auth"] = map[string]any{}
	}
	if rateLimit > 0 {
		plugins["limit-count"] = map[string]any{
			"count": rateLimit, "time_window": 60, "rejected_code": 429, "key": "remote_addr",
		}
	}
	body := map[string]any{
		"uri":     path,
		"methods": []string{method},
		"plugins": plugins,
		"upstream": map[string]any{
			"type":  "roundrobin",
			"nodes": map[string]int{upstream: 1},
		},
	}
	return c.do(ctx, http.MethodPut, "/apisix/admin/routes/"+routeID, body)
}

// DeleteRoute 接口下线时删除路由。
func (c *Client) DeleteRoute(ctx context.Context, routeID string) error {
	if !c.Enabled() {
		return nil
	}
	return c.do(ctx, http.MethodDelete, "/apisix/admin/routes/"+routeID, nil)
}

func (c *Client) do(ctx context.Context, method, path string, body any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.admin+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-KEY", c.adminKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("apisix admin %s %s -> %d", method, path, resp.StatusCode)
	}
	return nil
}
