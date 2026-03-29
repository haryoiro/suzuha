import { Button, Card, Spin, Progress, message } from "antd";
import { useState, memo } from "react";
import { useQuery } from "@tanstack/react-query";
import type { BoredomStatus } from "../lib/api";
import { agentApi, boredomApi } from "../lib/api";
import { formatRelative } from "../lib/date";

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

export const DashboardPage = memo(function DashboardPage() {
  const [compacting, setCompacting] = useState(false);

  const { data: boredom, isLoading } = useQuery<BoredomStatus>({
    queryKey: ["boredom"],
    queryFn: boredomApi.get,
    refetchInterval: 30000,
  });

  const handleCompact = async () => {
    setCompacting(true);
    try {
      const res = await agentApi.compact();
      message.success(`Compact done (${res.message_count} messages remaining)`);
    } catch {
      message.error("Compact failed");
    } finally {
      setCompacting(false);
    }
  };

  if (isLoading) {
    return (
      <div style={{ textAlign: "center", padding: 48 }}>
        <Spin size="large" />
      </div>
    );
  }

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

      <Card
        style={{ marginBottom: 16 }}
        styles={{ body: { display: "flex", alignItems: "center", gap: 16 } }}
      >
        <Button
          type="link"
          href={`http://${window.location.hostname}:3000`}
          target="_blank"
          rel="noopener noreferrer"
          style={{ padding: 0, fontSize: 14 }}
        >
          Langfuse Dashboard
        </Button>
        <Button
          type="link"
          href={`https://${window.location.hostname}:5174`}
          target="_blank"
          rel="noopener noreferrer"
          style={{ padding: 0, fontSize: 14 }}
        >
          Voice Widget
        </Button>
        <Button
          loading={compacting}
          onClick={handleCompact}
          size="small"
        >
          Compact Context
        </Button>
      </Card>
    </div>
  );
});

export default DashboardPage;
