// Agent：随业务服务运行，负责自动注册、拉起 frpc、心跳、退出反注册。
// 仅依赖标准库，可独立编译为单二进制。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ---- 与控制面对齐的报文结构 ----

type registerReq struct {
	Service     regService      `json:"service"`
	Instance    regInstance     `json:"instance"`
	APIs        []regAPI        `json:"apis"`
	OpenAPISpec json.RawMessage `json:"openapi_spec,omitempty"`
}

type regService struct {
	Name       string   `json:"name"`
	Version    string   `json:"version"`
	Env        string   `json:"env"`
	Owner      string   `json:"owner"`
	Tags       []string `json:"tags"`
	ConnMode   string   `json:"conn_mode"`
	HealthPath string   `json:"health_path"`
}

type regInstance struct {
	InstanceUID   string `json:"instance_uid"`
	LocalPort     int    `json:"local_port"`
	AdvertiseHost string `json:"advertise_host,omitempty"`
}

type regAPI struct {
	Path         string `json:"path"`
	Method       string `json:"method"`
	Summary      string `json:"summary"`
	Group        string `json:"group"`
	AuthRequired bool   `json:"auth_required"`
}

type registerResp struct {
	ServiceID         int64 `json:"service_id"`
	InstanceID        int64 `json:"instance_id"`
	FRP               frp   `json:"frp"`
	HeartbeatInterval int   `json:"heartbeat_interval"`
}

type frp struct {
	ServerAddr string `json:"server_addr"`
	ServerPort int    `json:"server_port"`
	Token      string `json:"token"`
	RemotePort int    `json:"remote_port"`
	ProxyName  string `json:"proxy_name"`
}

type config struct {
	platformURL  string
	name         string
	version      string
	env          string
	owner        string
	connMode     string
	localPort    string
	healthPath   string
	openapiURL   string
	frpcBin      string
	workDir      string
	advertiseHost string // 服务可直达地址（同网段/容器编排内），设置后免 frp
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func loadConfig() config {
	return config{
		platformURL: env("PLATFORM_URL", "http://localhost:8080"),
		name:        env("SERVICE_NAME", "demo-service"),
		version:     env("SERVICE_VERSION", "1.0.0"),
		env:         env("SERVICE_ENV", "dev"),
		owner:       env("SERVICE_OWNER", ""),
		connMode:    env("CONN_MODE", "relay"),
		localPort:   env("LOCAL_PORT", "9000"),
		healthPath:  env("HEALTH_PATH", "/health"),
		openapiURL:  env("OPENAPI_URL", "http://localhost:9000/openapi.json"),
		frpcBin:     env("FRPC_BIN", ""), // 留空则不拉起 frpc（本机联调场景）
		workDir:     env("AGENT_WORKDIR", "."),
		advertiseHost: env("SERVICE_HOST", ""), // 设置后免 frp，直接用该地址作上游
	}
}

// runtime 持有 Agent 运行期可变状态，使心跳协程能在控制面侧实例丢失时自愈（重注册 + 必要时重启 frpc）。
type runtime struct {
	cfg     config
	uid     string
	apis    []regAPI
	rawSpec []byte

	mu         sync.Mutex
	frpcCmd    *exec.Cmd
	remotePort int
}

func main() {
	cfg := loadConfig()
	uid := instanceUID(cfg.name, cfg.localPort)
	log.Printf("agent starting: service=%s uid=%s", cfg.name, uid)

	apis, rawSpec, err := fetchOpenAPI(cfg.openapiURL)
	if err != nil {
		log.Printf("warn: fetch openapi failed (%v); registering with empty api list", err)
	}
	rt := &runtime{cfg: cfg, uid: uid, apis: apis, rawSpec: rawSpec}

	resp, err := register(cfg, uid, apis, rawSpec)
	if err != nil {
		log.Fatalf("register failed: %v", err)
	}
	log.Printf("registered: service_id=%d remote_port=%d proxy=%s", resp.ServiceID, resp.FRP.RemotePort, resp.FRP.ProxyName)

	// 拉起 frpc（穿透）
	if cfg.frpcBin != "" {
		if err := rt.startFRPC(resp.FRP); err != nil {
			log.Printf("warn: start frpc failed: %v", err)
		} else {
			log.Printf("frpc started, tunneling local:%s -> frps remote:%d", cfg.localPort, resp.FRP.RemotePort)
		}
	} else {
		log.Printf("FRPC_BIN 未设置，跳过 frpc 拉起（本机联调）")
	}

	// 心跳
	interval := time.Duration(resp.HeartbeatInterval) * time.Second
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	go rt.heartbeatLoop(ctx, interval)

	// 等待退出信号
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Printf("shutting down...")
	cancel()
	deregister(cfg, uid)
	rt.stopFRPC()
}

// instanceUID 基于 服务名+主机名+本地端口 生成稳定实例标识。
// 同一服务在同一主机同一端口即同一实例：进程重启后 UID 不变，可复用已分配的 frp 端口，
// 避免每次重启都新建实例行、泄漏 frp 端口。
func instanceUID(name, port string) string {
	host, _ := os.Hostname()
	return fmt.Sprintf("%s-%s-%s", name, host, port)
}

// fetchOpenAPI 拉取本地服务的 OpenAPI 文档：返回解析出的接口清单与原始文档字节。
func fetchOpenAPI(url string) ([]regAPI, []byte, error) {
	if url == "" {
		return nil, nil, nil
	}
	resp, err := http.Get(url)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	apis, err := parseOpenAPI(body)
	return apis, body, err
}

// parseOpenAPI 解析 OpenAPI 3.x 的 paths 段。
// 路径条目里除 HTTP 方法外还可能有 parameters/description/$ref 等同级字段（OpenAPI 合法），
// 故按 RawMessage 逐操作解析，跳过非方法键，避免个别字段导致整篇解析失败。
func parseOpenAPI(body []byte) ([]regAPI, error) {
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	type operation struct {
		Summary  string   `json:"summary"`
		Tags     []string `json:"tags"`
		Security []any    `json:"security"`
	}
	var out []regAPI
	methods := map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true}
	for path, ops := range doc.Paths {
		for method, raw := range ops {
			if !methods[strings.ToLower(method)] {
				continue // 跳过 parameters / description / $ref 等非操作字段
			}
			var op operation
			if err := json.Unmarshal(raw, &op); err != nil {
				continue // 单个操作解析失败不影响其他接口
			}
			grp := ""
			if len(op.Tags) > 0 {
				grp = op.Tags[0]
			}
			out = append(out, regAPI{
				Path:         path,
				Method:       strings.ToUpper(method),
				Summary:      op.Summary,
				Group:        grp,
				AuthRequired: len(op.Security) > 0,
			})
		}
	}
	return out, nil
}

func register(cfg config, uid string, apis []regAPI, rawSpec []byte) (*registerResp, error) {
	lp := 0
	fmt.Sscanf(cfg.localPort, "%d", &lp)
	req := registerReq{
		Service: regService{
			Name: cfg.name, Version: cfg.version, Env: cfg.env, Owner: cfg.owner,
			Tags: []string{}, ConnMode: cfg.connMode, HealthPath: cfg.healthPath,
		},
		Instance:    regInstance{InstanceUID: uid, LocalPort: lp, AdvertiseHost: cfg.advertiseHost},
		APIs:        apis,
		OpenAPISpec: json.RawMessage(rawSpec),
	}
	var out registerResp
	if err := postJSON(cfg.platformURL+"/api/v1/register", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (rt *runtime) heartbeatLoop(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			err := postJSON(rt.cfg.platformURL+"/api/v1/heartbeat", map[string]string{"instance_uid": rt.uid}, nil)
			if err == nil {
				continue
			}
			// 控制面侧实例已不存在（如控制面重启 + Redis 在线态过期）会返回 404；
			// 此时自动重新注册以自愈，而非永久报错。
			var he *httpStatusError
			if errors.As(err, &he) && he.code == http.StatusNotFound {
				log.Printf("heartbeat: 实例在控制面已丢失，尝试重新注册...")
				rt.reregister()
			} else {
				log.Printf("heartbeat error: %v", err)
			}
		}
	}
}

// reregister 重新注册当前实例；若使用 frp 且分配到的远程端口变化，则重启 frpc 以保持隧道一致。
func (rt *runtime) reregister() {
	resp, err := register(rt.cfg, rt.uid, rt.apis, rt.rawSpec)
	if err != nil {
		log.Printf("re-register failed: %v", err)
		return
	}
	log.Printf("re-registered: service_id=%d remote_port=%d", resp.ServiceID, resp.FRP.RemotePort)
	if rt.cfg.frpcBin == "" {
		return
	}
	rt.mu.Lock()
	needRestart := rt.frpcCmd == nil || resp.FRP.RemotePort != rt.remotePort
	rt.mu.Unlock()
	if needRestart {
		rt.stopFRPC()
		if err := rt.startFRPC(resp.FRP); err != nil {
			log.Printf("warn: restart frpc failed: %v", err)
		} else {
			log.Printf("frpc restarted, tunneling local:%s -> frps remote:%d", rt.cfg.localPort, resp.FRP.RemotePort)
		}
	}
}

func deregister(cfg config, uid string) {
	if err := postJSON(cfg.platformURL+"/api/v1/deregister", map[string]string{"instance_uid": uid}, nil); err != nil {
		log.Printf("deregister error: %v", err)
	}
}

// startFRPC 生成 frpc 配置并拉起 frpc 进程，记录当前进程与远程端口（供重启/退出时管理）。
func (rt *runtime) startFRPC(f frp) error {
	cmd, err := buildFRPC(rt.cfg, f)
	if err != nil {
		return err
	}
	rt.mu.Lock()
	rt.frpcCmd = cmd
	rt.remotePort = f.RemotePort
	rt.mu.Unlock()
	return nil
}

// stopFRPC 终止当前 frpc 进程（若有）。
func (rt *runtime) stopFRPC() {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.frpcCmd != nil && rt.frpcCmd.Process != nil {
		_ = rt.frpcCmd.Process.Kill()
		_ = rt.frpcCmd.Wait()
	}
	rt.frpcCmd = nil
}

// buildFRPC 写出 frpc 配置并启动进程。
func buildFRPC(cfg config, f frp) (*exec.Cmd, error) {
	lp := cfg.localPort
	conf := fmt.Sprintf(`serverAddr = "%s"
serverPort = %d
auth.method = "token"
auth.token = "%s"

[[proxies]]
name = "%s"
type = "tcp"
localIP = "127.0.0.1"
localPort = %s
remotePort = %d
`, f.ServerAddr, f.ServerPort, f.Token, f.ProxyName, lp, f.RemotePort)

	confPath := cfg.workDir + "/frpc.generated.toml"
	if err := os.WriteFile(confPath, []byte(conf), 0o600); err != nil {
		return nil, err
	}
	cmd := exec.Command(cfg.frpcBin, "-c", confPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// httpStatusError 携带 HTTP 状态码，便于调用方按状态分支处理（如心跳 404 触发重注册）。
type httpStatusError struct {
	url  string
	code int
	body string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("%s -> %d: %s", e.url, e.code, e.body)
}

func postJSON(url string, in any, out any) error {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(in); err != nil {
		return err
	}
	resp, err := http.Post(url, "application/json", &buf)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return &httpStatusError{url: url, code: resp.StatusCode, body: string(b)}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
