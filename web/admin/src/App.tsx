import { useState, useEffect, useCallback, lazy, Suspense } from "react";
import { ConfigProvider, Layout, Menu, theme, Drawer, Button, Grid, Spin } from "antd";
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
  EnvironmentOutlined,
  ToolOutlined,
  ExperimentOutlined,
  HeartOutlined,
  ScheduleOutlined,
} from "@ant-design/icons";
import { ErrorBoundary } from "./components/ErrorBoundary";

const DashboardPage = lazy(() => import("./routes/index"));
const MemoriesPage = lazy(() => import("./routes/memories/index"));
const MemoryDetailPage = lazy(() => import("./routes/memories/$id"));
const MetricsPage = lazy(() => import("./routes/metrics"));
const LogsPage = lazy(() => import("./routes/logs"));
const UsersPage = lazy(() => import("./routes/users/index"));
const ContextPage = lazy(() => import("./routes/context"));
const FeedsPage = lazy(() => import("./routes/feeds/index"));
const DiscordPage = lazy(() => import("./routes/discord/index"));
const PromptsPage = lazy(() => import("./routes/prompts"));
const ActionsPage = lazy(() => import("./routes/actions"));
const LocationPage = lazy(() => import("./routes/location"));
const ToolsPage = lazy(() => import("./routes/tools"));
const PlaygroundPage = lazy(() => import("./routes/playground"));
const PreferencesPage = lazy(() => import("./routes/preferences"));
const SchedulerPage = lazy(() => import("./routes/scheduler"));

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
  | { key: "location" }
  | { key: "tools" }
  | { key: "playground" }
  | { key: "preferences" }
  | { key: "scheduler" }
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
  const valid = ["dashboard", "memories", "feeds", "discord", "users", "actions", "location", "tools", "playground", "preferences", "scheduler", "prompts", "metrics", "context", "logs"];
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
    { key: "location", icon: <EnvironmentOutlined />, label: "Location" },
    { key: "tools", icon: <ToolOutlined />, label: "Tools" },
    { key: "playground", icon: <ExperimentOutlined />, label: "Playground" },
    { key: "preferences", icon: <HeartOutlined />, label: "Preferences" },
    { key: "scheduler", icon: <ScheduleOutlined />, label: "Scheduler" },
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
      case "location":
        return <LocationPage />;
      case "tools":
        return <ToolsPage />;
      case "playground":
        return <PlaygroundPage />;
      case "preferences":
        return <PreferencesPage />;
      case "scheduler":
        return <SchedulerPage />;
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
      getPopupContainer={(triggerNode) => triggerNode?.parentElement || document.body}
      theme={{
        algorithm: theme.darkAlgorithm,
        token: {
          colorPrimary: "#06b6d4",
          colorInfo: "#0ea5e9",
          colorLink: "#22d3ee",
          borderRadius: 8,
          colorBgContainer: "#111827",
          colorBgElevated: "#1a2332",
          colorBgLayout: "#0b1120",
          colorBorder: "rgba(255,255,255,0.08)",
          colorBorderSecondary: "rgba(255,255,255,0.05)",
        },
        components: {
          Menu: {
            darkItemBg: "transparent",
            darkSubMenuItemBg: "transparent",
            darkItemSelectedBg: "rgba(6,182,212,0.15)",
            darkItemHoverBg: "rgba(255,255,255,0.06)",
            darkItemSelectedColor: "#22d3ee",
            itemMarginInline: 8,
            itemBorderRadius: 8,
          },
          Card: {
            colorBgContainer: "rgba(255,255,255,0.03)",
            colorBorderSecondary: "rgba(255,255,255,0.06)",
          },
          Table: {
            colorBgContainer: "transparent",
            headerBg: "rgba(255,255,255,0.03)",
            rowHoverBg: "rgba(6,182,212,0.06)",
            borderColor: "rgba(255,255,255,0.06)",
          },
          Modal: {
            contentBg: "#1a2332",
            headerBg: "#1a2332",
          },
          Input: {
            colorBgContainer: "rgba(255,255,255,0.04)",
            activeBorderColor: "#06b6d4",
          },
          Select: {
            colorBgContainer: "rgba(255,255,255,0.04)",
          },
          Statistic: {
            titleFontSize: 13,
          },
        },
      }}
    >
      <Layout style={{ height: "100vh", background: "#0b1120", overflow: "hidden" }}>
        {!isMobile && (
          <Sider
            width={220}
            style={{
              background: "linear-gradient(180deg, #0d1526 0%, #091018 100%)",
              borderRight: "1px solid rgba(255,255,255,0.06)",
              height: "100vh",
              overflow: "auto",
              position: "sticky",
              top: 0,
            }}
          >
            <div
              style={{
                padding: "20px 16px",
                display: "flex",
                alignItems: "center",
                gap: 10,
                borderBottom: "1px solid rgba(255,255,255,0.06)",
              }}
            >
              <div
                style={{
                  width: 28,
                  height: 28,
                  borderRadius: 8,
                  background: "linear-gradient(135deg, #06b6d4, #0ea5e9)",
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                  fontSize: 14,
                  fontWeight: 700,
                  color: "#fff",
                  flexShrink: 0,
                }}
              >
                S
              </div>
              <span style={{ fontSize: 15, fontWeight: 600, color: "rgba(255,255,255,0.9)", letterSpacing: "0.02em" }}>
                suzuha admin
              </span>
            </div>
            <div style={{ padding: "8px 0" }}>
              <Menu {...menuProps} />
            </div>
          </Sider>
        )}

        {isMobile && (
          <Drawer
            placement="left"
            open={drawerOpen}
            onClose={() => setDrawerOpen(false)}
            width={260}
            styles={{
              body: {
                padding: 0,
                background: "linear-gradient(180deg, #0d1526 0%, #091018 100%)",
              },
            }}
          >
            <div
              style={{
                padding: "20px 16px",
                display: "flex",
                alignItems: "center",
                gap: 10,
                borderBottom: "1px solid rgba(255,255,255,0.06)",
              }}
            >
              <div
                style={{
                  width: 28,
                  height: 28,
                  borderRadius: 8,
                  background: "linear-gradient(135deg, #06b6d4, #0ea5e9)",
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                  fontSize: 14,
                  fontWeight: 700,
                  color: "#fff",
                  flexShrink: 0,
                }}
              >
                S
              </div>
              <span style={{ fontSize: 15, fontWeight: 600, color: "rgba(255,255,255,0.9)", letterSpacing: "0.02em" }}>
                suzuha admin
              </span>
            </div>
            <div style={{ padding: "8px 0" }}>
              <Menu {...menuProps} />
            </div>
          </Drawer>
        )}

        <Layout style={{ background: "#0b1120" }}>
          {isMobile && (
            <Header
              style={{
                padding: "0 16px",
                background: "rgba(13,21,38,0.95)",
                backdropFilter: "blur(8px)",
                display: "flex",
                alignItems: "center",
                height: 48,
                lineHeight: "48px",
                borderBottom: "1px solid rgba(255,255,255,0.06)",
              }}
            >
              <Button
                type="text"
                icon={<MenuOutlined />}
                onClick={() => setDrawerOpen(true)}
                style={{ color: "rgba(255,255,255,0.8)", fontSize: 18 }}
              />
              <span
                style={{
                  color: "rgba(255,255,255,0.9)",
                  fontWeight: 600,
                  marginLeft: 12,
                  fontSize: 15,
                }}
              >
                suzuha admin
              </span>
            </Header>
          )}
          <Content style={{ padding: isMobile ? 12 : 24, overflow: "auto", height: "100vh" }}>
            <ErrorBoundary key={page.key}>
              <Suspense fallback={<Spin style={{ display: "block", margin: "80px auto" }} />}>
                {renderPage()}
              </Suspense>
            </ErrorBoundary>
          </Content>
        </Layout>
      </Layout>
    </ConfigProvider>
  );
}
