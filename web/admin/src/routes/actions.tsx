import { useState, memo } from "react";
import { Table, Tag, Typography, Space, Button, Modal, Form, Input, message, Popconfirm, Select, DatePicker } from "antd";
import { PlusOutlined, DeleteOutlined, EditOutlined } from "@ant-design/icons";
import { useQueryClient } from "@tanstack/react-query";
import type { ColumnsType } from "antd/es/table";
import { useScheduledActions, useAllChannels } from "../hooks/useUsers";
import { dayjs, formatJST, toJST } from "../lib/date";
import { actionsApi, type ScheduledAction, type ChannelEntry } from "../lib/api";

const { Title, Text } = Typography;
const { TextArea } = Input;

/** Build channel select options grouped by guild. */
function buildChannelOptions(channels: ChannelEntry[]) {
  const groups = new Map<string, { label: string; options: { label: string; value: string }[] }>();
  for (const ch of channels) {
    const key = ch.guild_id;
    let group = groups.get(key);
    if (!group) {
      group = { label: ch.guild_name || ch.guild_id, options: [] };
      groups.set(key, group);
    }
    group.options.push({
      label: `#${ch.channel_name || ch.channel_id}`,
      value: ch.channel_id,
    });
  }
  return Array.from(groups.values());
}

/** Resolve channel_id to "Server > #channel" display string. */
function resolveChannelLabel(channelId: string, channels: ChannelEntry[]): string {
  const ch = channels.find((c) => c.channel_id === channelId);
  if (ch) return `${ch.guild_name} > #${ch.channel_name}`;
  return channelId;
}

const ActionFormModal = memo(function ActionFormModal({
  action,
  open,
  onClose,
  channels,
}: {
  action: ScheduledAction | null;
  open: boolean;
  onClose: () => void;
  channels: ChannelEntry[];
}) {
  const [form] = Form.useForm();
  const queryClient = useQueryClient();
  const isEdit = !!action;

  const handleSubmit = async (values: Record<string, unknown>) => {
    const scheduledAt = values.scheduled_at as dayjs.Dayjs | undefined;
    const body = {
      channel_id: values.channel_id as string,
      content: values.content as string,
      mode: (values.mode as string) || "direct",
      scheduled_at: scheduledAt ? scheduledAt.toISOString() : undefined,
      cron_expr: (values.cron_expr as string) || undefined,
    };

    try {
      if (isEdit && action) {
        await actionsApi.update(action.id, body);
        message.success("Updated");
      } else {
        await actionsApi.create(body);
        message.success("Created");
      }
      queryClient.invalidateQueries({ queryKey: ["scheduled-actions"] });
      onClose();
    } catch {
      message.error("Failed");
    }
  };

  const channelOptions = buildChannelOptions(channels);

  return (
    <Modal
      title={isEdit ? "Edit Action" : "New Scheduled Action"}
      open={open}
      onCancel={onClose}
      footer={null}
      destroyOnClose
    >
      <Form
        form={form}
        layout="vertical"
        initialValues={
          action
            ? {
                channel_id: action.channel_id,
                content: action.content,
                mode: action.mode || "direct",
                scheduled_at: toJST(action.scheduled_at),
                cron_expr: action.cron_expr ?? "",
              }
            : { mode: "direct", cron_expr: "" }
        }
        onFinish={handleSubmit}
      >
        <Form.Item label="Channel" name="channel_id" rules={[{ required: true }]}>
          <Select
            showSearch
            placeholder="Select channel"
            options={channelOptions}
            optionFilterProp="label"
          />
        </Form.Item>
        <Form.Item label="Mode" name="mode" rules={[{ required: true }]}>
          <Select options={[
            { value: "direct", label: "Direct（そのまま投稿）" },
            { value: "prompt", label: "Prompt（LLM で生成して投稿）" },
          ]} />
        </Form.Item>
        <Form.Item label="Content" name="content" rules={[{ required: true }]}>
          <TextArea rows={3} placeholder="Message content" />
        </Form.Item>
        <Form.Item label="Scheduled At" name="scheduled_at" extra="cron_expr のみの場合は省略可（次回実行時刻を自動計算）">
          <DatePicker showTime format="YYYY-MM-DD HH:mm" style={{ width: "100%" }} />
        </Form.Item>
        <Form.Item label="Cron Expression" name="cron_expr">
          <Input placeholder="e.g. 0 9 * * * (optional)" />
        </Form.Item>
        <Space>
          <Button type="primary" htmlType="submit">{isEdit ? "Update" : "Create"}</Button>
          <Button onClick={onClose}>Cancel</Button>
        </Space>
      </Form>
    </Modal>
  );
});

export const ActionsPage = memo(function ActionsPage() {
  const [statusFilter, setStatusFilter] = useState<string>("");
  const [editingAction, setEditingAction] = useState<ScheduledAction | null>(null);
  const [creating, setCreating] = useState(false);

  const queryClient = useQueryClient();
  const { data, isLoading } = useScheduledActions(statusFilter || undefined);
  const { data: channelsData } = useAllChannels();
  const actions = data?.data ?? [];
  const channels = channelsData?.data ?? [];

  const handleDelete = async (id: string) => {
    try {
      await actionsApi.delete(id);
      message.success("Deleted");
      queryClient.invalidateQueries({ queryKey: ["scheduled-actions"] });
    } catch {
      message.error("Delete failed");
    }
  };

  const columns: ColumnsType<ScheduledAction> = [
    {
      title: "Content",
      dataIndex: "content",
      key: "content",
      ellipsis: true,
      render: (text: string) => <Text style={{ maxWidth: 300 }}>{text}</Text>,
    },
    {
      title: "Mode",
      dataIndex: "mode",
      key: "mode",
      width: 90,
      render: (v: string) => <Tag color={v === "prompt" ? "purple" : "default"}>{v}</Tag>,
    },
    {
      title: "Channel",
      dataIndex: "channel_id",
      key: "channel_id",
      width: 220,
      responsive: ["md"],
      render: (v: string) => <Text>{resolveChannelLabel(v, channels)}</Text>,
    },
    {
      title: "Scheduled",
      dataIndex: "scheduled_at",
      key: "scheduled_at",
      width: 180,
      render: (v: string) => formatJST(v),
    },
    {
      title: "Cron",
      dataIndex: "cron_expr",
      key: "cron_expr",
      width: 120,
      responsive: ["lg"],
      render: (v?: string) => v ? <Text code>{v}</Text> : "-",
    },
    {
      title: "Status",
      dataIndex: "status",
      key: "status",
      width: 100,
      render: (status: string) => {
        const color = status === "pending" ? "blue" : status === "done" ? "green" : "default";
        return <Tag color={color}>{status}</Tag>;
      },
    },
    {
      title: "",
      key: "actions",
      width: 80,
      render: (_, record) => (
        <Space>
          <Button size="small" icon={<EditOutlined />} onClick={() => setEditingAction(record)} />
          <Popconfirm title="Delete?" onConfirm={() => handleDelete(record.id)}>
            <Button size="small" danger icon={<DeleteOutlined />} />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <Space style={{ marginBottom: 16, width: "100%", justifyContent: "space-between", flexWrap: "wrap" }}>
        <Title level={3} style={{ margin: 0 }}>Scheduled Actions</Title>
        <Space>
          <Select
            value={statusFilter}
            onChange={setStatusFilter}
            style={{ width: 120 }}
            options={[
              { value: "", label: "All" },
              { value: "pending", label: "Pending" },
              { value: "done", label: "Done" },
            ]}
          />
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreating(true)}>
            New
          </Button>
        </Space>
      </Space>

      <Table<ScheduledAction>
        columns={columns}
        dataSource={actions}
        rowKey="id"
        loading={isLoading}
        scroll={{ x: 500 }}
        pagination={false}
      />

      <ActionFormModal
        action={editingAction}
        open={!!editingAction || creating}
        onClose={() => { setEditingAction(null); setCreating(false); }}
        channels={channels}
      />
    </div>
  );
});

export default ActionsPage;
