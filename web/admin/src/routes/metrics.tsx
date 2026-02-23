import { Card, Col, Row, Statistic, Table, Spin, Tag } from "antd";
import { useMetrics } from "../hooks/useMetrics";
import type { MetricItem } from "../lib/api";
import type { ColumnsType } from "antd/es/table";

function findMetric(
  metrics: MetricItem[] | undefined,
  name: string
): MetricItem | undefined {
  return metrics?.find(
    (m) => m.name === name && Object.keys(m.labels ?? {}).length === 0
  );
}

function findMetrics(
  metrics: MetricItem[] | undefined,
  name: string
): MetricItem[] {
  return (metrics ?? []).filter((m) => m.name === name);
}

export function MetricsPage() {
  const { data, isLoading } = useMetrics();
  const metrics = data?.metrics;

  if (isLoading) {
    return (
      <div style={{ textAlign: "center", padding: 48 }}>
        <Spin size="large" />
      </div>
    );
  }

  const tokensIn = findMetric(metrics, "suzuha_llm_tokens_input_total");
  const tokensOut = findMetric(metrics, "suzuha_llm_tokens_output_total");
  const latency = findMetric(metrics, "suzuha_llm_latency_seconds");
  const contextUsage = findMetric(
    metrics,
    "suzuha_context_window_usage_ratio"
  );
  const memWrites = findMetric(metrics, "suzuha_memory_writes_total");
  const toolCalls = findMetrics(metrics, "suzuha_tool_calls_total");
  const events = findMetrics(metrics, "suzuha_events_total");

  const toolColumns: ColumnsType<MetricItem> = [
    {
      title: "Tool",
      render: (_, r) => r.labels?.tool ?? "—",
    },
    {
      title: "Status",
      render: (_, r) => {
        const s = r.labels?.status ?? "";
        return (
          <Tag color={s === "success" ? "green" : s === "error" ? "red" : "default"}>
            {s || "—"}
          </Tag>
        );
      },
    },
    {
      title: "Count",
      render: (_, r) => r.value?.toLocaleString() ?? 0,
      align: "right" as const,
    },
  ];

  const eventColumns: ColumnsType<MetricItem> = [
    {
      title: "Source",
      render: (_, r) => r.labels?.source ?? "—",
    },
    {
      title: "Type",
      render: (_, r) => r.labels?.type ?? "—",
    },
    {
      title: "Count",
      render: (_, r) => r.value?.toLocaleString() ?? 0,
      align: "right" as const,
    },
  ];

  return (
    <div>
      <h2 style={{ marginBottom: 24 }}>Metrics</h2>
      <p style={{ color: "rgba(255,255,255,0.45)", marginBottom: 16 }}>
        Auto-refreshes every 5s
      </p>

      <Row gutter={[16, 16]} style={{ marginBottom: 24 }}>
        <Col xs={12} lg={6}>
          <Card>
            <Statistic title="Tokens In" value={tokensIn?.value ?? 0} />
          </Card>
        </Col>
        <Col xs={12} lg={6}>
          <Card>
            <Statistic title="Tokens Out" value={tokensOut?.value ?? 0} />
          </Card>
        </Col>
        <Col xs={12} lg={6}>
          <Card>
            <Statistic
              title="Avg Latency"
              value={latency?.value ?? 0}
              precision={3}
              suffix="s"
            />
          </Card>
        </Col>
        <Col xs={12} lg={6}>
          <Card>
            <Statistic
              title="Context Usage"
              value={(contextUsage?.value ?? 0) * 100}
              precision={0}
              suffix="%"
            />
          </Card>
        </Col>
        <Col xs={12} lg={6}>
          <Card>
            <Statistic title="Memory Writes" value={memWrites?.value ?? 0} />
          </Card>
        </Col>
      </Row>

      {latency && latency.buckets && latency.buckets.length > 0 && (
        <Card title="Latency Histogram" style={{ marginBottom: 24 }}>
          <Table
            size="small"
            pagination={false}
            dataSource={latency.buckets}
            rowKey="le"
            columns={[
              {
                title: "Bucket (le)",
                dataIndex: "le",
                render: (v: number) =>
                  v === Infinity || v > 1e15 ? "+Inf" : `${v}s`,
              },
              {
                title: "Cumulative Count",
                dataIndex: "count",
                align: "right" as const,
              },
            ]}
          />
          <div style={{ marginTop: 8, color: "rgba(255,255,255,0.45)" }}>
            Total: {latency.count} requests, Sum: {latency.sum?.toFixed(3)}s
          </div>
        </Card>
      )}

      <Row gutter={[16, 16]}>
        {toolCalls.length > 0 && (
          <Col xs={24} lg={12}>
            <Card title="Tool Calls">
              <Table
                size="small"
                pagination={false}
                dataSource={toolCalls}
                columns={toolColumns}
                rowKey={(r) =>
                  `${r.labels?.tool}-${r.labels?.status}`
                }
              />
            </Card>
          </Col>
        )}
        {events.length > 0 && (
          <Col xs={24} lg={12}>
            <Card title="Events">
              <Table
                size="small"
                pagination={false}
                dataSource={events}
                columns={eventColumns}
                rowKey={(r) =>
                  `${r.labels?.source}-${r.labels?.type}`
                }
              />
            </Card>
          </Col>
        )}
      </Row>
    </div>
  );
}
