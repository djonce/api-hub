package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// parseAccessLogPayload 解析 APISIX http-logger 推送的访问日志。
// APISIX 既可能批量推送对象数组（batch_max_size>1），也可能推送单个对象（batch_max_size=1），两者都要兼容。
func parseAccessLogPayload(raw []byte) ([]apisixLogEntry, error) {
	var entries []apisixLogEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		var one apisixLogEntry
		if err2 := json.Unmarshal(raw, &one); err2 != nil {
			// 返回数组解析的错误，对调用方更直观。
			return nil, err
		}
		entries = []apisixLogEntry{one}
	}
	return entries, nil
}

// newAPIKey 生成消费方 API Key。
func newAPIKey() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "ak_fallback"
	}
	return "ak_" + hex.EncodeToString(b)
}

// apisixUsername 由消费方名与 Key ID 组成 APISIX consumer 用户名。
// APISIX 用户名只允许字母/数字/下划线，故对名称做清洗；附加 Key ID 以支持一个消费方多把 Key。
func apisixUsername(name string, keyID int64) string {
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	clean := b.String()
	if clean == "" {
		clean = "consumer"
	}
	return fmt.Sprintf("%s_%d", clean, keyID)
}

// ensureAPIKeyScheme 向 OpenAPI 文档注入 apikey 安全方案（header: apikey），
// 使 Swagger UI 出现 Authorize 按钮，便于调试中继接口。
func ensureAPIKeyScheme(doc map[string]any) {
	comps, _ := doc["components"].(map[string]any)
	if comps == nil {
		comps = map[string]any{}
		doc["components"] = comps
	}
	schemes, _ := comps["securitySchemes"].(map[string]any)
	if schemes == nil {
		schemes = map[string]any{}
		comps["securitySchemes"] = schemes
	}
	if _, exists := schemes["apiKeyAuth"]; !exists {
		schemes["apiKeyAuth"] = map[string]any{
			"type": "apiKey",
			"in":   "header",
			"name": "apikey",
		}
	}
}
