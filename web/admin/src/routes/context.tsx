import { Tag, Progress, Spin, Empty, Button } from "antd";
import { ReloadOutlined } from "@ant-design/icons";
import { useAgentContext } from "../hooks/useAgentContext";
import type { ContextMessage } from "../lib/api";

const roleColors: Record<string, string> = {
  system: "purple",
  user: "blue",
  assistant: "green",
  tool: "orange",
};

function MessageRow({ msg }: { msg: ContextMessage }) {
  return (
    <div
      style={{
        display: "flex",
        gap: 8,
        padding: "6px 8px",
        borderBottom: "1px solid rgba(255,255,255,0.06)",
        alignItems: "flex-start",
        fontSize: 13,
      }}
    >
      <Tag
        color={roleColors[msg.role] ?? "default"}
        style={{ margin: 0, flexShrink: 0, minWidth: 64, textAlign: "center" }}
      >
        {msg.role}
      </Tag>
      <span
        style={{
          color: "rgba(255,255,255,0.5)",
          flexShrink: 0,
          fontWeight: 500,
          fontSize: 12,
        }}
      >
        {msg.user_name ?? ""}
      </span>
      <div style={{ flex: 1, minWidth: 0 }}>
        <pre
          style={{
            margin: 0,
            fontFamily: "monospace",
            fontSize: 12,
            whiteSpace: "pre-wrap",
            wordBreak: "break-word",
            color: "rgba(255,255,255,0.85)",
          }}
        >
          {msg.content}
        </pre>
        {msg.tool_call_id && (
          <span style={{ fontSize: 11, color: "rgba(255,255,255,0.3)" }}>
            tool_call_id={msg.tool_call_id}
          </span>
        )}
        {msg.tool_calls && msg.tool_calls.length > 0 && (
          <span style={{ fontSize: 11, color: "rgba(255,255,255,0.3)", marginLeft: 8 }}>
            tool_calls={msg.tool_calls.length}
          </span>
        )}
      </div>
    </div>
  );
}

export function ContextPage() {
  const { data, isLoading, refetch } = useAgentContext();

  if (isLoading) {
    return (
      <div style={{ textAlign: "center", padding: 48 }}>
        <Spin />
      </div>
    );
  }

  if (!data) {
    return <Empty description="Agent unreachable" />;
  }

  const usagePct = Math.round(data.usage_ratio * 100);

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "calc(100vh - 88px)" }}>
      <div
        style={{
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          marginBottom: 12,
          flexWrap: "wrap",
          gap: 8,
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
          <h2 style={{ margin: 0 }}>Context</h2>
          <span style={{ color: "rgba(255,255,255,0.5)", fontSize: 13 }}>
            {data.count} msgs
          </span>
          <span style={{ color: "rgba(255,255,255,0.5)", fontSize: 13 }}>
            ~{data.estimated_tokens.toLocaleString()} tok
          </span>
          <Progress
            percent={usagePct}
            size="small"
            style={{ width: 100, margin: 0 }}
            strokeColor={usagePct > 80 ? "#f5222d" : usagePct > 60 ? "#faad14" : "#52c41a"}
            format={(pct) => `${pct}%`}
          />
        </div>
        <Button icon={<ReloadOutlined />} size="small" onClick={() => refetch()}>
          Refresh
        </Button>
      </div>

      <div
        style={{
          flex: 1,
          overflow: "auto",
          background: "rgba(0,0,0,0.3)",
          borderRadius: 6,
        }}
      >
        {data.messages.length === 0 ? (
          <div
            style={{
              textAlign: "center",
              padding: 48,
              color: "rgba(255,255,255,0.3)",
            }}
          >
            Context is empty
          </div>
        ) : (
          data.messages.map((msg, i) => (
            <MessageRow key={`${i}-${msg.timestamp}`} msg={msg} />
          ))
        )}
      </div>
    </div>
  );
}
