import { useState, memo, useMemo } from "react";
import { Table, Typography, Input, Modal, Tag, Descriptions, Card, Select, Switch, Space, message } from "antd";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import type { ColumnsType } from "antd/es/table";
import { useTools } from "../hooks/useTools";
import type { ToolInfo } from "../lib/api";
import { llmApi, toolsApi } from "../lib/api";

const { Title, Text } = Typography;

// --- LLM Provider Switcher ---

const PRESETS: { label: string; provider: string; model: string; api_base: string; api_key?: string; max_ctx?: number; vision?: boolean }[] = [
  { label: "Zhipu GLM-4.7", provider: "zhipu", model: "glm-4.7", api_base: "https://open.bigmodel.cn/api/paas/v4" },
  { label: "Qwen3-8B (local)", provider: "qwen", model: "Qwen/Qwen3-8B-AWQ", api_base: "http://host.docker.internal:8000/v1", api_key: "none", max_ctx: 12000 },
];

const LLMProviderSection = memo(function LLMProviderSection() {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({ queryKey: ["llm-provider"], queryFn: () => llmApi.get() });
  const mutation = useMutation({
    mutationFn: llmApi.update,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["llm-provider"] });
      message.success("Provider switched");
    },
    onError: () => message.error("Switch failed"),
  });

  const current = data ? `${data.provider}/${data.model}` : "";

  const handleSelect = (value: string) => {
    const preset = PRESETS.find((p) => `${p.provider}/${p.model}` === value);
    if (!preset) return;
    mutation.mutate({
      provider: preset.provider,
      model: preset.model,
      api_base: preset.api_base,
      api_key: preset.api_key,
      max_ctx: preset.max_ctx,
      vision: preset.vision,
    });
  };

  return (
    <Card size="small" style={{ marginBottom: 16 }}>
      <Space align="center">
        <Text strong>LLM Provider:</Text>
        <Select
          value={current || undefined}
          loading={isLoading}
          disabled={mutation.isPending}
          onChange={handleSelect}
          style={{ width: 250 }}
          placeholder="Select provider"
          options={PRESETS.map((p) => ({
            value: `${p.provider}/${p.model}`,
            label: p.label,
          }))}
        />
        {data && (
          <Text type="secondary" style={{ fontSize: 12 }}>
            {data.api_base}
          </Text>
        )}
      </Space>
    </Card>
  );
});

// --- Schema View ---

function SchemaView({ schema }: { schema: Record<string, unknown> }) {
  const properties = (schema.properties ?? {}) as Record<string, Record<string, unknown>>;
  const required = (schema.required ?? []) as string[];
  const entries = Object.entries(properties);

  if (entries.length === 0) {
    return <Text type="secondary">No parameters</Text>;
  }

  return (
    <Descriptions column={1} size="small" bordered>
      {entries.map(([name, prop]) => (
        <Descriptions.Item
          key={name}
          label={
            <>
              <Text code>{name}</Text>
              {required.includes(name) && (
                <Tag color="red" style={{ marginLeft: 4, fontSize: 10 }}>required</Tag>
              )}
              {prop.type && (
                <Tag style={{ marginLeft: 4, fontSize: 10 }}>{String(prop.type)}</Tag>
              )}
            </>
          }
        >
          {prop.description ? String(prop.description) : "-"}
        </Descriptions.Item>
      ))}
    </Descriptions>
  );
}

// --- Main Page ---

export const ToolsPage = memo(function ToolsPage() {
  const qc = useQueryClient();
  const { data, isLoading } = useTools();
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<ToolInfo | null>(null);

  const toggleMutation = useMutation({
    mutationFn: ({ name, enabled }: { name: string; enabled: boolean }) =>
      toolsApi.toggle(name, enabled),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["tools"] }),
    onError: () => message.error("Toggle failed"),
  });

  const tools = useMemo(() => {
    const all = data?.data ?? [];
    if (!search) return all;
    const q = search.toLowerCase();
    return all.filter(
      (t) => t.name.toLowerCase().includes(q) || t.description.toLowerCase().includes(q)
    );
  }, [data, search]);

  const enabledCount = useMemo(() => tools.filter((t) => t.enabled).length, [tools]);

  const columns: ColumnsType<ToolInfo> = [
    {
      title: "On",
      key: "enabled",
      width: 60,
      render: (_, record) => (
        <Switch
          size="small"
          checked={record.enabled}
          loading={toggleMutation.isPending && toggleMutation.variables?.name === record.name}
          onChange={(checked) => toggleMutation.mutate({ name: record.name, enabled: checked })}
        />
      ),
    },
    {
      title: "Name",
      dataIndex: "name",
      key: "name",
      sorter: (a, b) => a.name.localeCompare(b.name),
      render: (name: string, record) => (
        <a onClick={() => setSelected(record)} style={{ opacity: record.enabled ? 1 : 0.4 }}>{name}</a>
      ),
    },
    {
      title: "Description",
      dataIndex: "description",
      key: "description",
      ellipsis: true,
    },
    {
      title: "Params",
      key: "params",
      width: 80,
      render: (_, record) => {
        const props = record.input_schema?.properties as Record<string, unknown> | undefined;
        return props ? Object.keys(props).length : 0;
      },
    },
  ];

  return (
    <div>
      <Title level={3} style={{ marginBottom: 16 }}>
        Tools ({enabledCount}/{tools.length})
      </Title>
      <LLMProviderSection />
      <Input.Search
        placeholder="Search tools..."
        allowClear
        value={search}
        onChange={(e) => setSearch(e.target.value)}
        style={{ marginBottom: 16, maxWidth: 400 }}
      />
      <Table<ToolInfo>
        columns={columns}
        dataSource={tools}
        rowKey="name"
        loading={isLoading}
        pagination={false}
        scroll={{ x: 500 }}
        size="small"
      />
      <Modal
        title={selected?.name}
        open={!!selected}
        onCancel={() => setSelected(null)}
        footer={null}
        width={640}
      >
        {selected && (
          <>
            <p>{selected.description}</p>
            <Title level={5} style={{ marginTop: 16 }}>Parameters</Title>
            <SchemaView schema={selected.input_schema} />
          </>
        )}
      </Modal>
    </div>
  );
});

export default ToolsPage;
