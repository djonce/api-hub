import { useEffect, useState } from "react";
import { Card, Col, Row, Segmented, Statistic, Table, message } from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  Bar,
  BarChart,
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { api, type CallStats, type PathCount } from "../lib/api";

export default function Monitoring() {
  const [hours, setHours] = useState(24);
  const [data, setData] = useState<CallStats | null>(null);

  const load = (h: number) => {
    api
      .callStats(h)
      .then(setData)
      .catch((e) => message.error(String(e)));
  };

  useEffect(() => load(hours), [hours]);

  const successRate =
    data && data.total > 0 ? ((data.success / data.total) * 100).toFixed(1) + "%" : "-";

  const topColumns: ColumnsType<PathCount> = [
    { title: "接口路径", dataIndex: "path" },
    { title: "调用次数", dataIndex: "count", width: 120 },
  ];

  return (
    <>
      <Card
        style={{ marginBottom: 16 }}
        title="调用监控"
        extra={
          <Segmented
            value={hours}
            onChange={(v) => setHours(v as number)}
            options={[
              { label: "近1小时", value: 1 },
              { label: "近24小时", value: 24 },
              { label: "近7天", value: 168 },
            ]}
          />
        }
      >
        <Row gutter={16}>
          <Col span={6}>
            <Statistic title="总调用" value={data?.total ?? 0} />
          </Col>
          <Col span={6}>
            <Statistic title="成功率" value={successRate} valueStyle={{ color: "#3f8600" }} />
          </Col>
          <Col span={6}>
            <Statistic title="成功(<400)" value={data?.success ?? 0} />
          </Col>
          <Col span={6}>
            <Statistic title="错误(>=400)" value={data?.error ?? 0} valueStyle={{ color: "#cf1322" }} />
          </Col>
        </Row>
      </Card>

      <Row gutter={16}>
        <Col span={16}>
          <Card title="调用时序（按分钟）" style={{ marginBottom: 16 }}>
            <ResponsiveContainer width="100%" height={280}>
              <LineChart data={data?.series ?? []}>
                <CartesianGrid strokeDasharray="3 3" />
                <XAxis dataKey="t" tick={{ fontSize: 11 }} minTickGap={24} />
                <YAxis allowDecimals={false} />
                <Tooltip />
                <Line type="monotone" dataKey="count" stroke="#1677ff" dot={false} />
              </LineChart>
            </ResponsiveContainer>
          </Card>
        </Col>
        <Col span={8}>
          <Card title="按状态码分布" style={{ marginBottom: 16 }}>
            <ResponsiveContainer width="100%" height={280}>
              <BarChart data={data?.by_status ?? []}>
                <CartesianGrid strokeDasharray="3 3" />
                <XAxis dataKey="status" />
                <YAxis allowDecimals={false} />
                <Tooltip />
                <Bar dataKey="count" fill="#52c41a" />
              </BarChart>
            </ResponsiveContainer>
          </Card>
        </Col>
      </Row>

      <Card title="Top 接口">
        <Table
          rowKey="path"
          size="small"
          columns={topColumns}
          dataSource={data?.top_apis ?? []}
          pagination={false}
          locale={{ emptyText: "暂无调用数据（中继接口产生流量后这里会显示）" }}
        />
      </Card>
    </>
  );
}
