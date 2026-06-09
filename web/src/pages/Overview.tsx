import { useEffect, useState } from "react";
import { Card, Col, Row, Statistic, message } from "antd";
import { api, type Overview as OverviewData } from "../lib/api";

export default function Overview() {
  const [data, setData] = useState<OverviewData | null>(null);

  useEffect(() => {
    api.overview().then(setData).catch((e) => message.error(String(e)));
  }, []);

  return (
    <Row gutter={16}>
      <Col span={6}>
        <Card>
          <Statistic title="服务总数" value={data?.services ?? 0} />
        </Card>
      </Col>
      <Col span={6}>
        <Card>
          <Statistic title="在线服务" value={data?.online_services ?? 0} valueStyle={{ color: "#3f8600" }} />
        </Card>
      </Col>
      <Col span={6}>
        <Card>
          <Statistic title="接口总数" value={data?.apis ?? 0} />
        </Card>
      </Col>
      <Col span={6}>
        <Card>
          <Statistic title="消费方" value={data?.consumers ?? 0} />
        </Card>
      </Col>
    </Row>
  );
}
