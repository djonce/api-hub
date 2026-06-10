package apisix

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// captured 记录 mock Admin API 收到的一次请求。
type captured struct {
	method string
	path   string
	apiKey string
	body   map[string]any
}

// mockAdmin 启动一个记录请求的假 APISIX Admin API。
func mockAdmin(t *testing.T, status int) (*httptest.Server, *[]captured) {
	t.Helper()
	var calls []captured
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := captured{method: r.Method, path: r.URL.Path, apiKey: r.Header.Get("X-API-KEY")}
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			_ = json.Unmarshal(b, &c.body)
		}
		calls = append(calls, c)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestRelayPrefix(t *testing.T) {
	if got := RelayPrefix(12); got != "/r/12" {
		t.Errorf("RelayPrefix(12)=%q, want /r/12", got)
	}
}

func TestEnabled(t *testing.T) {
	if New("", "k", "").Enabled() {
		t.Error("未配置 admin 时 Enabled() 应为 false")
	}
	if !New("http://x", "k", "").Enabled() {
		t.Error("配置 admin 后 Enabled() 应为 true")
	}
}

// 未配置 APISIX 时所有写操作应为 no-op（不发 HTTP 请求）。
func TestUpsertRoute_NoopWhenDisabled(t *testing.T) {
	srv, calls := mockAdmin(t, 200)
	c := New("", "k", "") // admin 为空 -> disabled
	_ = srv
	if err := c.UpsertRoute(context.Background(), "1", 1, "GET", "/api/time",
		map[string]int{"h:9000": 1}, false, 0, false); err != nil {
		t.Fatalf("disabled 下不应报错: %v", err)
	}
	if len(*calls) != 0 {
		t.Errorf("disabled 下不应发起请求, got %d", len(*calls))
	}
}

func TestUpsertRoute_Basic(t *testing.T) {
	srv, calls := mockAdmin(t, 201)
	c := New(srv.URL, "secret-key", "")

	err := c.UpsertRoute(context.Background(), "7", 1, "GET", "/api/time",
		map[string]int{"backend:9000": 1}, false, 0, false)
	if err != nil {
		t.Fatalf("UpsertRoute: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("应发起 1 次请求, got %d", len(*calls))
	}
	got := (*calls)[0]
	if got.method != http.MethodPut {
		t.Errorf("method=%s, want PUT", got.method)
	}
	if got.path != "/apisix/admin/routes/7" {
		t.Errorf("path=%s, want /apisix/admin/routes/7", got.path)
	}
	if got.apiKey != "secret-key" {
		t.Errorf("缺少正确的 X-API-KEY: %q", got.apiKey)
	}
	if got.body["uri"] != "/r/1/api/time" {
		t.Errorf("uri=%v, want /r/1/api/time", got.body["uri"])
	}
	methods, _ := got.body["methods"].([]any)
	if len(methods) != 1 || methods[0] != "GET" {
		t.Errorf("methods=%v, want [GET]", got.body["methods"])
	}
	plugins, _ := got.body["plugins"].(map[string]any)
	pr, _ := plugins["proxy-rewrite"].(map[string]any)
	rx, _ := pr["regex_uri"].([]any)
	if len(rx) != 2 || rx[0] != "^/r/1(/.*)$" || rx[1] != "$1" {
		t.Errorf("proxy-rewrite.regex_uri=%v, want [^/r/1(/.*)$ $1]", pr["regex_uri"])
	}
	// 未要求鉴权/限流/熔断/日志，对应插件都不应出现
	for _, p := range []string{"key-auth", "limit-count", "api-breaker", "http-logger"} {
		if _, ok := plugins[p]; ok {
			t.Errorf("不应出现插件 %s", p)
		}
	}
	up, _ := got.body["upstream"].(map[string]any)
	if up["type"] != "roundrobin" {
		t.Errorf("upstream.type=%v, want roundrobin", up["type"])
	}
	nodes, _ := up["nodes"].(map[string]any)
	if nodes["backend:9000"] != float64(1) {
		t.Errorf("upstream.nodes=%v, want {backend:9000:1}", up["nodes"])
	}
}

func TestUpsertRoute_AllPlugins(t *testing.T) {
	srv, calls := mockAdmin(t, 200)
	c := New(srv.URL, "k", "http://ctrl:8080/api/v1/ingest/access")

	err := c.UpsertRoute(context.Background(), "9", 2, "POST", "/api/echo",
		map[string]int{"backend:9000": 1}, true /*auth*/, 60 /*rate*/, true /*breaker*/)
	if err != nil {
		t.Fatalf("UpsertRoute: %v", err)
	}
	plugins := (*calls)[0].body["plugins"].(map[string]any)

	if _, ok := plugins["key-auth"]; !ok {
		t.Error("authRequired 时应启用 key-auth")
	}
	if _, ok := plugins["api-breaker"]; !ok {
		t.Error("breaker 时应启用 api-breaker")
	}
	hl, ok := plugins["http-logger"].(map[string]any)
	if !ok {
		t.Fatal("配置 logIngest 时应启用 http-logger")
	}
	if hl["uri"] != "http://ctrl:8080/api/v1/ingest/access" {
		t.Errorf("http-logger.uri=%v", hl["uri"])
	}
	lc, ok := plugins["limit-count"].(map[string]any)
	if !ok {
		t.Fatal("rateLimit>0 时应启用 limit-count")
	}
	if lc["count"] != float64(60) {
		t.Errorf("limit-count.count=%v, want 60", lc["count"])
	}
	// 需鉴权时按 consumer 维度限流
	if lc["key"] != "consumer_name" {
		t.Errorf("authRequired 下 limit-count.key=%v, want consumer_name", lc["key"])
	}
}

func TestUpsertRoute_LimitByIPWhenNoAuth(t *testing.T) {
	srv, calls := mockAdmin(t, 200)
	c := New(srv.URL, "k", "")
	if err := c.UpsertRoute(context.Background(), "1", 1, "GET", "/x",
		map[string]int{"h:1": 1}, false, 30, false); err != nil {
		t.Fatal(err)
	}
	lc := (*calls)[0].body["plugins"].(map[string]any)["limit-count"].(map[string]any)
	if lc["key"] != "remote_addr" {
		t.Errorf("无鉴权下 limit-count.key=%v, want remote_addr", lc["key"])
	}
}

// 无在线实例（nodes 为空）时应转为删除路由，避免 502。
func TestUpsertRoute_EmptyNodesDeletes(t *testing.T) {
	srv, calls := mockAdmin(t, 200)
	c := New(srv.URL, "k", "")
	if err := c.UpsertRoute(context.Background(), "5", 1, "GET", "/x",
		map[string]int{}, false, 0, false); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].method != http.MethodDelete {
		t.Fatalf("空 nodes 应触发一次 DELETE, got %+v", *calls)
	}
	if (*calls)[0].path != "/apisix/admin/routes/5" {
		t.Errorf("delete path=%s", (*calls)[0].path)
	}
}

// 删除不存在的路由（404）应被幂等忽略。
func TestDeleteRoute_404Ignored(t *testing.T) {
	srv, _ := mockAdmin(t, 404)
	c := New(srv.URL, "k", "")
	if err := c.DeleteRoute(context.Background(), "404"); err != nil {
		t.Errorf("404 删除应被忽略, got %v", err)
	}
}

func TestUpsertConsumer(t *testing.T) {
	srv, calls := mockAdmin(t, 200)
	c := New(srv.URL, "k", "")

	if err := c.UpsertConsumer(context.Background(), "acme_1", "ak_xyz", 100); err != nil {
		t.Fatalf("UpsertConsumer: %v", err)
	}
	got := (*calls)[0]
	if got.method != http.MethodPut || got.path != "/apisix/admin/consumers" {
		t.Errorf("want PUT /apisix/admin/consumers, got %s %s", got.method, got.path)
	}
	if got.body["username"] != "acme_1" {
		t.Errorf("username=%v", got.body["username"])
	}
	plugins := got.body["plugins"].(map[string]any)
	ka, _ := plugins["key-auth"].(map[string]any)
	if ka["key"] != "ak_xyz" {
		t.Errorf("key-auth.key=%v, want ak_xyz", ka["key"])
	}
	lc, ok := plugins["limit-count"].(map[string]any)
	if !ok || lc["count"] != float64(100) {
		t.Errorf("quota>0 时应有 limit-count.count=100, got %v", plugins["limit-count"])
	}
}

func TestUpsertConsumer_NoQuotaNoLimit(t *testing.T) {
	srv, calls := mockAdmin(t, 200)
	c := New(srv.URL, "k", "")
	if err := c.UpsertConsumer(context.Background(), "u_1", "ak", 0); err != nil {
		t.Fatal(err)
	}
	plugins := (*calls)[0].body["plugins"].(map[string]any)
	if _, ok := plugins["limit-count"]; ok {
		t.Error("quota=0 时不应有 limit-count")
	}
}

// Admin API 返回 5xx 时应返回错误。
func TestUpsertRoute_ServerError(t *testing.T) {
	srv, _ := mockAdmin(t, 500)
	c := New(srv.URL, "k", "")
	if err := c.UpsertRoute(context.Background(), "1", 1, "GET", "/x",
		map[string]int{"h:1": 1}, false, 0, false); err == nil {
		t.Error("Admin 返回 500 时应报错")
	}
}
