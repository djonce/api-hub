package model

import (
	"encoding/json"
	"time"
)

// Service 业务服务元数据
type Service struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Version     string    `json:"version"`
	Env         string    `json:"env"`
	Owner       string    `json:"owner"`
	Tags        []string  `json:"tags"`
	ConnMode    string    `json:"conn_mode"` // direct / relay
	HealthPath  string    `json:"health_path"`
	Status      string    `json:"status"` // enabled / disabled
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	OnlineCount int       `json:"online_count"`
}

// Instance 运行实例的稳定连接信息
type Instance struct {
	ID            int64      `json:"id"`
	ServiceID     int64      `json:"service_id"`
	InstanceUID   string     `json:"instance_uid"`
	LocalPort     int        `json:"local_port"`
	FRPRemotePort int        `json:"frp_remote_port"`
	FRPProxyName  string     `json:"frp_proxy_name"`
	DirectURL     string     `json:"direct_url"`
	RelayUpstream string     `json:"relay_upstream"`
	LastSeenAt    *time.Time `json:"last_seen_at"`
	Online        bool       `json:"online"`
}

// API 接口元数据
type API struct {
	ID           int64           `json:"id"`
	ServiceID    int64           `json:"service_id"`
	Path         string          `json:"path"`
	Method       string          `json:"method"`
	Summary      string          `json:"summary"`
	Group        string          `json:"group"`
	ReqSchema    json.RawMessage `json:"req_schema,omitempty"`
	RespSchema   json.RawMessage `json:"resp_schema,omitempty"`
	AuthRequired   bool            `json:"auth_required"`
	RateLimit      int             `json:"rate_limit"`
	ConnMode       *string         `json:"conn_mode,omitempty"`
	BreakerEnabled bool            `json:"breaker_enabled"`
	Status         string          `json:"status"`
	Source         string          `json:"source"`
}

// AccessLogEntry 一条访问日志（落库后用于统计）。
type AccessLogEntry struct {
	ServiceID int64
	APIID     int64
	APIPath   string
	Method    string
	Status    int
	LatencyMS float64
	Consumer  string
}

// AuditEntry 一条审计日志。
type AuditEntry struct {
	ID     int64     `json:"id"`
	Action string    `json:"action"`
	Target string    `json:"target"`
	Detail string    `json:"detail"`
	TS     time.Time `json:"ts"`
}

// Consumer 消费方
type Consumer struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

// APIKey 消费方密钥
type APIKey struct {
	ID          int64     `json:"id"`
	ConsumerID  int64     `json:"consumer_id"`
	APIKey      string    `json:"api_key"`
	QuotaPerMin int       `json:"quota_per_min"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

// ---- 注册请求/响应 ----

type RegisterRequest struct {
	Service     RegisterService  `json:"service"`
	Instance    RegisterInstance `json:"instance"`
	APIs        []RegisterAPI    `json:"apis"`
	OpenAPISpec json.RawMessage  `json:"openapi_spec,omitempty"`
}

type RegisterService struct {
	Name       string   `json:"name"`
	Version    string   `json:"version"`
	Env        string   `json:"env"`
	Owner      string   `json:"owner"`
	Tags       []string `json:"tags"`
	ConnMode   string   `json:"conn_mode"`
	HealthPath string   `json:"health_path"`
}

type RegisterInstance struct {
	InstanceUID string `json:"instance_uid"`
	LocalPort   int    `json:"local_port"`
	// AdvertiseHost 非空表示服务可被平台/网关直接访问（如同网段、容器编排内），
	// 此时直连与中继上游用 AdvertiseHost:LocalPort，无需 frp 穿透。
	AdvertiseHost string `json:"advertise_host,omitempty"`
}

type RegisterAPI struct {
	Path         string          `json:"path"`
	Method       string          `json:"method"`
	Summary      string          `json:"summary"`
	Group        string          `json:"group"`
	ReqSchema    json.RawMessage `json:"req_schema,omitempty"`
	RespSchema   json.RawMessage `json:"resp_schema,omitempty"`
	AuthRequired bool            `json:"auth_required"`
}

type RegisterResponse struct {
	ServiceID         int64   `json:"service_id"`
	InstanceID        int64   `json:"instance_id"`
	FRP               FRPInfo `json:"frp"`
	HeartbeatInterval int     `json:"heartbeat_interval"`
}

type FRPInfo struct {
	ServerAddr string `json:"server_addr"`
	ServerPort int    `json:"server_port"`
	Token      string `json:"token"`
	RemotePort int    `json:"remote_port"`
	ProxyName  string `json:"proxy_name"`
}
