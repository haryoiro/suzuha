import { useState, useRef, useEffect } from "react";
import { Select, Space, Tag, Button, Switch } from "antd";
import { ClearOutlined } from "@ant-design/icons";
import { useLogStream } from "../hooks/useLogStream";
import type { LogEntry } from "../lib/api";

const levelColors: Record<string, string> = {
  DEBUG: "default",
  INFO: "blue",
  WARN: "orange",
  ERROR: "red",
};

function LogRow({ entry }: { entry: LogEntry }) {
  const time = new Date(entry.time).toLocaleTimeString("ja-JP", {
    hour12: false,
    fractionalSecondDigits: 3,
  });

  return (
    <div
      style={{
        display: "flex",
        gap: 8,
        padding: "4px 0",
        fontFamily: "monospace",
        fontSize: 12,
        borderBottom: "1px solid rgba(255,255,255,0.06)",
        alignItems: "flex-start",
      }}
    >
      <span style={{ color: "rgba(255,255,255,0.4)", whiteSpace: "nowrap" }}>
        {time}
      </span>
      <Tag
        color={levelColors[entry.level] ?? "default"}
        style={{ margin: 0, minWidth: 48, textAlign: "center" }}
      >
        {entry.level}
      </Tag>
      {entry.source && (
        <Tag style={{ margin: 0 }}>{entry.source}</Tag>
      )}
      <span style={{ flex: 1, wordBreak: "break-all" }}>{entry.msg}</span>
      {entry.attrs && Object.keys(entry.attrs).length > 0 && (
        <span style={{ color: "rgba(255,255,255,0.3)", whiteSpace: "nowrap" }}>
          {JSON.stringify(entry.attrs)}
        </span>
      )}
    </div>
  );
}

export function LogsPage() {
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
    <div style={{ display: "flex", flexDirection: "column", height: "calc(100vh - 88px)" }}>
      <div
        style={{
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          marginBottom: 12,
        }}
      >
        <Space>
          <h2 style={{ margin: 0 }}>Logs</h2>
          <Tag color={connected ? "green" : "red"}>
            {connected ? "Connected" : "Disconnected"}
          </Tag>
        </Space>
        <Space>
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
          <Space>
            Auto-scroll
            <Switch checked={autoScroll} onChange={setAutoScroll} size="small" />
          </Space>
          <Button icon={<ClearOutlined />} size="small" onClick={clear}>
            Clear
          </Button>
        </Space>
      </div>

      <div
        ref={containerRef}
        style={{
          flex: 1,
          overflow: "auto",
          background: "rgba(0,0,0,0.3)",
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
}
