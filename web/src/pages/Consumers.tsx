import { useEffect, useState } from "react";
import { Button, Card, Form, Input, Modal, Space, Table, Typography, message } from "antd";
import type { ColumnsType } from "antd/es/table";
import { api, type Consumer } from "../lib/api";

export default function Consumers() {
  const [data, setData] = useState<Consumer[]>([]);
  const [loading, setLoading] = useState(false);
  const [open, setOpen] = useState(false);
  const [form] = Form.useForm();

  const load = () => {
    setLoading(true);
    api
      .consumers()
      .then((d) => setData(d ?? []))
      .catch((e) => message.error(String(e)))
      .finally(() => setLoading(false));
  };

  useEffect(load, []);

  const create = async () => {
    const v = await form.validateFields();
    try {
      await api.createConsumer(v.name, v.description ?? "");
      message.success("已创建消费方");
      setOpen(false);
      form.resetFields();
      load();
    } catch (e) {
      message.error(String(e));
    }
  };

  const issueKey = async (c: Consumer) => {
    try {
      const k = await api.createKey(c.id, 0);
      Modal.success({
        title: `已为 ${c.name} 签发 API Key`,
        width: 560,
        content: (
          <div>
            <Typography.Paragraph>
              <Typography.Text copyable>{k.api_key}</Typography.Text>
            </Typography.Paragraph>
            <Typography.Paragraph type="secondary" style={{ marginBottom: 0 }}>
              中继模式调用时带请求头 <Typography.Text code>apikey: {k.api_key}</Typography.Text>
              ；中继入口见各服务详情页的「中继入口」。
            </Typography.Paragraph>
          </div>
        ),
      });
    } catch (e) {
      message.error(String(e));
    }
  };

  const columns: ColumnsType<Consumer> = [
    { title: "名称", dataIndex: "name" },
    { title: "描述", dataIndex: "description" },
    {
      title: "操作",
      key: "op",
      render: (_, r) => <a onClick={() => issueKey(r)}>签发 API Key</a>,
    },
  ];

  return (
    <Card
      title="消费方"
      extra={
        <Space>
          <a onClick={load}>刷新</a>
          <Button type="primary" onClick={() => setOpen(true)}>
            新建消费方
          </Button>
        </Space>
      }
    >
      <Table rowKey="id" loading={loading} columns={columns} dataSource={data} />
      <Modal title="新建消费方" open={open} onOk={create} onCancel={() => setOpen(false)}>
        <Form form={form} layout="vertical">
          <Form.Item name="name" label="名称" rules={[{ required: true }]}>
            <Input placeholder="如 order-service / mobile-app" />
          </Form.Item>
          <Form.Item name="description" label="描述">
            <Input.TextArea rows={2} />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  );
}
