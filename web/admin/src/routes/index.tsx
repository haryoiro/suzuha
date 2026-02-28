import { Button, Card, Col, Row, Statistic, Spin, Progress, message } from "antd";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useMetrics } from "../hooks/useMetrics";
import type { MetricItem, BoredomStatus } from "../lib/api";
import { agentApi, boredomApi } from "../lib/api";
import { formatRelative } from "../lib/date";

function findMetric(
  metrics: MetricItem[] | undefined,
  name: string
): MetricItem | undefined {
  return metrics?.find((m) => m.name === name);
}

/** Compute average latency from a histogram metric's sum/count. */
function avgFromHistogram(m: MetricItem | undefined): number {
  if (!m || !m.sum || !m.count || m.count === 0) return 0;
  return m.sum / m.count;
}

function boredomLabel(value: number): string {
  if (value >= 80) return "かなり暇";
  if (value >= 50) return "そこそこ暇";
  if (value >= 30) return "ちょっと暇";
  if (value >= 20) return "少し暇";
  return "暇じゃない";
}

function boredomColor(value: number): string {
  if (value >= 80) return "#f5222d";
  if (value >= 50) return "#faad14";
  if (value >= 20) return "#06b6d4";
  return "#52c41a";
}

export function DashboardPage() {
  const { data, isLoading, refetch } = useMetrics();
  const metrics = data?.metrics;
  const [compacting, setCompacting] = useState(false);

  const { data: boredom } = useQuery<BoredomStatus>({
    queryKey: ["boredom"],
    queryFn: boredomApi.get,
    refetchInterval: 30000,
  });

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

  const avgLatency = avgFromHistogram(latency);
  const boredomVal = boredom?.boredom ?? 0;

  return (
    <div>
      <h2 style={{ marginBottom: 24 }}>Dashboard</h2>

      {boredom && (
        <Card
          style={{ marginBottom: 16 }}
          styles={{ body: { padding: "16px 24px" } }}
        >
          <div style={{ display: "flex", alignItems: "center", gap: 24, flexWrap: "wrap" }}>
            <div style={{ flex: "0 0 auto" }}>
              <div style={{ fontSize: 13, color: "rgba(255,255,255,0.45)", marginBottom: 4 }}>
                退屈度
              </div>
              <Progress
                type="dashboard"
                percent={Math.round(boredomVal)}
                size={80}
                strokeColor={boredomColor(boredomVal)}
                format={(pct) => <span style={{ fontSize: 18, fontWeight: 600, color: boredomColor(boredomVal) }}>{pct}</span>}
              />
            </div>
            <div style={{ flex: 1, minWidth: 150 }}>
              <div style={{ fontSize: 15, fontWeight: 500, marginBottom: 8, color: boredomColor(boredomVal) }}>
                {boredomLabel(boredomVal)}
              </div>
              <div style={{ fontSize: 13, color: "rgba(255,255,255,0.55)", lineHeight: 1.8 }}>
                <div>
                  最終やり取り: {boredom.last_interaction ? formatRelative(boredom.last_interaction) : "—"}
                  {boredom.last_channel && (
                    <span style={{ color: "rgba(255,255,255,0.35)", marginLeft: 8 }}>
                      ({boredom.last_channel})
                    </span>
                  )}
                </div>
                <div>
                  最終独り言: {boredom.last_posted_at ? formatRelative(boredom.last_posted_at) : "—"}
                </div>
                <div>
                  投稿閾値: {boredom.post_threshold}
                  {boredomVal >= boredom.post_threshold
                    ? <span style={{ color: "#52c41a", marginLeft: 8 }}>投稿可能</span>
                    : <span style={{ color: "rgba(255,255,255,0.3)", marginLeft: 8 }}>閾値未満</span>
                  }
                </div>
              </div>
            </div>
          </div>
        </Card>
      )}

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
              value={avgLatency}
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
