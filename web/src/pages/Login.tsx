import { useState } from "react";
import { Card, Form, Input, Button, message } from "antd";
import { UserOutlined, LockOutlined, ApiOutlined } from "@ant-design/icons";
import { api } from "../lib/api";

// 管理后台登录页。登录成功后由 App 监听 AUTH_EVENT 自动切换到后台。
export default function Login() {
  const [loading, setLoading] = useState(false);

  const onFinish = async (v: { username: string; password: string }) => {
    setLoading(true);
    try {
      await api.login(v.username.trim(), v.password);
    } catch (e) {
      message.error(e instanceof Error ? e.message : "登录失败");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div
      style={{
        minHeight: "100vh",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        background: "#f0f2f5",
      }}
    >
      <Card style={{ width: 360, boxShadow: "0 2px 12px rgba(0,0,0,0.08)" }}>
        <div style={{ textAlign: "center", marginBottom: 24, fontSize: 20, fontWeight: 600 }}>
          <ApiOutlined /> API 集合平台
        </div>
        <Form onFinish={onFinish}>
          <Form.Item name="username" rules={[{ required: true, message: "请输入用户名" }]}>
            <Input prefix={<UserOutlined />} placeholder="用户名" size="large" autoFocus />
          </Form.Item>
          <Form.Item name="password" rules={[{ required: true, message: "请输入密码" }]}>
            <Input.Password prefix={<LockOutlined />} placeholder="密码" size="large" />
          </Form.Item>
          <Button type="primary" htmlType="submit" block size="large" loading={loading}>
            登录
          </Button>
        </Form>
      </Card>
    </div>
  );
}
