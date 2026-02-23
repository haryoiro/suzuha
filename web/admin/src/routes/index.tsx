import { Button, Card, Col, Row, Statistic, Spin, message } from "antd";
import { useState } from "react";
import { useMetrics } from "../hooks/useMetrics";
import type { MetricItem } from "../lib/api";
import { agentApi } from "../lib/api";

function findMetric(
  metrics: MetricItem[] | undefined,
  name: string
): MetricItem | undefined {
  return metrics?.find((m) => m.name === name);
}

export function DashboardPage() {
  const { data, isLoading, refetch } = useMetrics();
  const metrics = data?.metrics;
  const [compacting, setCompacting] = useState(false);

  const handleCompact = async () => {
    setCompacting(true);
    try {
      const res = await agentApi.compact();
      message.success(`Compact done (${res.message_count} messages remaining)`);
      refetch();
    } catch {
      message.error("Compact failed");
    } finally {
      setCompacting(false);
    }
  };

  const tokensIn = findMetric(metrics, "suzuha_llm_tokens_input_total");
  const tokensOut = findMetric(metrics, "suzuha_llm_tokens_output_total");
  const latency = findMetric(metrics, "suzuha_llm_latency_seconds");
  const contextUsage = findMetric(
    metrics,
    "suzuha_context_window_usage_ratio"
  );
  const memWrites = findMetric(metrics, "suzuha_memory_writes_total");

  if (isLoading) {
    return (
      <div style={{ textAlign: "center", padding: 48 }}>
        <Spin size="large" />
      </div>
    );
  }

  return (
    <div>
      <h2 style={{ marginBottom: 24 }}>Dashboard</h2>
      <Row gutter={[16, 16]}>
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
            <Button
              size="small"
              loading={compacting}
              onClick={handleCompact}
              style={{ marginTop: 8 }}
            >
              Compact
            </Button>
          </Card>
        </Col>
        <Col xs={12} lg={6}>
          <Card>
            <Statistic
              title="Memory Writes"
              value={memWrites?.value ?? 0}
            />
          </Card>
        </Col>
      </Row>
    </div>
  );
}
