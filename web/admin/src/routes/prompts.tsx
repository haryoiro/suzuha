import { useState, useEffect } from "react";
import { Typography, Card, Tabs, Input, Button, Space, App, Spin, Tag } from "antd";
import { SaveOutlined, ReloadOutlined } from "@ant-design/icons";
import { usePrompts, useUpdatePrompt } from "../hooks/usePrompts";

const { Title } = Typography;
const { TextArea } = Input;

export function PromptsPage() {
  const { data: files, isLoading } = usePrompts();
  const updateMutation = useUpdatePrompt();
  const { message } = App.useApp();

  // Local editing state keyed by file name.
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [dirty, setDirty] = useState<Record<string, boolean>>({});

  // Sync fetched data into drafts (only for files not yet edited).
  useEffect(() => {
    if (!files) return;
    setDrafts((prev) => {
      const next = { ...prev };
      for (const f of files) {
        if (!(f.name in next)) {
          next[f.name] = f.content;
        }
      }
      return next;
    });
  }, [files]);

  const handleChange = (name: string, value: string) => {
    setDrafts((prev) => ({ ...prev, [name]: value }));
    const original = files?.find((f) => f.name === name)?.content ?? "";
    setDirty((prev) => ({ ...prev, [name]: value !== original }));
  };

  const handleSave = async (name: string) => {
    try {
      const result = await updateMutation.mutateAsync({
        name,
        content: drafts[name] ?? "",
      });
      setDirty((prev) => ({ ...prev, [name]: false }));
      if (result.reloaded) {
        message.success(`${name} saved & agent reloaded`);
      } else {
        message.warning(`${name} saved (agent reload failed)`);
      }
    } catch {
      message.error(`Failed to save ${name}`);
    }
  };

  const handleReset = (name: string) => {
    const original = files?.find((f) => f.name === name)?.content ?? "";
    setDrafts((prev) => ({ ...prev, [name]: original }));
    setDirty((prev) => ({ ...prev, [name]: false }));
  };

  if (isLoading) {
    return <Spin style={{ display: "block", margin: "80px auto" }} />;
  }

  const tabItems = (files ?? []).map((f) => ({
    key: f.name,
    label: (
      <span>
        {f.name} {dirty[f.name] && <Tag color="orange" style={{ marginLeft: 4 }}>unsaved</Tag>}
      </span>
    ),
    children: (
      <div>
        <div style={{ marginBottom: 12, display: "flex", justifyContent: "space-between", alignItems: "center", flexWrap: "wrap", gap: 8 }}>
          <span style={{ color: "rgba(255,255,255,0.45)", fontSize: 12 }}>
            {f.updated_at ? `Last updated: ${new Date(f.updated_at).toLocaleString()}` : "New file"}
          </span>
          <Space>
            <Button
              icon={<ReloadOutlined />}
              disabled={!dirty[f.name]}
              onClick={() => handleReset(f.name)}
            >
              Reset
            </Button>
            <Button
              type="primary"
              icon={<SaveOutlined />}
              disabled={!dirty[f.name]}
              loading={updateMutation.isPending}
              onClick={() => handleSave(f.name)}
            >
              Save
            </Button>
          </Space>
        </div>
        <TextArea
          value={drafts[f.name] ?? ""}
          onChange={(e) => handleChange(f.name, e.target.value)}
          autoSize={{ minRows: 16, maxRows: 40 }}
          style={{ fontFamily: "monospace", fontSize: 13 }}
        />
      </div>
    ),
  }));

  return (
    <div>
      <Title level={3}>Prompts</Title>
      <Card>
        <Tabs items={tabItems} />
      </Card>
    </div>
  );
}
