package frpalloc

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newAlloc 用内存版 redis 构造一个分配器，便于离线单测。
func newAlloc(t *testing.T, min, max int) *Allocator {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return New(rdb, min, max)
}

func TestAllocate_Sequential(t *testing.T) {
	a := newAlloc(t, 40000, 40002)
	ctx := context.Background()

	p1, name1, err := a.Allocate(ctx, "uid-a")
	if err != nil || p1 != 40000 {
		t.Fatalf("第一个端口应为 40000, got %d err=%v", p1, err)
	}
	if name1 != "svc-uid-a-40000" {
		t.Errorf("proxy 名不符: %q", name1)
	}
	p2, _, err := a.Allocate(ctx, "uid-b")
	if err != nil || p2 != 40001 {
		t.Fatalf("第二个端口应为 40001, got %d err=%v", p2, err)
	}
}

func TestAllocate_Exhausted(t *testing.T) {
	a := newAlloc(t, 40000, 40001) // 仅 2 个端口
	ctx := context.Background()
	if _, _, err := a.Allocate(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Allocate(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Allocate(ctx, "c"); err == nil {
		t.Error("端口用尽后应返回错误")
	}
}

// Reserve 预占的端口不应再被分配出去。
func TestReserve_SkipsTaken(t *testing.T) {
	a := newAlloc(t, 40000, 40002)
	ctx := context.Background()
	if err := a.Reserve(ctx, 40000); err != nil {
		t.Fatal(err)
	}
	p, _, err := a.Allocate(ctx, "uid")
	if err != nil {
		t.Fatal(err)
	}
	if p == 40000 {
		t.Errorf("已预占的 40000 不应被分配, got %d", p)
	}
}

// Release 回收后端口可再次被分配。
func TestRelease_Reusable(t *testing.T) {
	a := newAlloc(t, 40000, 40000) // 只有 1 个端口
	ctx := context.Background()
	p, _, err := a.Allocate(ctx, "a")
	if err != nil || p != 40000 {
		t.Fatalf("got %d err=%v", p, err)
	}
	// 用尽
	if _, _, err := a.Allocate(ctx, "b"); err == nil {
		t.Fatal("应已用尽")
	}
	// 回收后可再分配
	if err := a.Release(ctx, 40000); err != nil {
		t.Fatal(err)
	}
	p2, _, err := a.Allocate(ctx, "c")
	if err != nil || p2 != 40000 {
		t.Errorf("回收后应能再次分配到 40000, got %d err=%v", p2, err)
	}
}
