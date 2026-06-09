// demo-service：一个最小业务服务，暴露 /health、/openapi.json 与若干业务接口。
// 配合 agent 即可演示「自动注册 + 接口同步」。
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	addr := ":" + env("PORT", "9000")
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("GET /api/time", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"now": time.Now().Format(time.RFC3339)})
	})

	mux.HandleFunc("POST /api/echo", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeJSON(w, map[string]any{"echo": body})
	})

	mux.HandleFunc("GET /openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(openapiDoc))
	})

	log.Printf("demo-service listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

const openapiDoc = `{
  "openapi": "3.0.0",
  "info": {"title": "demo-service", "version": "1.0.0"},
  "paths": {
    "/api/time": {
      "get": {"summary": "返回当前服务器时间", "tags": ["util"]}
    },
    "/api/echo": {
      "post": {
        "summary": "回显请求体",
        "tags": ["util"],
        "security": [{"apiKey": []}]
      }
    }
  }
}`
