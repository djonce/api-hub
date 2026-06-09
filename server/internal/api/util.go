package api

import (
	"crypto/rand"
	"encoding/hex"
)

// newAPIKey 生成消费方 API Key。
func newAPIKey() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "ak_fallback"
	}
	return "ak_" + hex.EncodeToString(b)
}
