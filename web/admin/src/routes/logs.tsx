import { useState, useRef, useEffect, memo } from "react";
import { Select, Space, Tag, Button, Switch } from "antd";
import { ClearOutlined } from "@ant-design/icons";
import { useLogStream } from "../hooks/useLogStream";
import type { LogEntry } from "../lib/api";
import { formatJST } from "../lib/date";

const levelColors: Record<string, string> = {
  DEBUG: "default",
  INFO: "blue",
  WARN: "orange",
  ERROR: "red",
};

function formatValue(v: unknown): string {
  if (typeof v === "string") return v;
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  return JSON.stringify(v);
}

const LogRow = memo(function LogRow({ entry }: { entry: LogEntry }) {
  const time = formatJST(entry.time, "HH:mm:ss.SSS");

  const attrs = entry.attrs ? Object.entries(entry.attrs) : [];

  return (
    <div
      style={{
        display: "flex",
        gap: 8,
        padding: "4px 0",
        fontFamily: "monospace",
        fontSize: 12,
        borderBottom: "1px solid rgba(255,255,255,0.06)",
        alignItems: "baseline",
        flexWrap: "wrap",
        wordBreak: "break-all",
        minWidth: 0,
      }}
    >
      <span style={{ color: "rgba(255,255,255,0.4)", flexShrink: 0 }}>
        {time}
      </span>
      <Tag
        color={levelColors[entry.level] ?? "default"}
        style={{ margin: 0, minWidth: 48, textAlign: "center", flexShrink: 0 }}
      >
        {entry.level}
      </Tag>
      {entry.source && (
        <Tag style={{ margin: 0, flexShrink: 0 }}>{entry.source}</Tag>
      )}
      <span>{entry.msg}</span>
      {attrs.map(([k, v]) => (
        <span key={k}>
          <span style={{ color: "rgba(255,255,255,0.35)" }}>{k}</span>
          <span style={{ color: "rgba(255,255,255,0.2)" }}>=</span>
          <span style={{ color: "rgba(255,255,255,0.55)" }}>{formatValue(v)}</span>
        </span>
      ))}
    </div>
  );
});

export const LogsPage = memo(function LogsPage() {
  const [level, setLevel] = useState<string | undefined>();
  const [source, setSource] = useState<string | undefined>();
  const [autoScroll, setAutoScroll] = useState(true);
  const containerRef = useRef<HTMLDivElement>(null);

  const { logs, connected, clear } = useLogStream({ level, source });

  useEffect(() => {
    if (autoScroll && containerRef.current) {
      containerRef.current.scrollTop = containerRef.current.scrollHeight;
    }
  }, [logs, autoScroll]);

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "calc(100vh - 88px)", minWidth: 0, overflow: "hidden" }}>
      <div
        style={{
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          marginBottom: 12,
          flexWrap: "wrap",
          gap: 8,
          minWidth: 0,
        }}
      >
        <Space>
          <h2 style={{ margin: 0 }}>Logs</h2>
          <Tag color={connected ? "green" : "red"}>
            {connected ? "Connected" : "Disconnected"}
          </Tag>
        </Space>
        <div style={{ display: "flex", flexWrap: "wrap", gap: 8, alignItems: "center" }}>
          <Select
            placeholder="Level"
            allowClear
            style={{ width: 100 }}
            value={level}
            onChange={setLevel}
            options={[
              { label: "DEBUG", value: "DEBUG" },
              { label: "INFO", value: "INFO" },
              { label: "WARN", value: "WARN" },
              { label: "ERROR", value: "ERROR" },
            ]}
          />
          <Select
            placeholder="Source"
            allowClear
            style={{ width: 140 }}
            value={source}
            onChange={setSource}
            options={[
              { label: "agent", value: "agent" },
              { label: "consolidator", value: "consolidator" },
            ]}
          />
          <Space size={4}>
            Auto-scroll
            <Switch checked={autoScroll} onChange={setAutoScroll} size="small" />
          </Space>
          <Button icon={<ClearOutlined />} size="small" onClick={clear}>
            Clear
          </Button>
        </div>
      </div>

      <div
        ref={containerRef}
        style={{
          flex: 1,
          overflowY: "auto",
          overflowX: "hidden",
          background: "rgba(0,0,0,0.25)",
          borderRadius: 6,
          padding: "8px 12px",
        }}
      >
        {logs.length === 0 ? (
          <div
            style={{
              textAlign: "center",
              padding: 48,
              color: "rgba(255,255,255,0.3)",
            }}
          >
            Waiting for log entries...
          </div>
        ) : (
          logs.map((entry) => <LogRow key={entry.seq} entry={entry} />)
        )}
      </div>
    </div>
  );
});

export default LogsPage;
