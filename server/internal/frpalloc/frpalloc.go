package frpalloc

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Allocator 在 [min,max] 区间内分配 frp 远程端口，已用端口记录在 Redis 集合。
type Allocator struct {
	rdb *redis.Client
	min int
	max int
}

const usedKey = "frp:ports:used"

func New(rdb *redis.Client, min, max int) *Allocator {
	return &Allocator{rdb: rdb, min: min, max: max}
}

// Allocate 返回一个未占用端口及其 proxy 名称。
func (a *Allocator) Allocate(ctx context.Context, instanceUID string) (int, string, error) {
	for p := a.min; p <= a.max; p++ {
		added, err := a.rdb.SAdd(ctx, usedKey, p).Result()
		if err != nil {
			return 0, "", err
		}
		if added == 1 { // 抢占成功
			return p, proxyName(instanceUID, p), nil
		}
	}
	return 0, "", fmt.Errorf("no free frp port in range %d-%d", a.min, a.max)
}

// Reserve 标记某端口已占用（重注册复用已有端口时调用）。
func (a *Allocator) Reserve(ctx context.Context, port int) error {
	return a.rdb.SAdd(ctx, usedKey, port).Err()
}

// Release 回收端口（反注册时可调用）。
func (a *Allocator) Release(ctx context.Context, port int) error {
	return a.rdb.SRem(ctx, usedKey, port).Err()
}

func proxyName(uid string, port int) string {
	return fmt.Sprintf("svc-%s-%d", uid, port)
}
