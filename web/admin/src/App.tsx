import { useState, useEffect, useCallback } from "react";
import { ConfigProvider, Layout, Menu, theme, Drawer, Button, Grid } from "antd";
import {
  DashboardOutlined,
  DatabaseOutlined,
  BarChartOutlined,
  FileTextOutlined,
  EditOutlined,
  TeamOutlined,
  MessageOutlined,
  WifiOutlined,
  ClockCircleOutlined,
  MenuOutlined,
  ApiOutlined,
} from "@ant-design/icons";
import { DashboardPage } from "./routes/index";
import { MemoriesPage } from "./routes/memories/index";
import { MemoryDetailPage } from "./routes/memories/$id";
import { MetricsPage } from "./routes/metrics";
import { LogsPage } from "./routes/logs";
import { UsersPage } from "./routes/users/index";
import { ContextPage } from "./routes/context";
import { FeedsPage } from "./routes/feeds/index";
import { DiscordPage } from "./routes/discord/index";
import { PromptsPage } from "./routes/prompts";
import { ActionsPage } from "./routes/actions";

const { Sider, Header, Content } = Layout;
const { useBreakpoint } = Grid;

type Page =
  | { key: "dashboard" }
  | { key: "memories" }
  | { key: "memory-detail"; id: string }
  | { key: "feeds" }
  | { key: "discord" }
  | { key: "users" }
  | { key: "actions" }
  | { key: "prompts" }
  | { key: "metrics" }
  | { key: "context" }
  | { key: "logs" };

/** Parse location hash into a Page. */
function parseHash(): Page {
  const hash = window.location.hash.replace("#", "");
  if (!hash) return { key: "dashboard" };
  if (hash.startsWith("memory/")) {
    return { key: "memory-detail", id: hash.slice("memory/".length) };
  }
  const valid = ["dashboard", "memories", "feeds", "discord", "users", "actions", "prompts", "metrics", "context", "logs"];
  if (valid.includes(hash)) return { key: hash } as Page;
  return { key: "dashboard" };
}

export function App() {
  const [page, setPageState] = useState<Page>(parseHash);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const screens = useBreakpoint();
  const isMobile = !screens.md;

  const setPage = useCallback((p: Page) => {
    const hash = p.key === "memory-detail" ? `memory/${p.id}` : p.key === "dashboard" ? "" : p.key;
    window.location.hash = hash;
    setPageState(p);
  }, []);

  useEffect(() => {
    const onHashChange = () => setPageState(parseHash());
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);

  const menuItems = [
    { key: "dashboard", icon: <DashboardOutlined />, label: "Dashboard" },
    { key: "memories", icon: <DatabaseOutlined />, label: "Memories" },
    { key: "feeds", icon: <WifiOutlined />, label: "Feeds" },
    { key: "discord", icon: <ApiOutlined />, label: "Discord" },
    { key: "users", icon: <TeamOutlined />, label: "Users" },
    { key: "actions", icon: <ClockCircleOutlined />, label: "Actions" },
    { key: "prompts", icon: <EditOutlined />, label: "Prompts" },
    { key: "metrics", icon: <BarChartOutlined />, label: "Metrics" },
    { key: "context", icon: <MessageOutlined />, label: "Context" },
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
      case "feeds":
        return <FeedsPage />;
      case "discord":
        return <DiscordPage />;
      case "users":
        return <UsersPage />;
      case "actions":
        return <ActionsPage />;
      case "prompts":
        return <PromptsPage />;
      case "metrics":
        return <MetricsPage />;
      case "context":
        return <ContextPage />;
      case "logs":
        return <LogsPage />;
    }
  }

  const menuProps = {
    theme: "dark" as const,
    mode: "inline" as const,
    selectedKeys: [page.key === "memory-detail" ? "memories" : page.key],
    items: menuItems,
    onClick: ({ key }: { key: string }) => {
      setPage({ key } as Page);
      setDrawerOpen(false);
    },
  };

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
        {!isMobile && (
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
            <Menu {...menuProps} />
          </Sider>
        )}

        {isMobile && (
          <Drawer
            placement="left"
            open={drawerOpen}
            onClose={() => setDrawerOpen(false)}
            width={240}
            styles={{ body: { padding: 0, background: "#001529" } }}
          >
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
            <Menu {...menuProps} />
          </Drawer>
        )}

        <Layout>
          {isMobile && (
            <Header
              style={{
                padding: "0 12px",
                background: "#001529",
                display: "flex",
                alignItems: "center",
                height: 48,
                lineHeight: "48px",
              }}
            >
              <Button
                type="text"
                icon={<MenuOutlined />}
                onClick={() => setDrawerOpen(true)}
                style={{ color: "#fff", fontSize: 18 }}
              />
              <span
                style={{
                  color: "#fff",
                  fontWeight: 700,
                  marginLeft: 12,
                  fontSize: 15,
                }}
              >
                suzuha admin
              </span>
            </Header>
          )}
          <Content style={{ padding: isMobile ? 12 : 24, overflow: "auto" }}>
            {renderPage()}
          </Content>
        </Layout>
      </Layout>
    </ConfigProvider>
  );
}
