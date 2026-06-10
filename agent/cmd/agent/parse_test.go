package main

import "testing"

const sampleOpenAPI = `{
  "openapi": "3.0.0",
  "paths": {
    "/api/time": {
      "get": {"summary": "当前时间", "tags": ["clock"]}
    },
    "/api/echo": {
      "post": {"summary": "回显", "tags": ["util"], "security": [{"apiKeyAuth": []}]},
      "parameters": []
    }
  }
}`

func find(apis []regAPI, method, path string) (regAPI, bool) {
	for _, a := range apis {
		if a.Method == method && a.Path == path {
			return a, true
		}
	}
	return regAPI{}, false
}

func TestParseOpenAPI(t *testing.T) {
	apis, err := parseOpenAPI([]byte(sampleOpenAPI))
	if err != nil {
		t.Fatalf("parseOpenAPI: %v", err)
	}
	if len(apis) != 2 {
		t.Fatalf("应解析出 2 个接口, got %d: %+v", len(apis), apis)
	}

	get, ok := find(apis, "GET", "/api/time")
	if !ok {
		t.Fatal("缺少 GET /api/time")
	}
	if get.Summary != "当前时间" || get.Group != "clock" || get.AuthRequired {
		t.Errorf("GET /api/time 字段不符: %+v", get)
	}

	post, ok := find(apis, "POST", "/api/echo")
	if !ok {
		t.Fatal("缺少 POST /api/echo")
	}
	// 带 security 的接口应识别为需要鉴权
	if !post.AuthRequired {
		t.Errorf("POST /api/echo 应为需鉴权: %+v", post)
	}
	if post.Group != "util" {
		t.Errorf("group=%q, want util", post.Group)
	}
}

// 非 HTTP 方法的键（如 parameters/description）不应被当作接口。
func TestParseOpenAPI_IgnoresNonMethods(t *testing.T) {
	doc := `{"paths":{"/x":{"description":"foo","parameters":[],"get":{}}}}`
	apis, err := parseOpenAPI([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(apis) != 1 || apis[0].Method != "GET" {
		t.Errorf("应只解析出 GET /x, got %+v", apis)
	}
}

func TestParseOpenAPI_Invalid(t *testing.T) {
	if _, err := parseOpenAPI([]byte(`{bad`)); err == nil {
		t.Error("非法 JSON 应返回错误")
	}
}

func TestParseOpenAPI_EmptyPaths(t *testing.T) {
	apis, err := parseOpenAPI([]byte(`{"paths":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(apis) != 0 {
		t.Errorf("无 paths 应解析出 0 个, got %d", len(apis))
	}
}
