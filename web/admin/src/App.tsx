import { useState } from "react";
import { ConfigProvider, Layout, Menu, theme } from "antd";
import {
  DashboardOutlined,
  DatabaseOutlined,
  BarChartOutlined,
  FileTextOutlined,
} from "@ant-design/icons";
import { DashboardPage } from "./routes/index";
import { MemoriesPage } from "./routes/memories/index";
import { MemoryDetailPage } from "./routes/memories/$id";
import { MetricsPage } from "./routes/metrics";
import { LogsPage } from "./routes/logs";

const { Sider, Content } = Layout;

type Page =
  | { key: "dashboard" }
  | { key: "memories" }
  | { key: "memory-detail"; id: string }
  | { key: "metrics" }
  | { key: "logs" };

export function App() {
  const [page, setPage] = useState<Page>({ key: "dashboard" });

  const menuItems = [
    { key: "dashboard", icon: <DashboardOutlined />, label: "Dashboard" },
    { key: "memories", icon: <DatabaseOutlined />, label: "Memories" },
    { key: "metrics", icon: <BarChartOutlined />, label: "Metrics" },
    { key: "logs", icon: <FileTextOutlined />, label: "Logs" },
  ];

  const navigateToMemory = (id: string) =>
    setPage({ key: "memory-detail", id });
  const navigateBack = () => setPage({ key: "memories" });

  function renderPage() {
    switch (page.key) {
      case "dashboard":
        return <DashboardPage />;
      case "memories":
        return <MemoriesPage onViewDetail={navigateToMemory} />;
      case "memory-detail":
        return <MemoryDetailPage id={page.id} onBack={navigateBack} />;
      case "metrics":
        return <MetricsPage />;
      case "logs":
        return <LogsPage />;
    }
  }

  return (
    <ConfigProvider
      theme={{
        algorithm: theme.darkAlgorithm,
        token: {
          colorPrimary: "#7c3aed",
          borderRadius: 6,
        },
      }}
    >
      <Layout style={{ minHeight: "100vh" }}>
        <Sider width={200} theme="dark">
          <div
            style={{
              padding: "16px",
              fontSize: "16px",
              fontWeight: 700,
              color: "#fff",
              borderBottom: "1px solid rgba(255,255,255,0.1)",
            }}
          >
            suzuha admin
          </div>
          <Menu
            theme="dark"
            mode="inline"
            selectedKeys={[
              page.key === "memory-detail" ? "memories" : page.key,
            ]}
            items={menuItems}
            onClick={({ key }) => setPage({ key } as Page)}
          />
        </Sider>
        <Layout>
          <Content style={{ padding: 24, overflow: "auto" }}>
            {renderPage()}
          </Content>
        </Layout>
      </Layout>
    </ConfigProvider>
  );
}
