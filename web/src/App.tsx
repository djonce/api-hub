import { useEffect, useState } from "react";
import { Layout, Menu, Button } from "antd";
import {
  ApiOutlined,
  AppstoreOutlined,
  TeamOutlined,
  DashboardOutlined,
  LineChartOutlined,
  FileSearchOutlined,
  LogoutOutlined,
} from "@ant-design/icons";
import { Link, Route, Routes, useLocation } from "react-router-dom";
import Overview from "./pages/Overview";
import Services from "./pages/Services";
import ServiceDetail from "./pages/ServiceDetail";
import ApiDocs from "./pages/ApiDocs";
import Consumers from "./pages/Consumers";
import Monitoring from "./pages/Monitoring";
import Audit from "./pages/Audit";
import Login from "./pages/Login";
import { api, isAuthed, AUTH_EVENT } from "./lib/api";

const { Header, Sider, Content } = Layout;

// 登录门：未登录显示登录页；登录态变化（登录/登出/会话过期）时自动切换。
export default function App() {
  const [authed, setAuthed] = useState(isAuthed());
  useEffect(() => {
    const onChange = () => setAuthed(isAuthed());
    window.addEventListener(AUTH_EVENT, onChange);
    return () => window.removeEventListener(AUTH_EVENT, onChange);
  }, []);

  if (!authed) return <Login />;
  return <AdminLayout />;
}

function AdminLayout() {
  const loc = useLocation();
  const selected = loc.pathname.startsWith("/services")
    ? "services"
    : loc.pathname.startsWith("/consumers")
    ? "consumers"
    : loc.pathname.startsWith("/monitoring")
    ? "monitoring"
    : loc.pathname.startsWith("/audit")
    ? "audit"
    : "overview";

  return (
    <Layout style={{ minHeight: "100vh" }}>
      <Sider theme="dark">
        <div style={{ color: "#fff", padding: 16, fontSize: 16, fontWeight: 600 }}>
          <ApiOutlined /> API 集合平台
        </div>
        <Menu
          theme="dark"
          mode="inline"
          selectedKeys={[selected]}
          items={[
            { key: "overview", icon: <DashboardOutlined />, label: <Link to="/">概览</Link> },
            { key: "services", icon: <AppstoreOutlined />, label: <Link to="/services">服务与接口</Link> },
            { key: "consumers", icon: <TeamOutlined />, label: <Link to="/consumers">消费方</Link> },
            { key: "monitoring", icon: <LineChartOutlined />, label: <Link to="/monitoring">监控</Link> },
            { key: "audit", icon: <FileSearchOutlined />, label: <Link to="/audit">审计日志</Link> },
          ]}
        />
      </Sider>
      <Layout>
        <Header
          style={{
            background: "#fff",
            paddingLeft: 24,
            paddingRight: 24,
            fontWeight: 600,
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
          }}
        >
          <span>管理后台</span>
          <Button type="text" icon={<LogoutOutlined />} onClick={() => api.logout()}>
            退出登录
          </Button>
        </Header>
        <Content style={{ margin: 24 }}>
          <Routes>
            <Route path="/" element={<Overview />} />
            <Route path="/services" element={<Services />} />
            <Route path="/services/:id" element={<ServiceDetail />} />
            <Route path="/services/:id/docs" element={<ApiDocs />} />
            <Route path="/consumers" element={<Consumers />} />
            <Route path="/monitoring" element={<Monitoring />} />
            <Route path="/audit" element={<Audit />} />
          </Routes>
        </Content>
      </Layout>
    </Layout>
  );
}
