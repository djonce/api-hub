import { Layout, Menu } from "antd";
import {
  ApiOutlined,
  AppstoreOutlined,
  TeamOutlined,
  DashboardOutlined,
  LineChartOutlined,
  FileSearchOutlined,
} from "@ant-design/icons";
import { Link, Route, Routes, useLocation } from "react-router-dom";
import Overview from "./pages/Overview";
import Services from "./pages/Services";
import ServiceDetail from "./pages/ServiceDetail";
import ApiDocs from "./pages/ApiDocs";
import Consumers from "./pages/Consumers";
import Monitoring from "./pages/Monitoring";
import Audit from "./pages/Audit";

const { Header, Sider, Content } = Layout;

export default function App() {
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
        <Header style={{ background: "#fff", paddingLeft: 24, fontWeight: 600 }}>管理后台</Header>
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
