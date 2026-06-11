package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/api-hub/server/internal/apisix"
	"github.com/api-hub/server/internal/frpalloc"
	"github.com/api-hub/server/internal/model"
	"github.com/api-hub/server/internal/store"
)

type Config struct {
	FRPServerAddr string
	FRPServerPort int
	FRPToken      string
	HeartbeatSec  int
	PublicHost    string // 平台公网/可达主机，用于拼直连URL与中继上游
	GatewayURL    string // APISIX 数据面对外地址，如 http://host:9080，用于拼中继入口
	AdminUser     string // 管理后台登录账号
	AdminPass     string // 管理后台登录密码
}

type Handler struct {
	st    *store.Store
	alloc *frpalloc.Allocator
	ax    *apisix.Client
	cfg   Config
}

func New(st *store.Store, alloc *frpalloc.Allocator, ax *apisix.Client, cfg Config) *Handler {
	return &Handler{st: st, alloc: alloc, ax: ax, cfg: cfg}
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()

	// 登录 / 鉴权（公开）
	mux.HandleFunc("POST /api/v1/login", h.login)
	mux.HandleFunc("POST /api/v1/logout", h.logout)
	mux.HandleFunc("GET /api/v1/me", h.auth(h.me))

	// Agent <-> 注册中心（机器调用，不走登录态）
	mux.HandleFunc("POST /api/v1/register", h.register)
	mux.HandleFunc("POST /api/v1/heartbeat", h.heartbeat)
	mux.HandleFunc("POST /api/v1/deregister", h.deregister)

	// 控制台（均需登录）
	mux.HandleFunc("GET /api/v1/services", h.auth(h.listServices))
	mux.HandleFunc("GET /api/v1/services/{id}", h.auth(h.getService))
	mux.HandleFunc("GET /api/v1/services/{id}/apis", h.auth(h.listAPIs))
	mux.HandleFunc("GET /api/v1/services/{id}/instances", h.auth(h.listInstances))
	mux.HandleFunc("GET /api/v1/services/{id}/openapi", h.auth(h.getOpenAPI))
	mux.HandleFunc("POST /api/v1/services/{id}/sync", h.auth(h.syncService))
	mux.HandleFunc("PATCH /api/v1/apis/{id}", h.auth(h.patchAPI))
	mux.HandleFunc("GET /api/v1/config", h.auth(h.getConfig))
	mux.HandleFunc("GET /api/v1/consumers", h.auth(h.listConsumers))
	mux.HandleFunc("POST /api/v1/consumers", h.auth(h.createConsumer))
	mux.HandleFunc("POST /api/v1/consumers/{id}/keys", h.auth(h.createKey))
	mux.HandleFunc("GET /api/v1/stats/overview", h.auth(h.overview))

	// 三期：监控 / 审计（需登录）
	mux.HandleFunc("GET /api/v1/stats/calls", h.auth(h.callStats))
	mux.HandleFunc("GET /api/v1/audit", h.auth(h.listAudit))
	// 数据面访问日志接入（APISIX http-logger 推送，内网调用，不走登录态）
	mux.HandleFunc("POST /api/v1/ingest/access", h.ingestAccess)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	return withCORS(mux)
}

func (h *Handler) ttl() time.Duration { return time.Duration(h.cfg.HeartbeatSec*3) * time.Second }

// ---------- Agent 接口 ----------

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var req model.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Service.Name == "" || req.Instance.InstanceUID == "" || req.Instance.LocalPort == 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("name / instance_uid / local_port required"))
		return
	}
	ctx := r.Context()

	serviceID, err := h.st.UpsertService(ctx, req.Service)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	// 端口：重注册复用已有端口，否则分配新端口。
	port, proxy, err := h.st.ExistingInstancePort(ctx, serviceID, req.Instance.InstanceUID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if port == 0 {
		port, proxy, err = h.alloc.Allocate(ctx, req.Instance.InstanceUID)
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, err)
			return
		}
	} else {
		_ = h.alloc.Reserve(ctx, port)
	}

	inst := model.Instance{
		ServiceID:     serviceID,
		InstanceUID:   req.Instance.InstanceUID,
		LocalPort:     req.Instance.LocalPort,
		FRPRemotePort: port,
		FRPProxyName:  proxy,
		DirectURL:     fmt.Sprintf("http://%s:%d", h.cfg.PublicHost, port),
		RelayUpstream: fmt.Sprintf("%s:%d", h.cfg.PublicHost, port),
	}
	// 服务可直达时（advertise_host），直连/中继上游直接指向它，免 frp。
	if ah := req.Instance.AdvertiseHost; ah != "" {
		inst.DirectURL = fmt.Sprintf("http://%s:%d", ah, req.Instance.LocalPort)
		inst.RelayUpstream = fmt.Sprintf("%s:%d", ah, req.Instance.LocalPort)
	}
	instID, err := h.st.UpsertInstance(ctx, inst)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	if err := h.st.UpsertAPIs(ctx, serviceID, req.APIs); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	if err := h.st.SetOpenAPI(ctx, serviceID, req.OpenAPISpec); err != nil {
		log.Printf("set openapi: %v", err)
	}

	if err := h.st.MarkOnline(ctx, serviceID, req.Instance.InstanceUID, h.ttl()); err != nil {
		log.Printf("mark online: %v", err)
	}

	// 新实例上线后重发布该服务的中继路由（刷新上游节点）。
	h.resyncService(ctx, serviceID)
	h.st.InsertAudit(ctx, "service.register", req.Service.Name, req.Instance.InstanceUID)

	writeJSON(w, http.StatusOK, model.RegisterResponse{
		ServiceID:  serviceID,
		InstanceID: instID,
		FRP: model.FRPInfo{
			ServerAddr: h.cfg.FRPServerAddr,
			ServerPort: h.cfg.FRPServerPort,
			Token:      h.cfg.FRPToken,
			RemotePort: port,
			ProxyName:  proxy,
		},
		HeartbeatInterval: h.cfg.HeartbeatSec,
	})
}

func (h *Handler) heartbeat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		InstanceUID string `json:"instance_uid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := h.st.Heartbeat(r.Context(), body.InstanceUID, h.ttl()); err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) deregister(w http.ResponseWriter, r *http.Request) {
	var body struct {
		InstanceUID string `json:"instance_uid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	// 回收该实例占用的 frp 远程端口，避免端口随实例下线而泄漏、最终耗尽分配区间。
	if port, err := h.st.InstancePortByUID(ctx, body.InstanceUID); err == nil && port > 0 {
		_ = h.alloc.Release(ctx, port)
	}
	_ = h.st.Deregister(ctx, body.InstanceUID)
	h.st.InsertAudit(ctx, "service.deregister", body.InstanceUID, "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---------- 控制台接口 ----------

func (h *Handler) listServices(w http.ResponseWriter, r *http.Request) {
	svcs, err := h.st.ListServices(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, svcs)
}

func (h *Handler) getService(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	svc, err := h.st.GetService(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, svc)
}

func (h *Handler) listAPIs(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	apis, err := h.st.ListAPIs(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, apis)
}

func (h *Handler) listInstances(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	insts, err := h.st.ListInstances(ctx, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	online := h.st.OnlineUIDs(ctx, id)
	for i := range insts {
		insts[i].Online = online[insts[i].InstanceUID]
	}
	writeJSON(w, http.StatusOK, insts)
}

// getOpenAPI 返回服务的 OpenAPI 文档，并把 servers 改写为在线实例的直连地址，
// 使 Swagger UI 的 try-it-out 在直连模式下可直接调用。
func (h *Handler) getOpenAPI(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	spec, err := h.st.GetOpenAPI(ctx, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if len(spec) == 0 {
		writeErr(w, http.StatusNotFound, fmt.Errorf("该服务暂未上报 OpenAPI 文档"))
		return
	}
	var doc map[string]any
	if err := json.Unmarshal(spec, &doc); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if doc == nil {
		doc = map[string]any{}
	}
	servers := []map[string]string{}
	if base := h.pickBaseURL(ctx, id); base != "" {
		servers = append(servers, map[string]string{"url": base, "description": "直连"})
	}
	if h.ax.Enabled() && h.cfg.GatewayURL != "" {
		servers = append(servers, map[string]string{
			"url":         h.cfg.GatewayURL + apisix.RelayPrefix(id),
			"description": "中继（经平台网关，需 apikey 请求头）",
		})
	}
	if len(servers) > 0 {
		doc["servers"] = servers
	}
	// 注入 apikey 安全方案，便于在 Swagger UI 中调试中继接口。
	ensureAPIKeyScheme(doc)
	writeJSON(w, http.StatusOK, doc)
}

// pickBaseURL 优先取在线实例的直连地址，否则取任一实例。
func (h *Handler) pickBaseURL(ctx context.Context, serviceID int64) string {
	insts, err := h.st.ListInstances(ctx, serviceID)
	if err != nil || len(insts) == 0 {
		return ""
	}
	online := h.st.OnlineUIDs(ctx, serviceID)
	for _, in := range insts {
		if online[in.InstanceUID] && in.DirectURL != "" {
			return in.DirectURL
		}
	}
	return insts[0].DirectURL
}

func (h *Handler) patchAPI(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var p store.APIPatch
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	if err := h.st.UpdateAPI(ctx, id, p); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	detail, _ := json.Marshal(p)
	h.st.InsertAudit(ctx, "api.update", "api:"+strconv.FormatInt(id, 10), string(detail))
	// 上线+中继 -> 同步 APISIX 路由；下线/直连 -> 删除路由。
	h.syncAPI(ctx, id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// syncService 手动重发布某服务的全部中继路由。
func (h *Handler) syncService(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	h.resyncService(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// resyncService 遍历服务下所有接口并同步到 APISIX。
func (h *Handler) resyncService(ctx context.Context, serviceID int64) {
	if !h.ax.Enabled() {
		return
	}
	apis, err := h.st.ListAPIs(ctx, serviceID)
	if err != nil {
		log.Printf("resync list apis: %v", err)
		return
	}
	for _, a := range apis {
		h.syncAPI(ctx, a.ID)
	}
}

// relayNodes 收集该服务全部在线实例的中继上游，作为 APISIX upstream 节点。
func (h *Handler) relayNodes(ctx context.Context, serviceID int64) map[string]int {
	insts, err := h.st.ListInstances(ctx, serviceID)
	if err != nil {
		return nil
	}
	online := h.st.OnlineUIDs(ctx, serviceID)
	nodes := map[string]int{}
	for _, in := range insts {
		if online[in.InstanceUID] && in.RelayUpstream != "" {
			nodes[in.RelayUpstream] = 1
		}
	}
	return nodes
}

// effectiveMode 计算接口的有效调用模式（接口覆盖优先，否则继承服务）。
func effectiveMode(svcMode string, apiMode *string) string {
	if apiMode != nil && *apiMode != "" {
		return *apiMode
	}
	return svcMode
}

// syncAPI 将单个接口状态同步到 APISIX（未配置 APISIX 时为 no-op）。
func (h *Handler) syncAPI(ctx context.Context, apiID int64) {
	if !h.ax.Enabled() {
		return
	}
	a, err := h.st.GetAPI(ctx, apiID)
	if err != nil {
		log.Printf("syncAPI get api: %v", err)
		return
	}
	routeID := strconv.FormatInt(a.ID, 10)
	svc, err := h.st.GetService(ctx, a.ServiceID)
	if err != nil {
		return
	}
	// 下线、或直连模式：网关上不应存在该路由。
	if a.Status != "enabled" || effectiveMode(svc.ConnMode, a.ConnMode) != "relay" {
		_ = h.ax.DeleteRoute(ctx, routeID)
		return
	}
	nodes := h.relayNodes(ctx, a.ServiceID)
	if err := h.ax.UpsertRoute(ctx, routeID, a.ServiceID, a.Method, a.Path, nodes, a.AuthRequired, a.RateLimit, a.BreakerEnabled); err != nil {
		log.Printf("apisix upsert route: %v", err)
	}
}

func (h *Handler) listConsumers(w http.ResponseWriter, r *http.Request) {
	cs, err := h.st.ListConsumers(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, cs)
}

func (h *Handler) createConsumer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	c, err := h.st.CreateConsumer(r.Context(), body.Name, body.Description)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	h.st.InsertAudit(r.Context(), "consumer.create", c.Name, "")
	writeJSON(w, http.StatusOK, c)
}

func (h *Handler) createKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		QuotaPerMin int `json:"quota_per_min"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	ctx := r.Context()
	key := newAPIKey()
	k, err := h.st.CreateKey(ctx, id, key, body.QuotaPerMin)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// 同步为 APISIX consumer（key-auth 凭据 + 配额），使 Key 在中继网关生效。
	if h.ax.Enabled() {
		if c, e := h.st.GetConsumer(ctx, id); e == nil {
			uname := apisixUsername(c.Name, k.ID)
			if e := h.ax.UpsertConsumer(ctx, uname, k.APIKey, k.QuotaPerMin); e != nil {
				log.Printf("apisix upsert consumer: %v", e)
			}
		}
	}
	h.st.InsertAudit(ctx, "key.issue", "consumer:"+strconv.FormatInt(id, 10), "")
	writeJSON(w, http.StatusOK, k)
}

// getConfig 返回前端需要的运行配置（中继入口、是否启用网关）。
func (h *Handler) getConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"apisix_enabled": h.ax.Enabled(),
		"gateway_url":    h.cfg.GatewayURL,
	})
}

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	o, err := h.st.StatsOverview(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

// ---------- 三期：监控 / 审计 / 日志接入 ----------

func (h *Handler) callStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var sid int64
	if v := q.Get("service_id"); v != "" {
		sid, _ = strconv.ParseInt(v, 10, 64)
	}
	hours := 24
	if v := q.Get("hours"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			hours = n
		}
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	cs, err := h.st.CallStats(r.Context(), sid, since)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, cs)
}

func (h *Handler) listAudit(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	a, err := h.st.ListAudit(r.Context(), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// apisixLogEntry 对应 APISIX http-logger 默认日志对象的关注子集。
type apisixLogEntry struct {
	RouteID  string  `json:"route_id"`
	Latency  float64 `json:"latency"`
	Consumer string  `json:"consumer"`
	Request  struct {
		URI    string `json:"uri"`
		Method string `json:"method"`
	} `json:"request"`
	Response struct {
		Status int `json:"status"`
	} `json:"response"`
}

// ingestAccess 接收 APISIX http-logger 推送的访问日志（JSON 数组），落库供统计。
func (h *Handler) ingestAccess(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	entries, err := parseAccessLogPayload(raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	logs := make([]model.AccessLogEntry, 0, len(entries))
	for _, e := range entries {
		le := model.AccessLogEntry{
			Method:    e.Request.Method,
			Status:    e.Response.Status,
			LatencyMS: e.Latency,
			Consumer:  e.Consumer,
			APIPath:   e.Request.URI,
		}
		// route_id 即控制面的 api.id，可回溯服务与原始路径。
		if id, err := strconv.ParseInt(e.RouteID, 10, 64); err == nil {
			if a, err := h.st.GetAPI(ctx, id); err == nil {
				le.APIID = a.ID
				le.ServiceID = a.ServiceID
				le.APIPath = a.Path
			}
		}
		logs = append(logs, le)
	}
	if err := h.st.InsertAccessLogs(ctx, logs); err != nil {
		log.Printf("ingest access: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ingested": len(logs)})
}

// ---------- 登录 / 鉴权 ----------

const sessionTTL = 24 * time.Hour

// bearerToken 从 Authorization: Bearer <token> 头取出 token。
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}

// login 校验管理员账号密码，成功则签发会话 token。
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// 常量时间比较，避免计时侧信道。
	userOK := subtle.ConstantTimeCompare([]byte(body.Username), []byte(h.cfg.AdminUser)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(body.Password), []byte(h.cfg.AdminPass)) == 1
	if !userOK || !passOK {
		writeErr(w, http.StatusUnauthorized, fmt.Errorf("用户名或密码错误"))
		return
	}
	token := newSessionToken()
	if err := h.st.CreateSession(r.Context(), token, body.Username, sessionTTL); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token, "username": body.Username})
}

// logout 注销当前会话（删除 token）。
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if tok := bearerToken(r); tok != "" {
		_ = h.st.DeleteSession(r.Context(), tok)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// me 返回当前登录用户（经 auth 中间件保护，用户名必非空）。
func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	user, _ := h.st.SessionUser(r.Context(), bearerToken(r))
	writeJSON(w, http.StatusOK, map[string]string{"username": user})
}

// auth 中间件：要求请求携带有效会话 token，否则 401。
func (h *Handler) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearerToken(r)
		if tok == "" {
			writeErr(w, http.StatusUnauthorized, fmt.Errorf("未登录"))
			return
		}
		user, err := h.st.SessionUser(r.Context(), tok)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if user == "" {
			writeErr(w, http.StatusUnauthorized, fmt.Errorf("登录已过期，请重新登录"))
			return
		}
		next(w, r)
	}
}

// ---------- helpers ----------

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid id"))
		return 0, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PATCH,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
