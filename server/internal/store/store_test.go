package store

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/api-hub/server/internal/model"
)

// testStore 连接由环境变量指定的测试库（PG + Redis），并清空相关表/键以保证用例独立。
// 未配置环境变量时整组用例跳过，因此 `go test ./...` 在无基础设施时仍可通过。
//
// 运行方式（基础设施可用时）：
//
//	APIHUB_TEST_PG_DSN="postgres://apihub:apihub@localhost:5432/apihub?sslmode=disable" \
//	APIHUB_TEST_REDIS="localhost:6379" go test ./internal/store/ -v
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("APIHUB_TEST_PG_DSN")
	redisAddr := os.Getenv("APIHUB_TEST_REDIS")
	if dsn == "" || redisAddr == "" {
		t.Skip("未设置 APIHUB_TEST_PG_DSN / APIHUB_TEST_REDIS，跳过 store 集成测试")
	}
	ctx := context.Background()
	st, err := New(ctx, dsn, redisAddr)
	if err != nil {
		t.Fatalf("连接测试库失败: %v", err)
	}
	t.Cleanup(st.Close)

	if _, err := st.pg.Exec(ctx,
		`TRUNCATE service, instance, api, consumer, api_key, access_log, audit_log RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("清空表失败: %v", err)
	}
	if err := st.rdb.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("清空 redis 失败: %v", err)
	}
	return st
}

func sampleService() model.RegisterService {
	return model.RegisterService{Name: "demo-service", Version: "1.0.0", Env: "dev", Owner: "team", ConnMode: "relay", HealthPath: "/health"}
}

func TestServiceAndInstanceFlow(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	id, err := st.UpsertService(ctx, sampleService())
	if err != nil || id == 0 {
		t.Fatalf("UpsertService: id=%d err=%v", id, err)
	}
	// 相同 (name,version,env) 应幂等返回同一 id
	id2, err := st.UpsertService(ctx, sampleService())
	if err != nil || id2 != id {
		t.Fatalf("重复 Upsert 应返回同一 id: %d vs %d (err=%v)", id, id2, err)
	}

	// 尚无实例
	port, _, err := st.ExistingInstancePort(ctx, id, "uid-1")
	if err != nil || port != 0 {
		t.Fatalf("无实例时端口应为 0, got %d err=%v", port, err)
	}

	instID, err := st.UpsertInstance(ctx, model.Instance{
		ServiceID: id, InstanceUID: "uid-1", LocalPort: 9000,
		FRPRemotePort: 40001, FRPProxyName: "svc-uid-1-40001",
		DirectURL: "http://h:40001", RelayUpstream: "h:40001",
	})
	if err != nil || instID == 0 {
		t.Fatalf("UpsertInstance: %v", err)
	}

	// 重注册应能复用端口
	port, proxy, err := st.ExistingInstancePort(ctx, id, "uid-1")
	if err != nil || port != 40001 || proxy != "svc-uid-1-40001" {
		t.Fatalf("复用端口不符: port=%d proxy=%q err=%v", port, proxy, err)
	}

	insts, err := st.ListInstances(ctx, id)
	if err != nil || len(insts) != 1 {
		t.Fatalf("ListInstances 应有 1 个, got %d err=%v", len(insts), err)
	}

	// 反注册回收端口要用到：按 uid 查端口
	if p, err := st.InstancePortByUID(ctx, "uid-1"); err != nil || p != 40001 {
		t.Errorf("InstancePortByUID=%d err=%v, want 40001", p, err)
	}
	if p, err := st.InstancePortByUID(ctx, "no-such-uid"); err != nil || p != 0 {
		t.Errorf("未知 uid 应返回 0, got %d err=%v", p, err)
	}

	// 在线状态
	if err := st.MarkOnline(ctx, id, "uid-1", 30*time.Second); err != nil {
		t.Fatal(err)
	}
	if n := st.OnlineCount(ctx, id); n != 1 {
		t.Errorf("OnlineCount=%d, want 1", n)
	}
	if !st.OnlineUIDs(ctx, id)["uid-1"] {
		t.Error("OnlineUIDs 应包含 uid-1")
	}
	svc, err := st.GetService(ctx, id)
	if err != nil || svc.OnlineCount != 1 {
		t.Fatalf("GetService.OnlineCount=%d err=%v", svc.OnlineCount, err)
	}
	if err := st.Heartbeat(ctx, "uid-1", 30*time.Second); err != nil {
		t.Errorf("Heartbeat: %v", err)
	}

	// 反注册后下线
	if err := st.Deregister(ctx, "uid-1"); err != nil {
		t.Fatal(err)
	}
	if n := st.OnlineCount(ctx, id); n != 0 {
		t.Errorf("反注册后 OnlineCount=%d, want 0", n)
	}
}

func TestAPIsAndPatch(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	id, _ := st.UpsertService(ctx, sampleService())

	apis := []model.RegisterAPI{
		{Path: "/api/time", Method: "GET", Summary: "时间", AuthRequired: false},
		{Path: "/api/echo", Method: "POST", Summary: "回显", AuthRequired: true},
	}
	if err := st.UpsertAPIs(ctx, id, apis); err != nil {
		t.Fatalf("UpsertAPIs: %v", err)
	}
	got, err := st.ListAPIs(ctx, id)
	if err != nil || len(got) != 2 {
		t.Fatalf("ListAPIs 应有 2 个, got %d err=%v", len(got), err)
	}
	// 默认 source=openapi, status=enabled
	if got[0].Source != "openapi" || got[0].Status != "enabled" {
		t.Errorf("默认值不符: %+v", got[0])
	}

	// 选一个接口改其管理属性
	target := got[0]
	disabled := "disabled"
	direct := "direct"
	rl := 60
	br := true
	if err := st.UpdateAPI(ctx, target.ID, APIPatch{Status: &disabled, ConnMode: &direct, RateLimit: &rl, BreakerEnabled: &br}); err != nil {
		t.Fatalf("UpdateAPI: %v", err)
	}
	after, err := st.GetAPI(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != "disabled" || after.RateLimit != 60 || !after.BreakerEnabled {
		t.Errorf("PATCH 未生效: %+v", after)
	}
	if after.ConnMode == nil || *after.ConnMode != "direct" {
		t.Errorf("conn_mode 应为 direct, got %v", after.ConnMode)
	}

	// 再次上报（重注册）：应保留管理员设置的 status/rate_limit，不被覆盖回默认
	if err := st.UpsertAPIs(ctx, id, apis); err != nil {
		t.Fatal(err)
	}
	reAfter, _ := st.GetAPI(ctx, target.ID)
	if reAfter.Status != "disabled" || reAfter.RateLimit != 60 {
		t.Errorf("重注册不应覆盖管理员设置: %+v", reAfter)
	}
}

func TestOpenAPIRoundTrip(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	id, _ := st.UpsertService(ctx, sampleService())

	spec := []byte(`{"openapi":"3.0.0","paths":{"/api/time":{"get":{}}}}`)
	if err := st.SetOpenAPI(ctx, id, spec); err != nil {
		t.Fatalf("SetOpenAPI: %v", err)
	}
	got, err := st.GetOpenAPI(ctx, id)
	if err != nil {
		t.Fatalf("GetOpenAPI: %v", err)
	}
	// JSONB 不保留空白与键序，按语义比较而非字节比较。
	var want, have map[string]any
	if err := json.Unmarshal(spec, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(got, &have); err != nil {
		t.Fatalf("返回的不是合法 JSON: %s", got)
	}
	if !reflect.DeepEqual(want, have) {
		t.Fatalf("GetOpenAPI 往返语义不一致: got=%s", got)
	}
	// 不存在的服务返回 nil
	none, err := st.GetOpenAPI(ctx, 999999)
	if err != nil || none != nil {
		t.Errorf("未知服务应返回 nil, got %v err=%v", none, err)
	}
}

func TestConsumersAndKeys(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	c, err := st.CreateConsumer(ctx, "acme", "测试消费方")
	if err != nil || c.ID == 0 {
		t.Fatalf("CreateConsumer: %v", err)
	}
	k, err := st.CreateKey(ctx, c.ID, "ak_test_123", 100)
	if err != nil || k.APIKey != "ak_test_123" || k.QuotaPerMin != 100 {
		t.Fatalf("CreateKey: %+v err=%v", k, err)
	}
	list, err := st.ListConsumers(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "acme" {
		t.Fatalf("ListConsumers: %+v err=%v", list, err)
	}
	if got, err := st.GetConsumer(ctx, c.ID); err != nil || got.Name != "acme" {
		t.Errorf("GetConsumer: %+v err=%v", got, err)
	}
}

func TestAccessLogsAndStats(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	entries := []model.AccessLogEntry{
		{ServiceID: 1, APIPath: "/api/time", Method: "GET", Status: 200, LatencyMS: 3},
		{ServiceID: 1, APIPath: "/api/time", Method: "GET", Status: 200, LatencyMS: 4},
		{ServiceID: 1, APIPath: "/api/echo", Method: "POST", Status: 500, LatencyMS: 9},
		{ServiceID: 2, APIPath: "/other", Method: "GET", Status: 200, LatencyMS: 1},
	}
	if err := st.InsertAccessLogs(ctx, entries); err != nil {
		t.Fatalf("InsertAccessLogs: %v", err)
	}
	since := time.Now().Add(-time.Hour)

	// 全部服务
	all, err := st.CallStats(ctx, 0, since)
	if err != nil {
		t.Fatal(err)
	}
	if all.Total != 4 || all.Success != 3 || all.Error != 1 {
		t.Errorf("全量统计不符: total=%d success=%d error=%d", all.Total, all.Success, all.Error)
	}

	// 仅 service_id=1
	one, err := st.CallStats(ctx, 1, since)
	if err != nil {
		t.Fatal(err)
	}
	if one.Total != 3 || one.Success != 2 || one.Error != 1 {
		t.Errorf("service=1 统计不符: total=%d success=%d error=%d", one.Total, one.Success, one.Error)
	}
	// Top 接口：/api/time 出现 2 次应排第一
	if len(one.TopAPIs) == 0 || one.TopAPIs[0].Path != "/api/time" || one.TopAPIs[0].Count != 2 {
		t.Errorf("TopAPIs 不符: %+v", one.TopAPIs)
	}
	// 空时间窗（未来 since）应无数据
	empty, _ := st.CallStats(ctx, 0, time.Now().Add(time.Hour))
	if empty.Total != 0 {
		t.Errorf("未来时间窗应无数据, got total=%d", empty.Total)
	}
}

func TestAudit(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	st.InsertAudit(ctx, "service.register", "demo-service", `{"x":1}`)
	st.InsertAudit(ctx, "api.update", "api:1", "")

	list, err := st.ListAudit(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("应有 2 条审计, got %d", len(list))
	}
	// 按 id 倒序，最新的在前
	if list[0].Action != "api.update" {
		t.Errorf("最新审计应为 api.update, got %s", list[0].Action)
	}
	found := false
	for _, a := range list {
		if a.Action == "service.register" && a.Target == "demo-service" {
			found = true
		}
	}
	if !found {
		t.Error("应包含 service.register / demo-service")
	}
}

func TestSession(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	if err := st.CreateSession(ctx, "tok-1", "admin", time.Minute); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if u, err := st.SessionUser(ctx, "tok-1"); err != nil || u != "admin" {
		t.Errorf("SessionUser=%q err=%v, want admin", u, err)
	}
	// 未知 token 返回空
	if u, err := st.SessionUser(ctx, "nope"); err != nil || u != "" {
		t.Errorf("未知 token 应返回空, got %q err=%v", u, err)
	}
	// 删除后失效
	if err := st.DeleteSession(ctx, "tok-1"); err != nil {
		t.Fatal(err)
	}
	if u, _ := st.SessionUser(ctx, "tok-1"); u != "" {
		t.Errorf("删除后应失效, got %q", u)
	}
}

func TestStatsOverview(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	id, _ := st.UpsertService(ctx, sampleService())
	_ = st.UpsertAPIs(ctx, id, []model.RegisterAPI{{Path: "/a", Method: "GET"}})
	_, _ = st.CreateConsumer(ctx, "c1", "")

	o, err := st.StatsOverview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if o.Services != 1 || o.APIs != 1 || o.Consumers != 1 {
		t.Errorf("Overview 不符: %+v", o)
	}
}
