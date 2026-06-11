package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/api-hub/server/internal/model"
)

type Store struct {
	pg  *pgxpool.Pool
	rdb *redis.Client
}

func New(ctx context.Context, pgDSN, redisAddr string) (*Store, error) {
	pool, err := pgxpool.New(ctx, pgDSN)
	if err != nil {
		return nil, fmt.Errorf("pg connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("pg ping: %w", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return &Store{pg: pool, rdb: rdb}, nil
}

func (s *Store) Close() {
	s.pg.Close()
	_ = s.rdb.Close()
}

func (s *Store) Redis() *redis.Client { return s.rdb }

// ---------- 管理后台会话（Redis，登录态）----------

func sessionKey(token string) string { return "auth:session:" + token }

// CreateSession 登录成功后写入会话，token -> 用户名，带 TTL。
func (s *Store) CreateSession(ctx context.Context, token, user string, ttl time.Duration) error {
	return s.rdb.Set(ctx, sessionKey(token), user, ttl).Err()
}

// SessionUser 返回 token 对应的登录用户名；token 无效/过期返回空串。
func (s *Store) SessionUser(ctx context.Context, token string) (string, error) {
	u, err := s.rdb.Get(ctx, sessionKey(token)).Result()
	if err == redis.Nil {
		return "", nil
	}
	return u, err
}

// DeleteSession 注销时删除会话。
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	return s.rdb.Del(ctx, sessionKey(token)).Err()
}

// ---------- 注册相关 ----------

// ExistingInstancePort 返回该实例已分配的 frp 端口（用于重注册时复用），不存在返回 0。
func (s *Store) ExistingInstancePort(ctx context.Context, serviceID int64, uid string) (int, string, error) {
	var port int
	var proxy string
	err := s.pg.QueryRow(ctx,
		`SELECT frp_remote_port, frp_proxy_name FROM instance WHERE service_id=$1 AND instance_uid=$2`,
		serviceID, uid).Scan(&port, &proxy)
	if err == pgx.ErrNoRows {
		return 0, "", nil
	}
	return port, proxy, err
}

// InstancePortByUID 返回某实例的 frp 远程端口（反注册时用于回收端口），不存在返回 0。
func (s *Store) InstancePortByUID(ctx context.Context, uid string) (int, error) {
	var port int
	err := s.pg.QueryRow(ctx, `SELECT frp_remote_port FROM instance WHERE instance_uid=$1`, uid).Scan(&port)
	if err == pgx.ErrNoRows {
		return 0, nil
	}
	return port, err
}

// UpsertService 按 (name,version,env) 幂等写入，返回 service id。
func (s *Store) UpsertService(ctx context.Context, rs model.RegisterService) (int64, error) {
	if rs.ConnMode == "" {
		rs.ConnMode = "relay"
	}
	if rs.HealthPath == "" {
		rs.HealthPath = "/health"
	}
	if rs.Tags == nil {
		// tags 列为 NOT NULL；请求未带 tags 时用空数组，避免写入 NULL 报错。
		rs.Tags = []string{}
	}
	var id int64
	err := s.pg.QueryRow(ctx, `
		INSERT INTO service (name, version, env, owner, tags, conn_mode, health_path, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7, now())
		ON CONFLICT (name, version, env) DO UPDATE
		  SET owner=EXCLUDED.owner, tags=EXCLUDED.tags, conn_mode=EXCLUDED.conn_mode,
		      health_path=EXCLUDED.health_path, updated_at=now()
		RETURNING id`,
		rs.Name, rs.Version, rs.Env, rs.Owner, rs.Tags, rs.ConnMode, rs.HealthPath).Scan(&id)
	return id, err
}

// SetOpenAPI 保存服务上报的完整 OpenAPI 文档。
func (s *Store) SetOpenAPI(ctx context.Context, serviceID int64, spec []byte) error {
	if len(spec) == 0 {
		return nil
	}
	_, err := s.pg.Exec(ctx,
		`UPDATE service SET openapi_spec=$2, updated_at=now() WHERE id=$1`, serviceID, spec)
	return err
}

// GetOpenAPI 返回服务的 OpenAPI 文档（无则返回 nil）。
func (s *Store) GetOpenAPI(ctx context.Context, serviceID int64) ([]byte, error) {
	var spec []byte
	err := s.pg.QueryRow(ctx, `SELECT openapi_spec FROM service WHERE id=$1`, serviceID).Scan(&spec)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return spec, err
}

// UpsertInstance 写入/更新实例连接信息。
func (s *Store) UpsertInstance(ctx context.Context, in model.Instance) (int64, error) {
	var id int64
	err := s.pg.QueryRow(ctx, `
		INSERT INTO instance (service_id, instance_uid, local_port, frp_remote_port, frp_proxy_name, direct_url, relay_upstream, last_seen_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7, now())
		ON CONFLICT (service_id, instance_uid) DO UPDATE
		  SET local_port=EXCLUDED.local_port, frp_remote_port=EXCLUDED.frp_remote_port,
		      frp_proxy_name=EXCLUDED.frp_proxy_name, direct_url=EXCLUDED.direct_url,
		      relay_upstream=EXCLUDED.relay_upstream, last_seen_at=now()
		RETURNING id`,
		in.ServiceID, in.InstanceUID, in.LocalPort, in.FRPRemotePort,
		in.FRPProxyName, in.DirectURL, in.RelayUpstream).Scan(&id)
	return id, err
}

// UpsertAPIs 幂等写入上报的接口，保留管理员设置的 status/conn_mode/rate_limit。
func (s *Store) UpsertAPIs(ctx context.Context, serviceID int64, apis []model.RegisterAPI) error {
	batch := &pgx.Batch{}
	for _, a := range apis {
		batch.Queue(`
			INSERT INTO api (service_id, path, method, summary, grp, req_schema, resp_schema, auth_required, source, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'openapi', now())
			ON CONFLICT (service_id, method, path) DO UPDATE
			  SET summary=EXCLUDED.summary, grp=EXCLUDED.grp, req_schema=EXCLUDED.req_schema,
			      resp_schema=EXCLUDED.resp_schema, auth_required=EXCLUDED.auth_required, updated_at=now()`,
			serviceID, a.Path, a.Method, a.Summary, a.Group, a.ReqSchema, a.RespSchema, a.AuthRequired)
	}
	br := s.pg.SendBatch(ctx, batch)
	defer br.Close()
	for range apis {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

// ---------- 心跳 / 在线状态（Redis ZSET，score=过期时刻）----------

func onlineKey(serviceID int64) string { return fmt.Sprintf("svc:online:%d", serviceID) }

func (s *Store) MarkOnline(ctx context.Context, serviceID int64, uid string, ttl time.Duration) error {
	expireAt := float64(time.Now().Add(ttl).Unix())
	key := onlineKey(serviceID)
	if err := s.rdb.ZAdd(ctx, key, redis.Z{Score: expireAt, Member: uid}).Err(); err != nil {
		return err
	}
	// 记录 uid -> serviceID，便于心跳时定位
	if err := s.rdb.Set(ctx, "uid:svc:"+uid, serviceID, ttl*3).Err(); err != nil {
		return err
	}
	return s.rdb.Expire(ctx, key, ttl*3).Err()
}

func (s *Store) Heartbeat(ctx context.Context, uid string, ttl time.Duration) error {
	sid, err := s.rdb.Get(ctx, "uid:svc:"+uid).Int64()
	if err == redis.Nil {
		return fmt.Errorf("unknown instance_uid: %s", uid)
	}
	if err != nil {
		return err
	}
	_, _ = s.pg.Exec(ctx, `UPDATE instance SET last_seen_at=now() WHERE instance_uid=$1`, uid)
	return s.MarkOnline(ctx, sid, uid, ttl)
}

func (s *Store) Deregister(ctx context.Context, uid string) error {
	sid, err := s.rdb.Get(ctx, "uid:svc:"+uid).Int64()
	if err == nil {
		s.rdb.ZRem(ctx, onlineKey(sid), uid)
	}
	s.rdb.Del(ctx, "uid:svc:"+uid)
	return nil
}

// OnlineCount 清理过期成员后返回在线实例数。
func (s *Store) OnlineCount(ctx context.Context, serviceID int64) int {
	key := onlineKey(serviceID)
	now := fmt.Sprintf("%d", time.Now().Unix())
	s.rdb.ZRemRangeByScore(ctx, key, "0", now)
	n, _ := s.rdb.ZCard(ctx, key).Result()
	return int(n)
}

// OnlineUIDs 返回当前在线实例 uid 集合。
func (s *Store) OnlineUIDs(ctx context.Context, serviceID int64) map[string]bool {
	key := onlineKey(serviceID)
	now := fmt.Sprintf("%d", time.Now().Unix())
	s.rdb.ZRemRangeByScore(ctx, key, "0", now)
	uids, _ := s.rdb.ZRange(ctx, key, 0, -1).Result()
	m := make(map[string]bool, len(uids))
	for _, u := range uids {
		m[u] = true
	}
	return m
}

// ---------- 查询（控制台用）----------

func (s *Store) ListServices(ctx context.Context) ([]model.Service, error) {
	rows, err := s.pg.Query(ctx, `
		SELECT id,name,version,env,owner,tags,conn_mode,health_path,status,created_at,updated_at
		FROM service ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Service
	for rows.Next() {
		var s2 model.Service
		if err := rows.Scan(&s2.ID, &s2.Name, &s2.Version, &s2.Env, &s2.Owner, &s2.Tags,
			&s2.ConnMode, &s2.HealthPath, &s2.Status, &s2.CreatedAt, &s2.UpdatedAt); err != nil {
			return nil, err
		}
		s2.OnlineCount = s.OnlineCount(ctx, s2.ID)
		out = append(out, s2)
	}
	return out, rows.Err()
}

func (s *Store) GetService(ctx context.Context, id int64) (*model.Service, error) {
	var s2 model.Service
	err := s.pg.QueryRow(ctx, `
		SELECT id,name,version,env,owner,tags,conn_mode,health_path,status,created_at,updated_at
		FROM service WHERE id=$1`, id).Scan(
		&s2.ID, &s2.Name, &s2.Version, &s2.Env, &s2.Owner, &s2.Tags,
		&s2.ConnMode, &s2.HealthPath, &s2.Status, &s2.CreatedAt, &s2.UpdatedAt)
	if err != nil {
		return nil, err
	}
	s2.OnlineCount = s.OnlineCount(ctx, s2.ID)
	return &s2, nil
}

func (s *Store) ListInstances(ctx context.Context, serviceID int64) ([]model.Instance, error) {
	rows, err := s.pg.Query(ctx, `
		SELECT id,service_id,instance_uid,local_port,frp_remote_port,frp_proxy_name,direct_url,relay_upstream,last_seen_at
		FROM instance WHERE service_id=$1 ORDER BY id`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Instance
	for rows.Next() {
		var in model.Instance
		if err := rows.Scan(&in.ID, &in.ServiceID, &in.InstanceUID, &in.LocalPort,
			&in.FRPRemotePort, &in.FRPProxyName, &in.DirectURL, &in.RelayUpstream, &in.LastSeenAt); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

func (s *Store) ListAPIs(ctx context.Context, serviceID int64) ([]model.API, error) {
	rows, err := s.pg.Query(ctx, `
		SELECT id,service_id,path,method,summary,grp,req_schema,resp_schema,auth_required,rate_limit,conn_mode,breaker_enabled,status,source
		FROM api WHERE service_id=$1 ORDER BY path, method`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.API
	for rows.Next() {
		var a model.API
		if err := rows.Scan(&a.ID, &a.ServiceID, &a.Path, &a.Method, &a.Summary, &a.Group,
			&a.ReqSchema, &a.RespSchema, &a.AuthRequired, &a.RateLimit, &a.ConnMode,
			&a.BreakerEnabled, &a.Status, &a.Source); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) GetAPI(ctx context.Context, id int64) (*model.API, error) {
	var a model.API
	err := s.pg.QueryRow(ctx, `
		SELECT id,service_id,path,method,summary,grp,req_schema,resp_schema,auth_required,rate_limit,conn_mode,breaker_enabled,status,source
		FROM api WHERE id=$1`, id).Scan(&a.ID, &a.ServiceID, &a.Path, &a.Method, &a.Summary, &a.Group,
		&a.ReqSchema, &a.RespSchema, &a.AuthRequired, &a.RateLimit, &a.ConnMode, &a.BreakerEnabled, &a.Status, &a.Source)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// APIPatch 控制台可改字段（nil 表示不改）。
type APIPatch struct {
	Status         *string `json:"status"`
	ConnMode       *string `json:"conn_mode"`
	RateLimit      *int    `json:"rate_limit"`
	BreakerEnabled *bool   `json:"breaker_enabled"`
}

func (s *Store) UpdateAPI(ctx context.Context, id int64, p APIPatch) error {
	_, err := s.pg.Exec(ctx, `
		UPDATE api SET
		  status          = COALESCE($2, status),
		  conn_mode       = CASE WHEN $3::text IS NOT NULL THEN $3 ELSE conn_mode END,
		  rate_limit      = COALESCE($4, rate_limit),
		  breaker_enabled = COALESCE($5, breaker_enabled),
		  updated_at      = now()
		WHERE id=$1`, id, p.Status, p.ConnMode, p.RateLimit, p.BreakerEnabled)
	return err
}

// ---------- 消费方 / Key ----------

func (s *Store) ListConsumers(ctx context.Context) ([]model.Consumer, error) {
	rows, err := s.pg.Query(ctx, `SELECT id,name,description,created_at FROM consumer ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Consumer
	for rows.Next() {
		var c model.Consumer
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetConsumer(ctx context.Context, id int64) (*model.Consumer, error) {
	var c model.Consumer
	err := s.pg.QueryRow(ctx,
		`SELECT id,name,description,created_at FROM consumer WHERE id=$1`, id).
		Scan(&c.ID, &c.Name, &c.Description, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) CreateConsumer(ctx context.Context, name, desc string) (*model.Consumer, error) {
	var c model.Consumer
	err := s.pg.QueryRow(ctx,
		`INSERT INTO consumer (name, description) VALUES ($1,$2) RETURNING id,name,description,created_at`,
		name, desc).Scan(&c.ID, &c.Name, &c.Description, &c.CreatedAt)
	return &c, err
}

func (s *Store) CreateKey(ctx context.Context, consumerID int64, key string, quota int) (*model.APIKey, error) {
	var k model.APIKey
	err := s.pg.QueryRow(ctx,
		`INSERT INTO api_key (consumer_id, api_key, quota_per_min) VALUES ($1,$2,$3)
		 RETURNING id,consumer_id,api_key,quota_per_min,status,created_at`,
		consumerID, key, quota).Scan(&k.ID, &k.ConsumerID, &k.APIKey, &k.QuotaPerMin, &k.Status, &k.CreatedAt)
	return &k, err
}

// ---------- 统计 ----------

type Overview struct {
	Services     int `json:"services"`
	APIs         int `json:"apis"`
	OnlineSvc    int `json:"online_services"`
	Consumers    int `json:"consumers"`
}

func (s *Store) StatsOverview(ctx context.Context) (Overview, error) {
	var o Overview
	_ = s.pg.QueryRow(ctx, `SELECT count(*) FROM service`).Scan(&o.Services)
	_ = s.pg.QueryRow(ctx, `SELECT count(*) FROM api`).Scan(&o.APIs)
	_ = s.pg.QueryRow(ctx, `SELECT count(*) FROM consumer`).Scan(&o.Consumers)
	svcs, err := s.ListServices(ctx)
	if err == nil {
		for _, sv := range svcs {
			if sv.OnlineCount > 0 {
				o.OnlineSvc++
			}
		}
	}
	return o, nil
}

// ---------- 三期：访问日志 / 审计 / 调用统计 ----------

func (s *Store) InsertAccessLogs(ctx context.Context, entries []model.AccessLogEntry) error {
	if len(entries) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, e := range entries {
		var sid, aid any // 0 -> NULL
		if e.ServiceID > 0 {
			sid = e.ServiceID
		}
		if e.APIID > 0 {
			aid = e.APIID
		}
		batch.Queue(`INSERT INTO access_log (service_id, api_id, api_path, method, status, latency_ms, consumer)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			sid, aid, e.APIPath, e.Method, e.Status, e.LatencyMS, e.Consumer)
	}
	br := s.pg.SendBatch(ctx, batch)
	defer br.Close()
	for range entries {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) InsertAudit(ctx context.Context, action, target, detail string) {
	_, _ = s.pg.Exec(ctx, `INSERT INTO audit_log (action, target, detail) VALUES ($1,$2,$3)`, action, target, detail)
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]model.AuditEntry, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.pg.Query(ctx, `SELECT id,action,target,detail,ts FROM audit_log ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.AuditEntry{}
	for rows.Next() {
		var a model.AuditEntry
		if err := rows.Scan(&a.ID, &a.Action, &a.Target, &a.Detail, &a.TS); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

type SeriesPoint struct {
	T     string `json:"t"`
	Count int    `json:"count"`
}
type StatusCount struct {
	Status int `json:"status"`
	Count  int `json:"count"`
}
type PathCount struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}
type CallStats struct {
	Total    int           `json:"total"`
	Success  int           `json:"success"`
	Error    int           `json:"error"`
	Series   []SeriesPoint `json:"series"`
	ByStatus []StatusCount `json:"by_status"`
	TopAPIs  []PathCount   `json:"top_apis"`
}

// CallStats 聚合访问日志。serviceID=0 表示全部服务。
func (s *Store) CallStats(ctx context.Context, serviceID int64, since time.Time) (CallStats, error) {
	cs := CallStats{Series: []SeriesPoint{}, ByStatus: []StatusCount{}, TopAPIs: []PathCount{}}

	_ = s.pg.QueryRow(ctx, `SELECT
			count(*),
			count(*) FILTER (WHERE status < 400),
			count(*) FILTER (WHERE status >= 400)
		FROM access_log WHERE ts >= $1 AND ($2 = 0 OR service_id = $2)`,
		since, serviceID).Scan(&cs.Total, &cs.Success, &cs.Error)

	if rows, err := s.pg.Query(ctx, `SELECT to_char(date_trunc('minute', ts),'MM-DD HH24:MI') t, count(*)
		FROM access_log WHERE ts >= $1 AND ($2 = 0 OR service_id = $2)
		GROUP BY date_trunc('minute', ts) ORDER BY date_trunc('minute', ts)`, since, serviceID); err == nil {
		for rows.Next() {
			var p SeriesPoint
			if rows.Scan(&p.T, &p.Count) == nil {
				cs.Series = append(cs.Series, p)
			}
		}
		rows.Close()
	}

	if rows, err := s.pg.Query(ctx, `SELECT status, count(*) FROM access_log
		WHERE ts >= $1 AND ($2 = 0 OR service_id = $2) GROUP BY status ORDER BY status`, since, serviceID); err == nil {
		for rows.Next() {
			var sc StatusCount
			if rows.Scan(&sc.Status, &sc.Count) == nil {
				cs.ByStatus = append(cs.ByStatus, sc)
			}
		}
		rows.Close()
	}

	if rows, err := s.pg.Query(ctx, `SELECT api_path, count(*) c FROM access_log
		WHERE ts >= $1 AND ($2 = 0 OR service_id = $2) GROUP BY api_path ORDER BY c DESC LIMIT 10`, since, serviceID); err == nil {
		for rows.Next() {
			var pc PathCount
			if rows.Scan(&pc.Path, &pc.Count) == nil {
				cs.TopAPIs = append(cs.TopAPIs, pc)
			}
		}
		rows.Close()
	}
	return cs, nil
}
