import { useEffect, useState } from "react";
import { Card, Descriptions, Switch, Table, Tag, Select, message } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useParams } from "react-router-dom";
import { api, type Api, type Service } from "../lib/api";

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
  const [loading, setLoading] = useState(false);

  const load = () => {
    setLoading(true);
    Promise.all([api.service(sid), api.serviceApis(sid)])
      .then(([s, a]) => {
        setSvc(s);
        setApis(a ?? []);
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
      message.success("已更新模式");
      load();
    } catch (e) {
      message.error(String(e));
    }
  };

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
      width: 130,
      render: (_, r) => (
        <Select
          size="small"
          style={{ width: 110 }}
          value={r.conn_mode ?? "继承"}
          onChange={(v) => changeMode(r, v)}
          options={[
            { value: "继承", label: "继承服务" },
            { value: "direct", label: "直连" },
            { value: "relay", label: "中继" },
          ]}
        />
      ),
    },
    {
      title: "上线",
      key: "status",
      width: 90,
      render: (_, r) => (
        <Switch checked={r.status === "enabled"} onChange={(on) => toggleStatus(r, on)} />
      ),
    },
  ];

  return (
    <>
      <Card title={`服务详情：${svc?.name ?? ""}`} style={{ marginBottom: 16 }} loading={loading}>
        {svc && (
          <Descriptions column={3} size="small">
            <Descriptions.Item label="版本">{svc.version}</Descriptions.Item>
            <Descriptions.Item label="环境">{svc.env}</Descriptions.Item>
            <Descriptions.Item label="默认模式">{svc.conn_mode}</Descriptions.Item>
            <Descriptions.Item label="负责人">{svc.owner || "-"}</Descriptions.Item>
            <Descriptions.Item label="健康检查">{svc.health_path}</Descriptions.Item>
            <Descriptions.Item label="在线实例">{svc.online_count}</Descriptions.Item>
          </Descriptions>
        )}
      </Card>
      <Card title="接口列表" extra={<a onClick={load}>刷新</a>}>
        <Table rowKey="id" loading={loading} columns={columns} dataSource={apis} pagination={false} />
      </Card>
    </>
  );
}
