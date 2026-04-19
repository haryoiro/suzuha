import { useState, useMemo, memo } from "react";
import {
  Table,
  Tag,
  Space,
  Typography,
  Select,
  Switch,
  Button,
  Card,
  Row,
  Col,
  Statistic,
  Popconfirm,
  message,
} from "antd";
import { ReloadOutlined, UndoOutlined, DeleteOutlined } from "@ant-design/icons";
import type { ColumnsType } from "antd/es/table";
import {
  useGuildList,
  useChannelSettings,
  useUpsertChannelSetting,
  useDeleteChannelSetting,
} from "../../hooks/useChannelSettings";
import { contextApi } from "../../lib/api";
import type { ChannelSetting } from "../../lib/api";
import { formatJST } from "../../lib/date";

const { Title, Text } = Typography;


export const DiscordPage = memo(function DiscordPage() {
  const [guildId, setGuildId] = useState<string | undefined>(undefined);

  const { data: guildsData } = useGuildList();
  const { data, isLoading, refetch } = useChannelSettings(guildId);
  const upsertMutation = useUpsertChannelSetting();
  const deleteMutation = useDeleteChannelSetting();

  const channels = data?.data ?? [];

  const stats = useMemo(() => {
    const active = channels.filter((c) => c.mode === "active").length;
    const listen = channels.filter((c) => c.mode === "listen").length;
    const disabled = channels.filter((c) => c.mode === "disabled").length;
    return { total: channels.length, active, listen, disabled };
  }, [channels]);

  const handleModeChange = (record: ChannelSetting, mode: string) => {
    upsertMutation.mutate(
      {
        channelId: record.channel_id,
        mode,
        home: record.home,
        guild_id: record.guild_id,
      },
      { onSuccess: () => message.success(`${record.channel_name}: ${mode}`) }
    );
  };

  const handleHomeChange = (record: ChannelSetting, checked: boolean) => {
    upsertMutation.mutate(
      {
        channelId: record.channel_id,
        mode: record.mode,
        home: checked,
        guild_id: record.guild_id,
      },
      {
        onSuccess: () =>
          message.success(
            `${record.channel_name}: home ${checked ? "ON" : "OFF"}`
          ),
      }
    );
  };

  const handleReset = (channelId: string) => {
    deleteMutation.mutate(channelId, {
      onSuccess: () => message.success("Reset to default"),
    });
  };

  const columns: ColumnsType<ChannelSetting> = [
    {
      title: "Channel",
      dataIndex: "channel_name",
      key: "channel_name",
      render: (name: string, record: ChannelSetting) => (
        <div>
          <Text strong>#{name}</Text>
          <br />
          <Text type="secondary" style={{ fontSize: 11 }}>
            {record.channel_id}
          </Text>
        </div>
      ),
    },
    ...(guildId
      ? []
      : [
          {
            title: "Guild",
            dataIndex: "guild_name",
            key: "guild_name",
            width: 150,
            render: (name: string) => <Text>{name || "-"}</Text>,
          } as ColumnsType<ChannelSetting>[number],
        ]),
    {
      title: "Mode",
      dataIndex: "mode",
      key: "mode",
      width: 140,
      filters: [
        { text: "Active", value: "active" },
        { text: "Listen", value: "listen" },
        { text: "Disabled", value: "disabled" },
      ],
      onFilter: (value, record) => record.mode === value,
      render: (_: string, record: ChannelSetting) => (
        <Select
          value={record.mode}
          size="small"
          style={{ width: 120 }}
          onChange={(val) => handleModeChange(record, val)}
          options={[
            {
              label: <Tag color="green">Active</Tag>,
              value: "active",
            },
            {
              label: <Tag color="blue">Listen</Tag>,
              value: "listen",
            },
            {
              label: <Tag color="red">Disabled</Tag>,
              value: "disabled",
            },
          ]}
        />
      ),
    },
    {
      title: "Home",
      dataIndex: "home",
      key: "home",
      width: 90,
      render: (_: boolean, record: ChannelSetting) => (
        <Switch
          size="small"
          checked={record.home}
          onChange={(checked) => handleHomeChange(record, checked)}
        />
      ),
    },
    {
      title: "Users",
      dataIndex: "user_count",
      key: "user_count",
      width: 80,
      sorter: (a: ChannelSetting, b: ChannelSetting) =>
        a.user_count - b.user_count,
    },
    {
      title: "Last Activity",
      dataIndex: "last_user_message_at",
      key: "last_user_message_at",
      width: 160,
      render: (val?: string) =>
        val ? (
          <Text type="secondary" style={{ fontSize: 12 }}>
            {formatJST(val)}
          </Text>
        ) : (
          <Text type="secondary">-</Text>
        ),
    },
    {
      title: "",
      key: "actions",
      width: 100,
      render: (_: unknown, record: ChannelSetting) => (
        <Space size={0}>
          {record.settings_updated_at ? (
            <Popconfirm
              title="Reset to default (active)?"
              onConfirm={() => handleReset(record.channel_id)}
            >
              <Button type="text" size="small" icon={<UndoOutlined />} />
            </Popconfirm>
          ) : null}
          <Popconfirm
            title="Delete this channel from DB?"
            description="Context messages, settings, and logs for this channel will be removed."
            onConfirm={async () => {
              await contextApi.deleteChannel(record.channel_id);
              message.success(`Deleted #${record.channel_name}`);
              refetch();
            }}
          >
            <Button type="text" size="small" danger icon={<DeleteOutlined />} />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div>
      <Space
        style={{
          width: "100%",
          justifyContent: "space-between",
          marginBottom: 16,
        }}
      >
        <Title level={3} style={{ margin: 0 }}>
          Discord Channels
        </Title>
        <Space>
          <Select
            placeholder="All Guilds"
            allowClear
            style={{ width: 200 }}
            value={guildId}
            onChange={(val) => setGuildId(val)}
            options={(guildsData?.data ?? []).map((g) => ({
              label: g.name || g.id,
              value: g.id,
            }))}
          />
          <Button icon={<ReloadOutlined />} onClick={() => refetch()}>
            Refresh
          </Button>
        </Space>
      </Space>

      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col span={6}>
          <Card size="small">
            <Statistic title="Total" value={stats.total} />
          </Card>
        </Col>
        <Col span={6}>
          <Card size="small">
            <Statistic
              title="Active"
              value={stats.active}
              valueStyle={{ color: "#52c41a" }}
            />
          </Card>
        </Col>
        <Col span={6}>
          <Card size="small">
            <Statistic
              title="Listen"
              value={stats.listen}
              valueStyle={{ color: "#1677ff" }}
            />
          </Card>
        </Col>
        <Col span={6}>
          <Card size="small">
            <Statistic
              title="Disabled"
              value={stats.disabled}
              valueStyle={{ color: "#ff4d4f" }}
            />
          </Card>
        </Col>
      </Row>

      <Table
        columns={columns}
        dataSource={channels}
        loading={isLoading}
        rowKey="channel_id"
        size="small"
        pagination={false}
        rowClassName={(record) =>
          record.mode === "disabled" ? "row-disabled" : ""
        }
      />

      <style>{`
        .row-disabled td { opacity: 0.5; }
      `}</style>
    </div>
  );
});

export default DiscordPage;
