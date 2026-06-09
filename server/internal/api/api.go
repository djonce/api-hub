package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
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

	// Agent <-> 注册中心
	mux.HandleFunc("POST /api/v1/register", h.register)
	mux.HandleFunc("POST /api/v1/heartbeat", h.heartbeat)
	mux.HandleFunc("POST /api/v1/deregister", h.deregister)

	// 控制台
	mux.HandleFunc("GET /api/v1/services", h.listServices)
	mux.HandleFunc("GET /api/v1/services/{id}", h.getService)
	mux.HandleFunc("GET /api/v1/services/{id}/apis", h.listAPIs)
	mux.HandleFunc("PATCH /api/v1/apis/{id}", h.patchAPI)
	mux.HandleFunc("GET /api/v1/consumers", h.listConsumers)
	mux.HandleFunc("POST /api/v1/consumers", h.createConsumer)
	mux.HandleFunc("POST /api/v1/consumers/{id}/keys", h.createKey)
	mux.HandleFunc("GET /api/v1/stats/overview", h.overview)

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
	instID, err := h.st.UpsertInstance(ctx, inst)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	if err := h.st.UpsertAPIs(ctx, serviceID, req.APIs); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	if err := h.st.MarkOnline(ctx, serviceID, req.Instance.InstanceUID, h.ttl()); err != nil {
		log.Printf("mark online: %v", err)
	}

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
	_ = h.st.Deregister(r.Context(), body.InstanceUID)
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
	// 二期：上线+中继 -> 同步 APISIX 路由；下线 -> 删除路由。
	h.syncRoute(ctx, id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// syncRoute 将接口状态同步到 APISIX（未配置 APISIX 时为 no-op）。
func (h *Handler) syncRoute(ctx context.Context, apiID int64) {
	if !h.ax.Enabled() {
		return
	}
	a, err := h.st.GetAPI(ctx, apiID)
	if err != nil {
		log.Printf("syncRoute get api: %v", err)
		return
	}
	routeID := strconv.FormatInt(a.ID, 10)
	if a.Status != "enabled" {
		_ = h.ax.DeleteRoute(ctx, routeID)
		return
	}
	svc, err := h.st.GetService(ctx, a.ServiceID)
	if err != nil {
		return
	}
	mode := svc.ConnMode
	if a.ConnMode != nil && *a.ConnMode != "" {
		mode = *a.ConnMode
	}
	if mode != "relay" {
		_ = h.ax.DeleteRoute(ctx, routeID) // 直连模式不经网关
		return
	}
	// MVP：取该服务任一在线实例的上游（多实例负载均衡留待三期）。
	insts, err := h.st.ListInstances(ctx, a.ServiceID)
	if err != nil || len(insts) == 0 {
		return
	}
	if err := h.ax.UpsertRoute(ctx, routeID, a.Method, a.Path, insts[0].RelayUpstream, a.AuthRequired, a.RateLimit); err != nil {
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
	key := newAPIKey()
	k, err := h.st.CreateKey(r.Context(), id, key, body.QuotaPerMin)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, k)
}

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	o, err := h.st.StatsOverview(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
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
