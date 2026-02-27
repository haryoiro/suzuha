import { useState } from "react";
import {
  Table,
  Input,
  Select,
  Button,
  Space,
  Tag,
  Modal,
  Form,
  message,
  Popconfirm,
  Statistic,
  Progress,
  Segmented,
  Card,
  List,
  Slider,
  Spin,
  Empty,
} from "antd";
import {
  PlusOutlined,
  SearchOutlined,
  DeleteOutlined,
  EyeOutlined,
  CopyOutlined,
} from "@ant-design/icons";
import { useQuery } from "@tanstack/react-query";
import { useMemories, useCreateMemory, useDeleteMemory, useDuplicates } from "../../hooks/useMemories";
import { memoriesApi } from "../../lib/api";
import type { Memory } from "../../lib/api";
import type { ColumnsType } from "antd/es/table";
import { formatJST } from "../../lib/date";

const { TextArea } = Input;

const typeColors: Record<string, string> = {
  user: "blue",
  world: "green",
  tool: "orange",
  episode: "purple",
  self: "cyan",
};

interface Props {
  onViewDetail: (id: string) => void;
}

export function MemoriesPage({ onViewDetail }: Props) {
  const [view, setView] = useState<"list" | "duplicates">("list");
  const [offset, setOffset] = useState(0);
  const [limit] = useState(20);
  const [typeFilter, setTypeFilter] = useState<string>("");
  const [query, setQuery] = useState("");
  const [searchInput, setSearchInput] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const [form] = Form.useForm();

  const { data: stats } = useQuery({
    queryKey: ["vec-stats"],
    queryFn: memoriesApi.vecStats,
    refetchInterval: 10000,
  });

  const { data, isLoading } = useMemories({
    offset,
    limit,
    type: typeFilter || undefined,
    q: query || undefined,
    order: "updated_at",
    dir: "desc",
  });

  const createMutation = useCreateMemory();
  const deleteMutation = useDeleteMemory();

  const columns: ColumnsType<Memory> = [
    {
      title: "Type",
      dataIndex: "type",
      width: 80,
      render: (t: string) => <Tag color={typeColors[t]}>{t}</Tag>,
    },
    {
      title: "Content",
      dataIndex: "content",
      ellipsis: true,
    },
    {
      title: "Updated",
      dataIndex: "updated_at",
      width: 180,
      responsive: ["md"],
      render: (v: string) => formatJST(v),
    },
    {
      title: "Actions",
      width: 120,
      render: (_: unknown, record: Memory) => (
        <Space>
          <Button
            type="text"
            size="small"
            icon={<EyeOutlined />}
            onClick={() => onViewDetail(record.id)}
          />
          <Popconfirm
            title="Delete this memory?"
            onConfirm={() => {
              deleteMutation.mutate(record.id, {
                onSuccess: () => message.success("Deleted"),
              });
            }}
          >
            <Button type="text" size="small" danger icon={<DeleteOutlined />} />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  const handleCreate = async () => {
    try {
      const values = await form.validateFields();
      await createMutation.mutateAsync(values);
      message.success("Created");
      setCreateOpen(false);
      form.resetFields();
    } catch {
      // validation error
    }
  };

  const coveragePct = stats?.coverage_pct ?? 0;

  return (
    <div>
      {stats && (
        <Space size="large" style={{ marginBottom: 16 }}>
          <Statistic title="Total" value={stats.total_memories} />
          <Statistic
            title="Embedded"
            value={stats.embedded_count}
            valueStyle={{ color: "#52c41a" }}
          />
          <Statistic
            title="Missing"
            value={stats.missing_count}
            valueStyle={{
              color: stats.missing_count > 0 ? "#faad14" : "inherit",
            }}
          />
          <div style={{ width: 100 }}>
            <div style={{ fontSize: 12, color: "rgba(255,255,255,0.45)", marginBottom: 4 }}>
              Coverage
            </div>
            <Progress
              percent={Math.round(coveragePct)}
              size="small"
              strokeColor={
                coveragePct >= 90
                  ? "#52c41a"
                  : coveragePct >= 50
                    ? "#faad14"
                    : "#f5222d"
              }
            />
          </div>
        </Space>
      )}

      <Segmented
        value={view}
        onChange={(v) => setView(v as "list" | "duplicates")}
        options={[
          { label: "List", value: "list" },
          { label: "Duplicates", value: "duplicates", icon: <CopyOutlined /> },
        ]}
        style={{ marginBottom: 16 }}
      />

      {view === "list" ? (
        <>
          <div
            style={{
              display: "flex",
              justifyContent: "space-between",
              marginBottom: 16,
              flexWrap: "wrap",
              gap: 8,
            }}
          >
            <Space wrap>
              <Input
                placeholder="Search..."
                prefix={<SearchOutlined />}
                value={searchInput}
                onChange={(e) => setSearchInput(e.target.value)}
                onPressEnter={() => {
                  setQuery(searchInput);
                  setOffset(0);
                }}
                style={{ width: 200 }}
                allowClear
              />
              <Select
                placeholder="All types"
                allowClear
                style={{ width: 120 }}
                value={typeFilter || undefined}
                onChange={(v) => {
                  setTypeFilter(v ?? "");
                  setOffset(0);
                }}
                options={[
                  { label: "user", value: "user" },
                  { label: "world", value: "world" },
                  { label: "tool", value: "tool" },
                  { label: "episode", value: "episode" },
                  { label: "self", value: "self" },
                ]}
              />
            </Space>
            <Button
              type="primary"
              icon={<PlusOutlined />}
              onClick={() => setCreateOpen(true)}
            >
              New Memory
            </Button>
          </div>

          <Table
            rowKey="id"
            columns={columns}
            dataSource={data?.data}
            loading={isLoading}
            scroll={{ x: 500 }}
            pagination={{
              current: Math.floor(offset / limit) + 1,
              pageSize: limit,
              total: data?.total ?? 0,
              showTotal: (total) => `${total} items`,
              onChange: (page) => setOffset((page - 1) * limit),
            }}
            size="small"
          />
        </>
      ) : (
        <DuplicatesView onViewDetail={onViewDetail} />
      )}

      <Modal
        title="New Memory"
        open={createOpen}
        onOk={handleCreate}
        onCancel={() => setCreateOpen(false)}
        confirmLoading={createMutation.isPending}
      >
        <Form form={form} layout="vertical" initialValues={{ type: "user" }}>
          <Form.Item
            name="type"
            label="Type"
            rules={[{ required: true }]}
          >
            <Select
              options={[
                { label: "user", value: "user" },
                { label: "world", value: "world" },
                { label: "tool", value: "tool" },
              ]}
            />
          </Form.Item>
          <Form.Item
            name="content"
            label="Content"
            rules={[{ required: true }]}
          >
            <TextArea rows={4} />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
}

function DuplicatesView({ onViewDetail }: { onViewDetail: (id: string) => void }) {
  const [threshold, setThreshold] = useState(0.2);
  const { data, isLoading, refetch } = useDuplicates(threshold);
  const deleteMutation = useDeleteMemory();

  if (isLoading) {
    return <Spin tip="Scanning for duplicates..." style={{ display: "block", marginTop: 48 }} />;
  }

  const groups = data?.data ?? [];

  return (
    <div>
      <div style={{ display: "flex", alignItems: "center", gap: 16, marginBottom: 16 }}>
        <span style={{ whiteSpace: "nowrap", fontSize: 13, color: "rgba(255,255,255,0.65)" }}>
          Distance threshold:
        </span>
        <Slider
          min={0.05}
          max={0.5}
          step={0.05}
          value={threshold}
          onChange={setThreshold}
          style={{ width: 200 }}
          tooltip={{ formatter: (v) => `${v}` }}
        />
        <Button size="small" onClick={() => refetch()}>Refresh</Button>
      </div>

      {groups.length === 0 ? (
        <Empty description="No duplicate groups found" />
      ) : (
        <div style={{ fontSize: 13, color: "rgba(255,255,255,0.45)", marginBottom: 12 }}>
          {groups.length} duplicate group{groups.length > 1 ? "s" : ""} found
        </div>
      )}

      {groups.map((group, gi) => (
        <Card
          key={gi}
          size="small"
          title={
            <Space>
              <Tag color={typeColors[group.memories[0]?.type]}>{group.memories[0]?.type}</Tag>
              <span>{group.memories.length} similar memories</span>
            </Space>
          }
          style={{ marginBottom: 12 }}
        >
          <List
            size="small"
            dataSource={group.memories}
            renderItem={(mem, mi) => (
              <List.Item
                actions={[
                  <Button
                    key="view"
                    type="text"
                    size="small"
                    icon={<EyeOutlined />}
                    onClick={() => onViewDetail(mem.id)}
                  />,
                  mi > 0 ? (
                    <Popconfirm
                      key="del"
                      title="Delete this duplicate?"
                      onConfirm={() => {
                        deleteMutation.mutate(mem.id, {
                          onSuccess: () => {
                            message.success("Deleted");
                            refetch();
                          },
                        });
                      }}
                    >
                      <Button type="text" size="small" danger icon={<DeleteOutlined />} />
                    </Popconfirm>
                  ) : (
                    <Tag key="keep" color="green">keep</Tag>
                  ),
                ]}
              >
                <List.Item.Meta
                  title={
                    <span style={{ fontSize: 12, color: "rgba(255,255,255,0.45)" }}>
                      {mem.id.slice(0, 8)}... | {formatJST(mem.updated_at)}
                    </span>
                  }
                  description={
                    <span style={{ color: "rgba(255,255,255,0.85)" }}>
                      {mem.content}
                    </span>
                  }
                />
              </List.Item>
            )}
          />
        </Card>
      ))}
    </div>
  );
}
