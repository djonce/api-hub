import { useEffect, useState } from "react";
import { Badge, Card, Table, Tag, message } from "antd";
import type { ColumnsType } from "antd/es/table";
import { Link } from "react-router-dom";
import { api, type Service } from "../lib/api";

export default function Services() {
  const [data, setData] = useState<Service[]>([]);
  const [loading, setLoading] = useState(false);

  const load = () => {
    setLoading(true);
    api
      .services()
      .then((d) => setData(d ?? []))
      .catch((e) => message.error(String(e)))
      .finally(() => setLoading(false));
  };

  useEffect(load, []);

  const columns: ColumnsType<Service> = [
    {
      title: "服务",
      dataIndex: "name",
      render: (_, r) => <Link to={`/services/${r.id}`}>{r.name}</Link>,
    },
    { title: "版本", dataIndex: "version" },
    { title: "环境", dataIndex: "env", render: (v) => <Tag>{v}</Tag> },
    {
      title: "默认模式",
      dataIndex: "conn_mode",
      render: (v) => <Tag color={v === "relay" ? "blue" : "green"}>{v}</Tag>,
    },
    { title: "负责人", dataIndex: "owner" },
    {
      title: "在线实例",
      dataIndex: "online_count",
      render: (v: number) =>
        v > 0 ? <Badge status="success" text={`${v} 在线`} /> : <Badge status="default" text="离线" />,
    },
  ];

  return (
    <Card title="服务列表" extra={<a onClick={load}>刷新</a>}>
      <Table rowKey="id" loading={loading} columns={columns} dataSource={data} />
    </Card>
  );
}
