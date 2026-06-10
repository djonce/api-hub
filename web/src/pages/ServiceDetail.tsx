import { useEffect, useState } from "react";
import { Badge, Button, Card, Descriptions, InputNumber, Space, Switch, Table, Tag, Select, Typography, message } from "antd";
import type { ColumnsType } from "antd/es/table";
import { Link, useParams } from "react-router-dom";
import { api, type Api, type AppConfig, type Instance, type Service } from "../lib/api";

const methodColor: Record<string, string> = {
  GET: "green",
  POST: "blue",
  PUT: "orange",
  PATCH: "gold",
  DELETE: "red",
};

export default function ServiceDetail() {
  const { id } = useParams();
  const sid = Number(id);
  const [svc, setSvc] = useState<Service | null>(null);
  const [apis, setApis] = useState<Api[]>([]);
  const [instances, setInstances] = useState<Instance[]>([]);
  const [cfg, setCfg] = useState<AppConfig | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    api.config().then(setCfg).catch(() => {});
  }, []);

  const load = () => {
    setLoading(true);
    Promise.all([api.service(sid), api.serviceApis(sid), api.serviceInstances(sid)])
      .then(([s, a, ins]) => {
        setSvc(s);
        setApis(a ?? []);
        setInstances(ins ?? []);
      })
      .catch((e) => message.error(String(e)))
      .finally(() => setLoading(false));
  };

  useEffect(load, [sid]);

  const toggleStatus = async (row: Api, on: boolean) => {
    try {
      await api.patchApi(row.id, { status: on ? "enabled" : "disabled" });
      message.success(`${row.method} ${row.path} 已${on ? "上线" : "下线"}`);
      load();
    } catch (e) {
      message.error(String(e));
    }
  };

  const changeMode = async (row: Api, mode: string) => {
    try {
      await api.patchApi(row.id, { conn_mode: mode });
      message.success("已更新调用模式");
      load();
    } catch (e) {
      message.error(String(e));
    }
  };

  const changeRate = async (row: Api, rate: number | null) => {
    try {
      await api.patchApi(row.id, { rate_limit: rate ?? 0 });
      message.success("已更新限流");
      load();
    } catch (e) {
      message.error(String(e));
    }
  };

  const toggleBreaker = async (row: Api, on: boolean) => {
    try {
      await api.patchApi(row.id, { breaker_enabled: on });
      message.success(`熔断已${on ? "开启" : "关闭"}`);
      load();
    } catch (e) {
      message.error(String(e));
    }
  };

  const republish = async () => {
    try {
      await api.syncService(sid);
      message.success("已重新发布中继路由到网关");
    } catch (e) {
      message.error(String(e));
    }
  };

  const relayBase = cfg?.apisix_enabled ? `${cfg.gateway_url}/r/${sid}` : "";

  const columns: ColumnsType<Api> = [
    {
      title: "方法",
      dataIndex: "method",
      width: 90,
      render: (m: string) => <Tag color={methodColor[m] ?? "default"}>{m}</Tag>,
    },
    { title: "路径", dataIndex: "path" },
    { title: "说明", dataIndex: "summary" },
    { title: "分组", dataIndex: "group", render: (g: string) => (g ? <Tag>{g}</Tag> : "-") },
    { title: "鉴权", dataIndex: "auth_required", render: (v: boolean) => (v ? "是" : "否") },
    {
      title: "调用模式",
      key: "mode",
      width: 110,
      render: (_, r) => (
        <Select
          size="small"
          style={{ width: 90 }}
          value={r.conn_mode ?? svc?.conn_mode ?? "relay"}
          onChange={(v) => changeMode(r, v)}
          options={[
            { value: "direct", label: "直连" },
            { value: "relay", label: "中继" },
          ]}
        />
      ),
    },
    {
      title: "限流(次/分)",
      key: "rate",
      width: 120,
      render: (_, r) => (
        <InputNumber
          size="small"
          min={0}
          style={{ width: 90 }}
          value={r.rate_limit}
          onChange={(v) => changeRate(r, v as number | null)}
          placeholder="0=不限"
        />
      ),
    },
    {
      title: "熔断",
      key: "breaker",
      width: 70,
      render: (_, r) => (
        <Switch
          size="small"
          checked={r.breaker_enabled}
          onChange={(on) => toggleBreaker(r, on)}
        />
      ),
    },
    {
      title: "上线",
      key: "status",
      width: 70,
      render: (_, r) => (
        <Switch checked={r.status === "enabled"} onChange={(on) => toggleStatus(r, on)} />
      ),
    },
  ];

  const instanceColumns: ColumnsType<Instance> = [
    {
      title: "状态",
      dataIndex: "online",
      width: 90,
      render: (v: boolean) =>
        v ? <Badge status="success" text="在线" /> : <Badge status="default" text="离线" />,
    },
    {
      title: "直连地址",
      dataIndex: "direct_url",
      render: (v: string) => (v ? <Typography.Text copyable>{v}</Typography.Text> : "-"),
    },
    { title: "frp 端口", dataIndex: "frp_remote_port", width: 110 },
    { title: "本地端口", dataIndex: "local_port", width: 110 },
    { title: "实例ID", dataIndex: "instance_uid", ellipsis: true },
  ];

  return (
    <>
      <Card
        title={`服务详情：${svc?.name ?? ""}`}
        style={{ marginBottom: 16 }}
        loading={loading}
        extra={
          <Space>
            <a onClick={load}>刷新</a>
            {relayBase && (
              <Button onClick={republish}>重新发布中继路由</Button>
            )}
            <Link to={`/services/${sid}/docs`}>
              <Button type="primary">查看接口文档</Button>
            </Link>
          </Space>
        }
      >
        {svc && (
          <Descriptions column={3} size="small">
            <Descriptions.Item label="版本">{svc.version}</Descriptions.Item>
            <Descriptions.Item label="环境">{svc.env}</Descriptions.Item>
            <Descriptions.Item label="默认模式">{svc.conn_mode}</Descriptions.Item>
            <Descriptions.Item label="负责人">{svc.owner || "-"}</Descriptions.Item>
            <Descriptions.Item label="健康检查">{svc.health_path}</Descriptions.Item>
            <Descriptions.Item label="在线实例">{svc.online_count}</Descriptions.Item>
            {relayBase && (
              <Descriptions.Item label="中继入口" span={3}>
                <Typography.Text copyable>{relayBase}</Typography.Text>
                <span style={{ color: "#999", marginLeft: 8 }}>
                  （调用：{relayBase}/接口路径，需请求头 apikey）
                </span>
              </Descriptions.Item>
            )}
          </Descriptions>
        )}
      </Card>
      <Card title="运行实例" style={{ marginBottom: 16 }} loading={loading}>
        <Table
          rowKey="id"
          size="small"
          columns={instanceColumns}
          dataSource={instances}
          pagination={false}
          locale={{ emptyText: "暂无在线实例（服务未启动或未接入 Agent）" }}
        />
      </Card>
      <Card title="接口列表" extra={<a onClick={load}>刷新</a>}>
        <Table rowKey="id" loading={loading} columns={columns} dataSource={apis} pagination={false} />
      </Card>
    </>
  );
}
