import { useState, memo, useMemo } from "react";
import { Table, Typography, Input, Modal, Tag, Descriptions, Card, Select, Switch, Button, Flex, message } from "antd";
import { PlayCircleOutlined, ReloadOutlined } from "@ant-design/icons";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import type { ColumnsType } from "antd/es/table";
import { useTools } from "../hooks/useTools";
import type { ToolInfo, LLMRoleAssignment } from "../lib/api";
import { llmApi, toolsApi } from "../lib/api";

const { Title, Text } = Typography;

// --- LLM Role Manager (3-layer) ---

const ROLES = ["conversation", "background", "vision"] as const;

const LLMProviderSection = memo(function LLMProviderSection() {
  const qc = useQueryClient();
  const invalidateAll = () => {
    qc.invalidateQueries({ queryKey: ["llm-status"] });
    qc.invalidateQueries({ queryKey: ["llm-roles"] });
    qc.invalidateQueries({ queryKey: ["llm-models"] });
    qc.invalidateQueries({ queryKey: ["llm-providers"] });
  };

  const { data: status } = useQuery({ queryKey: ["llm-status"], queryFn: () => llmApi.status() });
  const { data: providers } = useQuery({ queryKey: ["llm-providers"], queryFn: () => llmApi.providers() });
  const { data: roles } = useQuery({ queryKey: ["llm-roles"], queryFn: () => llmApi.roles() });

  const [selectedProvider, setSelectedProvider] = useState<string>("");
  const { data: models } = useQuery({
    queryKey: ["llm-models", selectedProvider],
    queryFn: () => llmApi.models(selectedProvider || undefined),
  });

  const assignMutation = useMutation({
    mutationFn: ({ role, provider, model }: { role: string; provider: string; model: string }) =>
      llmApi.assignRole(role, provider, model),
    onSuccess: () => { invalidateAll(); message.success("ロールを切り替えました"); },
    onError: () => message.error("切り替えに失敗"),
  });

  const refreshMutation = useMutation({
    mutationFn: () => llmApi.refreshModels(),
    onSuccess: (r) => { invalidateAll(); message.success(`${r.models_updated} モデルを更新`); },
    onError: () => message.error("モデル更新に失敗"),
  });

  const roleAssignments = (roles ?? []) as LLMRoleAssignment[];
  const providerList = providers ?? [];
  const modelList = models ?? [];

  const findAssignment = (role: string) => roleAssignments.find((a) => a.role === role);

  const handleRoleChange = (role: string, value: string) => {
    const [prov, ...modelParts] = value.split("/");
    const model = modelParts.join("/");
    assignMutation.mutate({ role, provider: prov, model });
  };

  // Build model options grouped by provider
  const modelOptions = useMemo(() => {
    const grouped: Record<string, { value: string; label: string }[]> = {};
    for (const m of modelList) {
      if (!grouped[m.provider_name]) grouped[m.provider_name] = [];
      const caps = m.capabilities.includes("vision") ? " [vision]" : "";
      grouped[m.provider_name].push({
        value: `${m.provider_name}/${m.model_id}`,
        label: `${m.model_id}${caps}`,
      });
    }
    return Object.entries(grouped).map(([provider, options]) => ({
      label: provider,
      options,
    }));
  }, [modelList]);

  return (
    <Card size="small" style={{ marginBottom: 16 }}>
      <Flex vertical gap={12}>
        {status && (
          <Text type="secondary" style={{ fontSize: 12 }}>
            conversation: {status.provider}/{status.model}
            {status.vision && " [vision]"} ctx={status.max_ctx}
          </Text>
        )}

        {ROLES.map((role) => {
          const assignment = findAssignment(role);
          const currentValue = assignment ? `${assignment.provider_name}/${assignment.model_id}` : undefined;
          return (
            <Flex key={role} align="center" gap={8}>
              <Text strong style={{ width: 110 }}>{role}:</Text>
              <Select
                value={currentValue}
                onChange={(v) => handleRoleChange(role, v)}
                loading={assignMutation.isPending}
                style={{ width: 360 }}
                placeholder="provider/model"
                showSearch
                optionFilterProp="label"
                options={modelOptions}
              />
            </Flex>
          );
        })}

        <Flex align="center" gap={8}>
          <Text strong style={{ width: 110 }}>Filter:</Text>
          <Select
            value={selectedProvider || undefined}
            onChange={(v) => setSelectedProvider(v ?? "")}
            allowClear
            style={{ width: 160 }}
            placeholder="All providers"
            options={providerList.map((p) => ({ value: p.name, label: `${p.name} (${p.type})` }))}
          />
          <Button
            icon={<ReloadOutlined />}
            size="small"
            loading={refreshMutation.isPending}
            onClick={() => refreshMutation.mutate()}
          >
            Refresh Models
          </Button>
        </Flex>
      </Flex>
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
  const [execInput, setExecInput] = useState("{}");
  const [execOutput, setExecOutput] = useState<string | null>(null);

  const toggleMutation = useMutation({
    mutationFn: ({ name, enabled }: { name: string; enabled: boolean }) =>
      toolsApi.toggle(name, enabled),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["tools"] }),
    onError: () => message.error("Toggle failed"),
  });

  const execMutation = useMutation({
    mutationFn: ({ name, input }: { name: string; input: Record<string, unknown> }) =>
      toolsApi.execute(name, input),
    onSuccess: (result) => {
      setExecOutput(result.output);
      if (result.is_error) {
        message.warning("Tool returned error");
      } else {
        message.success("Executed");
      }
    },
    onError: () => message.error("Execution failed"),
  });

  const handleExecute = () => {
    if (!selected) return;
    try {
      const parsed = JSON.parse(execInput);
      setExecOutput(null);
      execMutation.mutate({ name: selected.name, input: parsed });
    } catch {
      message.error("Invalid JSON");
    }
  };

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
        onCancel={() => { setSelected(null); setExecOutput(null); setExecInput("{}"); }}
        footer={null}
        width={640}
      >
        {selected && (
          <>
            <p>{selected.description}</p>
            <Title level={5} style={{ marginTop: 16 }}>Parameters</Title>
            <SchemaView schema={selected.input_schema} />
            <Title level={5} style={{ marginTop: 16 }}>Execute</Title>
            <Input.TextArea
              rows={3}
              value={execInput}
              onChange={(e) => setExecInput(e.target.value)}
              placeholder='{"key": "value"}'
              style={{ fontFamily: "monospace", fontSize: 12, marginBottom: 8 }}
            />
            <Button
              type="primary"
              icon={<PlayCircleOutlined />}
              loading={execMutation.isPending}
              onClick={handleExecute}
            >
              Execute
            </Button>
            {execOutput != null && (
              <pre style={{
                marginTop: 12,
                padding: 12,
                background: "#1e1e1e",
                color: "#d4d4d4",
                borderRadius: 6,
                maxHeight: 300,
                overflow: "auto",
                fontSize: 12,
                whiteSpace: "pre-wrap",
                wordBreak: "break-word",
              }}>
                {execOutput}
              </pre>
            )}
          </>
        )}
      </Modal>
    </div>
  );
});

export default ToolsPage;
