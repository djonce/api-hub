package apisix

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Client 通过 APISIX Admin API 下发路由（数据面·中继）。
// 二期使用：当接口「上线 + 中继」时同步 route/upstream，下线时删除。
type Client struct {
	admin     string // 例如 http://localhost:9180
	adminKey  string
	logIngest string // 控制面访问日志接收地址；非空则给路由挂 http-logger
	http      *http.Client
}

func New(admin, adminKey, logIngest string) *Client {
	return &Client{admin: admin, adminKey: adminKey, logIngest: logIngest, http: &http.Client{Timeout: 5 * time.Second}}
}

// Enabled 是否配置了 APISIX（未配置则路由同步为 no-op，一期直连模式下可用）。
func (c *Client) Enabled() bool { return c.admin != "" }

// RelayPrefix 返回某服务在网关上的中继路径前缀，如 /r/12。
func RelayPrefix(serviceID int64) string { return fmt.Sprintf("/r/%d", serviceID) }

// UpsertRoute 将一个接口同步为 APISIX route。
// 为避免不同服务路径冲突，对外路径为 /r/{serviceID}{path}，再用 proxy-rewrite 还原为原始 path。
// nodes 为该服务全部在线实例的中继上游（roundrobin 负载均衡）。
func (c *Client) UpsertRoute(ctx context.Context, routeID string, serviceID int64, method, path string, nodes map[string]int, authRequired bool, rateLimit int, breaker bool) error {
	if !c.Enabled() {
		return nil
	}
	if len(nodes) == 0 {
		// 无在线实例则下线该路由，避免 502。
		return c.DeleteRoute(ctx, routeID)
	}
	prefix := RelayPrefix(serviceID)
	plugins := map[string]any{
		"proxy-rewrite": map[string]any{
			"regex_uri": []string{"^" + prefix + "(/.*)$", "$1"},
		},
	}
	if authRequired {
		plugins["key-auth"] = map[string]any{}
	}
	if rateLimit > 0 {
		key := "remote_addr"
		if authRequired {
			key = "consumer_name"
		}
		plugins["limit-count"] = map[string]any{
			"count": rateLimit, "time_window": 60, "rejected_code": 429,
			"key_type": "var", "key": key,
		}
	}
	if breaker {
		plugins["api-breaker"] = map[string]any{
			"break_response_code": 502,
			"unhealthy":           map[string]any{"http_statuses": []int{500, 502, 503}, "failures": 3},
			"healthy":             map[string]any{"http_statuses": []int{200}, "successes": 2},
		}
	}
	// 访问日志（三期监控）：把调用日志推送到控制面 /ingest/access。
	if c.logIngest != "" {
		plugins["http-logger"] = map[string]any{
			"uri":            c.logIngest,
			"batch_max_size": 1,
		}
	}
	body := map[string]any{
		"uri":     prefix + path,
		"methods": []string{method},
		"plugins": plugins,
		"upstream": map[string]any{
			"type":  "roundrobin",
			"nodes": nodes,
		},
	}
	return c.do(ctx, http.MethodPut, "/apisix/admin/routes/"+routeID, body)
}

// DeleteRoute 接口下线时删除路由。
func (c *Client) DeleteRoute(ctx context.Context, routeID string) error {
	if !c.Enabled() {
		return nil
	}
	if err := c.do(ctx, http.MethodDelete, "/apisix/admin/routes/"+routeID, nil); err != nil && !is404(err) {
		return err
	}
	return nil
}

// UpsertConsumer 把一个消费方 API Key 同步为 APISIX consumer（key-auth 凭据）。
// quotaPerMin>0 时在消费方维度加 limit-count 配额。
func (c *Client) UpsertConsumer(ctx context.Context, username, key string, quotaPerMin int) error {
	if !c.Enabled() {
		return nil
	}
	plugins := map[string]any{
		"key-auth": map[string]any{"key": key},
	}
	if quotaPerMin > 0 {
		plugins["limit-count"] = map[string]any{
			"count": quotaPerMin, "time_window": 60, "rejected_code": 429,
			"key_type": "var", "key": "consumer_name",
		}
	}
	body := map[string]any{"username": username, "plugins": plugins}
	return c.do(ctx, http.MethodPut, "/apisix/admin/consumers", body)
}

// DeleteConsumer 删除消费方。
func (c *Client) DeleteConsumer(ctx context.Context, username string) error {
	if !c.Enabled() {
		return nil
	}
	if err := c.do(ctx, http.MethodDelete, "/apisix/admin/consumers/"+username, nil); err != nil && !is404(err) {
		return err
	}
	return nil
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

// is404 判断删除时目标不存在的错误（幂等删除可忽略）。
func is404(err error) bool {
	return err != nil && strings.Contains(err.Error(), "-> 404")
}
