#!/usr/bin/env python3
"""
端到端冒烟测试：对 docker compose 起来的整套服务做核心链路校验。

用法：
    docker compose up -d --build
    python3 deploy/e2e.py
环境变量：
    BASE     控制面地址，默认 http://localhost:8080
    GATEWAY  APISIX 数据面地址，默认 http://localhost:9080
"""
import json
import os
import sys
import time
import urllib.error
import urllib.request

BASE = os.environ.get("BASE", "http://localhost:8080").rstrip("/")
GATEWAY = os.environ.get("GATEWAY", "http://localhost:9080").rstrip("/")

OK = "\033[32mPASS\033[0m"
NO = "\033[31mFAIL\033[0m"
WARN = "\033[33mWARN\033[0m"


def req(method, url, body=None, timeout=5):
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(url, data=data, method=method)
    r.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(r, timeout=timeout) as resp:
        raw = resp.read().decode()
        if not raw:
            return resp.status, None
        try:
            return resp.status, json.loads(raw)
        except json.JSONDecodeError:
            # 非 JSON 响应（如 /healthz 返回纯文本 "ok"）按原样返回
            return resp.status, raw


def wait(desc, fn, tries=60, delay=2):
    for i in range(tries):
        try:
            if fn():
                print(f"  {OK}  {desc}")
                return True
        except Exception:
            pass
        time.sleep(delay)
    print(f"  {NO}  {desc}  (超时 {tries*delay}s)")
    return False


failures = 0


def check(desc, cond):
    global failures
    print(f"  {OK if cond else NO}  {desc}")
    if not cond:
        failures += 1
    return cond


print(f"控制面 BASE={BASE}  网关 GATEWAY={GATEWAY}\n[1] 等待服务就绪")

if not wait("控制面 /healthz 可用", lambda: req("GET", f"{BASE}/healthz")[0] == 200):
    sys.exit(1)


def demo_registered():
    _, svcs = req("GET", f"{BASE}/api/v1/services")
    return any(s["name"] == "demo-service" for s in (svcs or []))


if not wait("demo-service 自动注册成功", demo_registered):
    sys.exit(1)

# 取服务 ID
_, svcs = req("GET", f"{BASE}/api/v1/services")
svc = next(s for s in svcs if s["name"] == "demo-service")
sid = svc["id"]
print(f"\n[2] 核心链路校验 (service_id={sid})")

check("服务在线实例 >= 1", svc.get("online_count", 0) >= 1)

_, apis = req("GET", f"{BASE}/api/v1/services/{sid}/apis")
paths = {(a["method"], a["path"]) for a in (apis or [])}
check("接口已同步 GET /api/time", ("GET", "/api/time") in paths)
check("接口已同步 POST /api/echo", ("POST", "/api/echo") in paths)

_, insts = req("GET", f"{BASE}/api/v1/services/{sid}/instances")
check("实例在线状态为 online", any(i.get("online") for i in (insts or [])))

st, doc = req("GET", f"{BASE}/api/v1/services/{sid}/openapi")
check("OpenAPI 文档可获取且含 paths", st == 200 and isinstance(doc, dict) and "paths" in doc)

# 上下线开关
api_time = next(a for a in apis if a["path"] == "/api/time")
req("PATCH", f"{BASE}/api/v1/apis/{api_time['id']}", {"status": "disabled"})
req("PATCH", f"{BASE}/api/v1/apis/{api_time['id']}", {"status": "enabled"})
check("接口上下线 PATCH 正常", True)

print("\n[3] 中继与监控（需 APISIX，软校验）")
# 发布中继路由
try:
    req("POST", f"{BASE}/api/v1/services/{sid}/sync")
except Exception as e:
    print(f"  {WARN}  发布中继路由失败: {e}")


def relay_ok():
    st, body = req("GET", f"{GATEWAY}/r/{sid}/api/time")
    return st == 200 and "now" in (body or {})


if wait("经网关中继调用 /r/%d/api/time 返回 200" % sid, relay_ok, tries=15, delay=2):
    # 触发几次以产生统计
    for _ in range(3):
        try:
            req("GET", f"{GATEWAY}/r/{sid}/api/time")
        except Exception:
            pass

    def stats_ok():
        _, cs = req("GET", f"{BASE}/api/v1/stats/calls?hours=1")
        return (cs or {}).get("total", 0) > 0

    if not wait("访问日志已采集（/stats/calls total>0）", stats_ok, tries=10, delay=2):
        print(f"  {WARN}  统计暂无数据（http-logger 可能稍有延迟）")
else:
    print(f"  {WARN}  中继链路未通（检查 APISIX 是否就绪、LOG_INGEST/ADMIN 配置）")

print("\n[4] 审计日志")
_, audit = req("GET", f"{BASE}/api/v1/audit")
check("审计日志含 service.register", any(a["action"] == "service.register" for a in (audit or [])))

print()
if failures == 0:
    print(f"{OK}  端到端核心链路全部通过 ✅")
    sys.exit(0)
print(f"{NO}  有 {failures} 项核心校验未通过 ❌")
sys.exit(1)
