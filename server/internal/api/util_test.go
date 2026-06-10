package api

import (
	"strings"
	"testing"
)

func TestNewAPIKey(t *testing.T) {
	k1 := newAPIKey()
	k2 := newAPIKey()
	if !strings.HasPrefix(k1, "ak_") {
		t.Errorf("key 应以 ak_ 开头, got %q", k1)
	}
	// "ak_" + 24 字节的 hex(48) = 51
	if len(k1) != 51 {
		t.Errorf("key 长度应为 51, got %d (%q)", len(k1), k1)
	}
	if k1 == k2 {
		t.Errorf("两次生成的 key 不应相同: %q", k1)
	}
}

func TestApisixUsername(t *testing.T) {
	cases := []struct {
		name  string
		keyID int64
		want  string
	}{
		{"my-consumer", 5, "my_consumer_5"}, // 连字符 -> 下划线
		{"abc_123", 1, "abc_123_1"},         // 合法字符保留
		{"a.b/c", 2, "a_b_c_2"},             // 点和斜杠 -> 下划线
		{"", 3, "consumer_3"},               // 空名 -> consumer
		{"用户", 7, "___7"},                   // 非 ASCII 全部 -> 下划线（2 个），再加分隔符 _ 与 keyID
	}
	for _, c := range cases {
		if got := apisixUsername(c.name, c.keyID); got != c.want {
			t.Errorf("apisixUsername(%q,%d)=%q, want %q", c.name, c.keyID, got, c.want)
		}
	}
}

func TestEnsureAPIKeyScheme(t *testing.T) {
	doc := map[string]any{}
	ensureAPIKeyScheme(doc)

	comps, ok := doc["components"].(map[string]any)
	if !ok {
		t.Fatal("应创建 components")
	}
	schemes, ok := comps["securitySchemes"].(map[string]any)
	if !ok {
		t.Fatal("应创建 securitySchemes")
	}
	scheme, ok := schemes["apiKeyAuth"].(map[string]any)
	if !ok {
		t.Fatal("应注入 apiKeyAuth")
	}
	if scheme["type"] != "apiKey" || scheme["in"] != "header" || scheme["name"] != "apikey" {
		t.Errorf("apiKeyAuth 内容不符: %+v", scheme)
	}
}

func TestEnsureAPIKeyScheme_PreservesExisting(t *testing.T) {
	doc := map[string]any{
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"existing": map[string]any{"type": "http"},
			},
		},
	}
	ensureAPIKeyScheme(doc)
	schemes := doc["components"].(map[string]any)["securitySchemes"].(map[string]any)
	if _, ok := schemes["existing"]; !ok {
		t.Error("不应丢失已有的 securityScheme")
	}
	if _, ok := schemes["apiKeyAuth"]; !ok {
		t.Error("应追加 apiKeyAuth")
	}
}

// TestParseAccessLogPayload_SingleObject 是针对真实 bug 的回归测试：
// APISIX http-logger 在 batch_max_size=1 时推送的是单个 JSON 对象而非数组，
// 解析必须能兼容，否则访问日志会被全部丢弃、监控无数据。
func TestParseAccessLogPayload_SingleObject(t *testing.T) {
	raw := []byte(`{"route_id":"1","latency":3.2,"consumer":"acme",
		"request":{"uri":"/r/1/api/time","method":"GET"},
		"response":{"status":200}}`)
	entries, err := parseAccessLogPayload(raw)
	if err != nil {
		t.Fatalf("解析单对象不应报错: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("应解析出 1 条, got %d", len(entries))
	}
	e := entries[0]
	if e.RouteID != "1" || e.Request.Method != "GET" || e.Request.URI != "/r/1/api/time" ||
		e.Response.Status != 200 || e.Latency != 3.2 || e.Consumer != "acme" {
		t.Errorf("字段解析不符: %+v", e)
	}
}

func TestParseAccessLogPayload_Array(t *testing.T) {
	raw := []byte(`[
		{"route_id":"1","request":{"uri":"/a","method":"GET"},"response":{"status":200}},
		{"route_id":"2","request":{"uri":"/b","method":"POST"},"response":{"status":500}}
	]`)
	entries, err := parseAccessLogPayload(raw)
	if err != nil {
		t.Fatalf("解析数组不应报错: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("应解析出 2 条, got %d", len(entries))
	}
	if entries[1].Response.Status != 500 || entries[1].Request.Method != "POST" {
		t.Errorf("第二条解析不符: %+v", entries[1])
	}
}

func TestParseAccessLogPayload_Invalid(t *testing.T) {
	if _, err := parseAccessLogPayload([]byte(`not-json`)); err == nil {
		t.Error("非法 JSON 应返回错误")
	}
}

func TestParseAccessLogPayload_EmptyArray(t *testing.T) {
	entries, err := parseAccessLogPayload([]byte(`[]`))
	if err != nil {
		t.Fatalf("空数组不应报错: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("空数组应解析出 0 条, got %d", len(entries))
	}
}
