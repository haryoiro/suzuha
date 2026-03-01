import { useState } from "react";
import {
  Typography,
  Card,
  List,
  Tag,
  Button,
  Space,
  Slider,
  Spin,
  Empty,
  Popconfirm,
  Statistic,
  message,
  Input,
  Modal,
} from "antd";
import { DeleteOutlined, MergeCellsOutlined, CheckOutlined, ThunderboltOutlined } from "@ant-design/icons";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { forgetApi, type ForgetGroup } from "../lib/api";
import { formatJST } from "../lib/date";

const { Text } = Typography;
const { TextArea } = Input;

const typeColors: Record<string, string> = {
  user: "blue",
  world: "green",
  tool: "orange",
  rss: "cyan",
  episode: "purple",
  self: "magenta",
};

/** Embeddable dedup view for the Memories page tab. */
export function DedupView() {
  const [threshold, setThreshold] = useState(0.25);
  const queryClient = useQueryClient();

  const { data: statusData } = useQuery({
    queryKey: ["forget-status"],
    queryFn: forgetApi.status,
  });

  const { data, isLoading, refetch } = useQuery({
    queryKey: ["forget-groups", threshold],
    queryFn: () => forgetApi.groups(threshold),
  });

  const runMutation = useMutation({
    mutationFn: forgetApi.run,
    onSuccess: (res) => {
      if (res.ok) {
        message.success("AI Dedup completed");
        queryClient.invalidateQueries({ queryKey: ["forget-groups"] });
        queryClient.invalidateQueries({ queryKey: ["forget-status"] });
        queryClient.invalidateQueries({ queryKey: ["vec-stats"] });
        refetch();
      } else {
        message.error(res.error ?? "Dedup failed");
      }
    },
    onError: () => message.error("Dedup request failed"),
  });

  const groups = data?.data ?? [];
  const status = statusData;

  return (
    <div>
      {status?.has_run && (
        <Space size="large" style={{ marginBottom: 12 }}>
          <Statistic title="Last deleted" value={status.total_deleted ?? 0} valueStyle={{ fontSize: 16 }} />
          <Statistic title="Last merged" value={status.total_merged ?? 0} valueStyle={{ fontSize: 16 }} />
          {status.last_run_at && (
            <Text type="secondary" style={{ fontSize: 12 }}>Last run: {formatJST(status.last_run_at)}</Text>
          )}
        </Space>
      )}

      <div style={{ display: "flex", alignItems: "center", gap: 16, marginBottom: 16, flexWrap: "wrap" }}>
        <Text type="secondary" style={{ whiteSpace: "nowrap", fontSize: 13 }}>
          Similarity:
        </Text>
        <Slider
          min={0.05}
          max={0.6}
          step={0.05}
          value={threshold}
          onChange={setThreshold}
          style={{ width: 200 }}
          tooltip={{ formatter: (v) => `${v}` }}
        />
        <Button size="small" onClick={() => refetch()}>Refresh</Button>
        <Text type="secondary" style={{ fontSize: 12 }}>
          {groups.length} group{groups.length !== 1 ? "s" : ""}
        </Text>
        <Popconfirm
          title="Run AI-based deduplication now?"
          description="The AI will judge each group and delete/merge automatically."
          onConfirm={() => runMutation.mutate()}
        >
          <Button
            type="primary"
            size="small"
            icon={<ThunderboltOutlined />}
            loading={runMutation.isPending}
          >
            Run AI Dedup
          </Button>
        </Popconfirm>
      </div>

      {isLoading ? (
        <Spin tip="Scanning..." style={{ display: "block", marginTop: 48 }} />
      ) : groups.length === 0 ? (
        <Empty description="No similar groups found" />
      ) : (
        groups.map((group, gi) => (
          <GroupCard key={gi} group={group} onDone={refetch} />
        ))
      )}
    </div>
  );
}

function GroupCard({ group, onDone }: { group: ForgetGroup; onDone: () => void }) {
  const [mergeOpen, setMergeOpen] = useState(false);
  const [mergeContent, setMergeContent] = useState("");
  const queryClient = useQueryClient();

  const deleteMutation = useMutation({
    mutationFn: (ids: string[]) => forgetApi.delete(ids),
    onSuccess: (res) => {
      message.success(`Deleted ${res.deleted} memories`);
      queryClient.invalidateQueries({ queryKey: ["forget-groups"] });
      onDone();
    },
    onError: () => message.error("Delete failed"),
  });

  const mergeMutation = useMutation({
    mutationFn: () =>
      forgetApi.merge(
        group.members.map((m) => m.id),
        mergeContent,
        group.type,
      ),
    onSuccess: () => {
      message.success("Merged");
      setMergeOpen(false);
      queryClient.invalidateQueries({ queryKey: ["forget-groups"] });
      onDone();
    },
    onError: () => message.error("Merge failed"),
  });

  const handleKeepNewest = () => {
    const sorted = [...group.members].sort(
      (a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime()
    );
    const deleteIds = sorted.slice(1).map((m) => m.id);
    deleteMutation.mutate(deleteIds);
  };

  const handleDeleteOne = (id: string) => {
    deleteMutation.mutate([id]);
  };

  const openMerge = () => {
    setMergeContent(group.members.map((m) => m.content).join("\n---\n"));
    setMergeOpen(true);
  };

  return (
    <>
      <Card
        size="small"
        title={
          <Space>
            <Tag color={typeColors[group.type]}>{group.type}</Tag>
            <span>{group.members.length} memories</span>
            <Text type="secondary" style={{ fontSize: 11 }}>
              avg dist: {group.avg_distance.toFixed(3)}
            </Text>
          </Space>
        }
        extra={
          <Space>
            <Popconfirm title="Keep newest, delete rest?" onConfirm={handleKeepNewest}>
              <Button size="small" icon={<CheckOutlined />}>Keep newest</Button>
            </Popconfirm>
            <Button size="small" icon={<MergeCellsOutlined />} onClick={openMerge}>Merge</Button>
          </Space>
        }
        style={{ marginBottom: 12 }}
      >
        <List
          size="small"
          dataSource={group.members}
          renderItem={(mem) => (
            <List.Item
              actions={[
                <Popconfirm key="del" title="Delete?" onConfirm={() => handleDeleteOne(mem.id)}>
                  <Button type="text" size="small" danger icon={<DeleteOutlined />} />
                </Popconfirm>,
              ]}
            >
              <List.Item.Meta
                title={
                  <Space size={8}>
                    <Text type="secondary" style={{ fontSize: 11, fontFamily: "monospace" }}>
                      {mem.id.slice(0, 8)}
                    </Text>
                    <Text type="secondary" style={{ fontSize: 11 }}>
                      {formatJST(mem.created_at)}
                    </Text>
                  </Space>
                }
                description={
                  <Text style={{ fontSize: 13, whiteSpace: "pre-wrap" }}>{mem.content}</Text>
                }
              />
            </List.Item>
          )}
        />
      </Card>

      <Modal
        title="Merge memories"
        open={mergeOpen}
        onCancel={() => setMergeOpen(false)}
        onOk={() => mergeMutation.mutate()}
        okText="Merge"
        confirmLoading={mergeMutation.isPending}
        width={600}
      >
        <Text type="secondary" style={{ display: "block", marginBottom: 8 }}>
          Edit merged content. All {group.members.length} originals will be deleted and replaced.
        </Text>
        <TextArea
          rows={8}
          value={mergeContent}
          onChange={(e) => setMergeContent(e.target.value)}
        />
      </Modal>
    </>
  );
}
