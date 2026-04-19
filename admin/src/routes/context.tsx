import { memo, useState } from "react";
import { Tag, Progress, Spin, Empty, Button, Segmented } from "antd";
import { ReloadOutlined, DownOutlined, RightOutlined } from "@ant-design/icons";
import { useAgentContext } from "../hooks/useAgentContext";
import type { ContextMessage, ContextSource } from "../lib/api";

const roleColors: Record<string, string> = {
  system: "purple",
  user: "blue",
  assistant: "green",
  tool: "orange",
};

const MessageRow = memo(function MessageRow({ msg }: { msg: ContextMessage }) {
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
});

const CollapsibleSection = memo(function CollapsibleSection({
  label,
  messages,
  color,
}: {
  label: string;
  messages: ContextMessage[];
  color: string;
}) {
  const [open, setOpen] = useState(false);
  return (
    <div style={{ marginBottom: 8, flexShrink: 0 }}>
      <div
        onClick={() => setOpen(!open)}
        style={{
          cursor: "pointer",
          display: "flex",
          alignItems: "center",
          gap: 6,
          color: "rgba(255,255,255,0.6)",
          fontSize: 13,
          userSelect: "none",
          marginBottom: open ? 4 : 0,
        }}
      >
        {open ? <DownOutlined style={{ fontSize: 10 }} /> : <RightOutlined style={{ fontSize: 10 }} />}
        <span>{label} ({messages.length})</span>
      </div>
      {open && (
        <div
          style={{
            background: `rgba(${color},0.1)`,
            border: `1px solid rgba(${color},0.3)`,
            borderRadius: 6,
            maxHeight: 200,
            overflow: "auto",
          }}
        >
          {messages.map((msg, i) => (
            <MessageRow key={`${label}-${i}`} msg={msg} />
          ))}
        </div>
      )}
    </div>
  );
});

const sourceOptions: { label: string; value: ContextSource }[] = [
  { label: "Discord", value: "discord" },
  { label: "Device", value: "device" },
  { label: "Web", value: "web" },
];

export const ContextPage = memo(function ContextPage() {
  const [source, setSource] = useState<ContextSource>("discord");
  const { data, isLoading, refetch } = useAgentContext(source);

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
          <Segmented
            options={sourceOptions}
            value={source}
            onChange={(val) => setSource(val as ContextSource)}
            size="small"
          />
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

      {data.background && data.background.length > 0 && (
        <CollapsibleSection label="Background" messages={data.background} color="114,46,209" />
      )}
      {data.foreground && data.foreground.length > 0 && (
        <CollapsibleSection label="Foreground" messages={data.foreground} color="46,114,209" />
      )}

      <div
        style={{
          flex: 1,
          overflow: "auto",
          background: "rgba(0,0,0,0.25)",
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
});

export default ContextPage;
