// Agent：随业务服务运行，负责自动注册、拉起 frpc、心跳、退出反注册。
// 仅依赖标准库，可独立编译为单二进制。
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// ---- 与控制面对齐的报文结构 ----

type registerReq struct {
	Service  regService  `json:"service"`
	Instance regInstance `json:"instance"`
	APIs     []regAPI    `json:"apis"`
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
	InstanceUID string `json:"instance_uid"`
	LocalPort   int    `json:"local_port"`
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
	platformURL string
	name        string
	version     string
	env         string
	owner       string
	connMode    string
	localPort   string
	healthPath  string
	openapiURL  string
	frpcBin     string
	workDir     string
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
	}
}

func main() {
	cfg := loadConfig()
	uid := instanceUID(cfg.name)
	log.Printf("agent starting: service=%s uid=%s", cfg.name, uid)

	apis, err := fetchOpenAPI(cfg.openapiURL)
	if err != nil {
		log.Printf("warn: fetch openapi failed (%v); registering with empty api list", err)
	}

	resp, err := register(cfg, uid, apis)
	if err != nil {
		log.Fatalf("register failed: %v", err)
	}
	log.Printf("registered: service_id=%d remote_port=%d proxy=%s", resp.ServiceID, resp.FRP.RemotePort, resp.FRP.ProxyName)

	// 拉起 frpc（穿透）
	var frpcCmd *exec.Cmd
	if cfg.frpcBin != "" {
		frpcCmd, err = startFRPC(cfg, resp.FRP)
		if err != nil {
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
	go heartbeatLoop(ctx, cfg, uid, interval)

	// 等待退出信号
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Printf("shutting down...")
	cancel()
	deregister(cfg, uid)
	if frpcCmd != nil && frpcCmd.Process != nil {
		_ = frpcCmd.Process.Kill()
	}
}

func instanceUID(name string) string {
	host, _ := os.Hostname()
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%s-%s", name, host, hex.EncodeToString(b))
}

// fetchOpenAPI 拉取本地服务的 OpenAPI 文档并解析出接口清单。
func fetchOpenAPI(url string) ([]regAPI, error) {
	if url == "" {
		return nil, nil
	}
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseOpenAPI(body)
}

// parseOpenAPI 解析 OpenAPI 3.x 的 paths 段。
func parseOpenAPI(body []byte) ([]regAPI, error) {
	var doc struct {
		Paths map[string]map[string]struct {
			Summary string   `json:"summary"`
			Tags    []string `json:"tags"`
			Security []any   `json:"security"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	var out []regAPI
	methods := map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true}
	for path, ops := range doc.Paths {
		for method, op := range ops {
			if !methods[strings.ToLower(method)] {
				continue
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

func register(cfg config, uid string, apis []regAPI) (*registerResp, error) {
	lp := 0
	fmt.Sscanf(cfg.localPort, "%d", &lp)
	req := registerReq{
		Service: regService{
			Name: cfg.name, Version: cfg.version, Env: cfg.env, Owner: cfg.owner,
			Tags: []string{}, ConnMode: cfg.connMode, HealthPath: cfg.healthPath,
		},
		Instance: regInstance{InstanceUID: uid, LocalPort: lp},
		APIs:     apis,
	}
	var out registerResp
	if err := postJSON(cfg.platformURL+"/api/v1/register", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func heartbeatLoop(ctx context.Context, cfg config, uid string, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := postJSON(cfg.platformURL+"/api/v1/heartbeat", map[string]string{"instance_uid": uid}, nil); err != nil {
				log.Printf("heartbeat error: %v", err)
			}
		}
	}
}

func deregister(cfg config, uid string) {
	if err := postJSON(cfg.platformURL+"/api/v1/deregister", map[string]string{"instance_uid": uid}, nil); err != nil {
		log.Printf("deregister error: %v", err)
	}
}

// startFRPC 生成 frpc 配置并拉起 frpc 进程。
func startFRPC(cfg config, f frp) (*exec.Cmd, error) {
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
		return fmt.Errorf("%s -> %d: %s", url, resp.StatusCode, string(b))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
