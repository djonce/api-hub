import { useEffect, useState } from "react";
import { Card, Table, Tag, message } from "antd";
import type { ColumnsType } from "antd/es/table";
import { api, type AuditEntry } from "../lib/api";

const actionColor: Record<string, string> = {
  "api.update": "blue",
  "key.issue": "gold",
  "consumer.create": "green",
  "service.register": "cyan",
  "service.deregister": "red",
};

export default function Audit() {
  const [data, setData] = useState<AuditEntry[]>([]);
  const [loading, setLoading] = useState(false);

  const load = () => {
    setLoading(true);
    api
      .audit()
      .then((d) => setData(d ?? []))
      .catch((e) => message.error(String(e)))
      .finally(() => setLoading(false));
  };

  useEffect(load, []);

  const columns: ColumnsType<AuditEntry> = [
    { title: "时间", dataIndex: "ts", width: 200, render: (v: string) => new Date(v).toLocaleString() },
    {
      title: "操作",
      dataIndex: "action",
      width: 160,
      render: (v: string) => <Tag color={actionColor[v] ?? "default"}>{v}</Tag>,
    },
    { title: "对象", dataIndex: "target", width: 200 },
    { title: "详情", dataIndex: "detail", ellipsis: true, render: (v: string) => v || "-" },
  ];

  return (
    <Card title="审计日志" extra={<a onClick={load}>刷新</a>}>
      <Table rowKey="id" loading={loading} columns={columns} dataSource={data} />
    </Card>
  );
}
