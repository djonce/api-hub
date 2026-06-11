package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 心跳自愈依赖 postJSON 返回带状态码的 httpStatusError，且能用 errors.As 取出 404。
func TestPostJSON_StatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"unknown instance_uid: x"}`))
	}))
	defer srv.Close()

	err := postJSON(srv.URL, map[string]string{"instance_uid": "x"}, nil)
	if err == nil {
		t.Fatal("非 2xx 应返回错误")
	}
	var he *httpStatusError
	if !errors.As(err, &he) {
		t.Fatalf("应为 *httpStatusError, got %T", err)
	}
	if he.code != http.StatusNotFound {
		t.Errorf("code=%d, want 404", he.code)
	}
}

func TestPostJSON_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	var out struct {
		OK bool `json:"ok"`
	}
	if err := postJSON(srv.URL, map[string]string{}, &out); err != nil {
		t.Fatalf("成功响应不应报错: %v", err)
	}
	if !out.OK {
		t.Error("应解析出 ok=true")
	}
}
