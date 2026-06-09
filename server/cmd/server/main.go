package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/api-hub/server/internal/api"
	"github.com/api-hub/server/internal/apisix"
	"github.com/api-hub/server/internal/frpalloc"
	"github.com/api-hub/server/internal/store"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func main() {
	ctx := context.Background()

	pgDSN := env("PG_DSN", "postgres://apihub:apihub@localhost:5432/apihub?sslmode=disable")
	redisAddr := env("REDIS_ADDR", "localhost:6379")
	listen := env("LISTEN", ":8080")

	st, err := store.New(ctx, pgDSN, redisAddr)
	if err != nil {
		log.Fatalf("store init: %v", err)
	}
	defer st.Close()

	alloc := frpalloc.New(st.Redis(), atoi(env("FRP_PORT_MIN", "40000")), atoi(env("FRP_PORT_MAX", "49999")))
	ax := apisix.New(env("APISIX_ADMIN", ""), env("APISIX_ADMIN_KEY", ""))

	cfg := api.Config{
		FRPServerAddr: env("FRP_SERVER_ADDR", "127.0.0.1"),
		FRPServerPort: atoi(env("FRP_SERVER_PORT", "7000")),
		FRPToken:      env("FRP_TOKEN", "CHANGE_ME_FRP_TOKEN"),
		HeartbeatSec:  atoi(env("HEARTBEAT_SEC", "10")),
		PublicHost:    env("PUBLIC_HOST", "127.0.0.1"),
	}

	h := api.New(st, alloc, ax, cfg)
	srv := &http.Server{
		Addr:              listen,
		Handler:           h.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("api-hub control plane listening on %s", listen)
	log.Fatal(srv.ListenAndServe())
}
