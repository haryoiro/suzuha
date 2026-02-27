import { useState } from "react";
import { Table, Tag, Input, Space, Typography, Card, Descriptions, Modal, List, Button, Form, Switch, Select, message } from "antd";
import { SearchOutlined, RobotOutlined, EditOutlined } from "@ant-design/icons";
import { useQueryClient } from "@tanstack/react-query";
import type { ColumnsType } from "antd/es/table";
import { useUsers, useUser, useAffinityEvents, useUserGuilds, useUserMemories } from "../../hooks/useUsers";
import { usersApi, type User, type PlatformLink, type UserGuildChannel } from "../../lib/api";
import { formatJST } from "../../lib/date";

const { Title, Text } = Typography;

export function UsersPage() {
  const [offset, setOffset] = useState(0);
  const [limit] = useState(50);
  const [search, setSearch] = useState("");
  const [selectedUserId, setSelectedUserId] = useState<string | null>(null);

  const { data, isLoading } = useUsers({ offset, limit, q: search });

  const columns: ColumnsType<User> = [
    {
      title: "Display Name",
      dataIndex: "display_name",
      key: "display_name",
      render: (name: string, record: User) => (
        <Space>
          <a onClick={() => setSelectedUserId(record.id)}>{name || "(unnamed)"}</a>
          {record.is_bot && <Tag icon={<RobotOutlined />} color="purple">BOT</Tag>}
        </Space>
      ),
    },
    {
      title: "Role",
      dataIndex: "role",
      key: "role",
      width: 120,
      render: (role: string) => {
        const color = role === "owner" ? "red" : role === "admin" ? "orange" : "blue";
        return <Tag color={color}>{role}</Tag>;
      },
    },
    {
      title: "Closeness",
      dataIndex: "closeness",
      key: "closeness",
      width: 90,
      sorter: (a: User, b: User) => a.closeness - b.closeness,
      render: (val: number) => {
        const color = val > 0 ? "#52c41a" : val < 0 ? "#ff4d4f" : "#8c8c8c";
        return <Text style={{ color }}>{val.toFixed(1)}</Text>;
      },
    },
    {
      title: "Trust",
      dataIndex: "trust",
      key: "trust",
      width: 80,
      sorter: (a: User, b: User) => a.trust - b.trust,
      render: (val: number) => {
        const color = val > 0 ? "#52c41a" : val < 0 ? "#ff4d4f" : "#8c8c8c";
        return <Text style={{ color }}>{val.toFixed(1)}</Text>;
      },
    },
    {
      title: "Interest",
      dataIndex: "interest",
      key: "interest",
      width: 80,
      sorter: (a: User, b: User) => a.interest - b.interest,
      render: (val: number) => {
        const color = val > 0 ? "#52c41a" : val < 0 ? "#ff4d4f" : "#8c8c8c";
        return <Text style={{ color }}>{val.toFixed(1)}</Text>;
      },
    },
    {
      title: "Platforms",
      dataIndex: "platforms",
      key: "platforms",
      responsive: ["md"],
      render: (platforms?: PlatformLink[]) =>
        platforms?.map((p) => (
          <Tag key={`${p.platform}-${p.platform_user_id}`}>
            {p.platform}: {p.platform_name || p.platform_user_id}
          </Tag>
        )) ?? "-",
    },
    {
      title: "Updated",
      dataIndex: "updated_at",
      key: "updated_at",
      width: 180,
      responsive: ["md"],
      render: (v: string) => formatJST(v),
    },
  ];

  return (
    <div>
      <Title level={3}>Users</Title>

      <Space style={{ marginBottom: 16 }}>
        <Input
          placeholder="Search users..."
          prefix={<SearchOutlined />}
          value={search}
          onChange={(e) => {
            setSearch(e.target.value);
            setOffset(0);
          }}
          allowClear
          style={{ width: "100%", maxWidth: 300 }}
        />
      </Space>

      <Table<User>
        columns={columns}
        dataSource={data?.data ?? []}
        rowKey="id"
        loading={isLoading}
        scroll={{ x: 400 }}
        pagination={{
          total: data?.total ?? 0,
          current: Math.floor(offset / limit) + 1,
          pageSize: limit,
          onChange: (page) => setOffset((page - 1) * limit),
          showTotal: (total) => `${total} users`,
        }}
      />

      <UserDetailModal
        userId={selectedUserId}
        onClose={() => setSelectedUserId(null)}
      />
    </div>
  );
}

/** Group guild-channel entries by guild for display. */
function groupByGuild(entries: UserGuildChannel[]) {
  const map = new Map<string, { name: string; channels: { id: string; name: string; lastSeen: string }[] }>();
  for (const e of entries) {
    let guild = map.get(e.guild_id);
    if (!guild) {
      guild = { name: e.guild_name, channels: [] };
      map.set(e.guild_id, guild);
    }
    guild.channels.push({ id: e.channel_id, name: e.channel_name, lastSeen: e.last_seen_at });
  }
  return Array.from(map.entries());
}

function UserDetailModal({
  userId,
  onClose,
}: {
  userId: string | null;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const { data: userData } = useUser(userId ?? "");
  const { data: eventsData } = useAffinityEvents(userId ?? "", 20);
  const { data: guildsData } = useUserGuilds(userId ?? "");
  const { data: memoriesData } = useUserMemories(userId ?? "", 20);
  const [editing, setEditing] = useState(false);

  const user = userData?.data;
  const events = eventsData?.data ?? [];
  const guildEntries = guildsData?.data ?? [];
  const memories = memoriesData?.data ?? [];

  const handleSave = async (values: { display_name: string; role: string; is_bot: boolean }) => {
    if (!userId) return;
    try {
      await usersApi.update(userId, values);
      message.success("Updated");
      setEditing(false);
      queryClient.invalidateQueries({ queryKey: ["user", userId] });
      queryClient.invalidateQueries({ queryKey: ["users"] });
    } catch {
      message.error("Update failed");
    }
  };

  return (
    <Modal
      title={
        <Space>
          {user?.display_name || "User Detail"}
          {user?.is_bot && <Tag icon={<RobotOutlined />} color="purple">BOT</Tag>}
        </Space>
      }
      open={!!userId}
      onCancel={() => { setEditing(false); onClose(); }}
      footer={null}
      width="90vw"
      style={{ maxWidth: 700 }}
    >
      {user && (
        <Space direction="vertical" style={{ width: "100%" }} size="large">
          {editing ? (
            <Card size="small" title="Edit User">
              <Form
                layout="vertical"
                initialValues={{ display_name: user.display_name, role: user.role, is_bot: user.is_bot }}
                onFinish={handleSave}
              >
                <Form.Item label="Display Name" name="display_name">
                  <Input />
                </Form.Item>
                <Form.Item label="Role" name="role">
                  <Select options={[
                    { value: "owner", label: "owner" },
                    { value: "member", label: "member" },
                    { value: "guest", label: "guest" },
                  ]} />
                </Form.Item>
                <Form.Item label="Bot" name="is_bot" valuePropName="checked">
                  <Switch />
                </Form.Item>
                <Space>
                  <Button type="primary" htmlType="submit">Save</Button>
                  <Button onClick={() => setEditing(false)}>Cancel</Button>
                </Space>
              </Form>
            </Card>
          ) : (
            <Card size="small" extra={<Button icon={<EditOutlined />} size="small" onClick={() => setEditing(true)}>Edit</Button>}>
              <Descriptions column={1} size="small">
                <Descriptions.Item label="ID">
                  <Text code copyable>{user.id}</Text>
                </Descriptions.Item>
                <Descriptions.Item label="Display Name">
                  {user.display_name || <Text type="secondary">(unnamed)</Text>}
                </Descriptions.Item>
                <Descriptions.Item label="Role">
                  <Tag>{user.role}</Tag>
                </Descriptions.Item>
                <Descriptions.Item label="Closeness">
                  <Text style={{ color: user.closeness > 0 ? "#52c41a" : user.closeness < 0 ? "#ff4d4f" : "#8c8c8c" }}>
                    {user.closeness.toFixed(1)}
                  </Text>
                </Descriptions.Item>
                <Descriptions.Item label="Trust">
                  <Text style={{ color: user.trust > 0 ? "#52c41a" : user.trust < 0 ? "#ff4d4f" : "#8c8c8c" }}>
                    {user.trust.toFixed(1)}
                  </Text>
                </Descriptions.Item>
                <Descriptions.Item label="Interest">
                  <Text style={{ color: user.interest > 0 ? "#52c41a" : user.interest < 0 ? "#ff4d4f" : "#8c8c8c" }}>
                    {user.interest.toFixed(1)}
                  </Text>
                </Descriptions.Item>
                <Descriptions.Item label="Created">
                  {formatJST(user.created_at)}
                </Descriptions.Item>
                <Descriptions.Item label="Updated">
                  {formatJST(user.updated_at)}
                </Descriptions.Item>
              </Descriptions>
            </Card>
          )}

          {user.platforms && user.platforms.length > 0 && (
            <Card title="Platform Links" size="small">
              <List
                size="small"
                dataSource={user.platforms}
                renderItem={(p) => (
                  <List.Item>
                    <Space>
                      <Tag color="blue">{p.platform}</Tag>
                      <Text>{p.platform_name || "(no name)"}</Text>
                      <Text code copyable style={{ fontSize: 12 }}>{p.platform_user_id}</Text>
                    </Space>
                  </List.Item>
                )}
              />
            </Card>
          )}

          {guildEntries.length > 0 && (
            <Card title="Servers" size="small">
              {groupByGuild(guildEntries).map(([guildId, guild]) => (
                <div key={guildId} style={{ marginBottom: 8 }}>
                  <Text strong>{guild.name || guildId}</Text>
                  <Text type="secondary" style={{ fontSize: 12, marginLeft: 8 }}>{guildId}</Text>
                  <div style={{ marginLeft: 16, marginTop: 4 }}>
                    {guild.channels.map((ch) => (
                      <Tag key={ch.id} style={{ marginBottom: 4 }}>
                        #{ch.name || ch.id}
                      </Tag>
                    ))}
                  </div>
                </div>
              ))}
            </Card>
          )}

          {memories.length > 0 && (
            <Card title="Known Facts" size="small">
              <List
                size="small"
                dataSource={memories}
                renderItem={(m) => (
                  <List.Item>
                    <Text>{m.content}</Text>
                  </List.Item>
                )}
              />
            </Card>
          )}

          {events.length > 0 && (
            <Card title="Affinity History" size="small">
              <List
                size="small"
                dataSource={events}
                renderItem={(e) => (
                  <List.Item>
                    <Space>
                      <Tag color={e.axis === "trust" ? "orange" : e.axis === "interest" ? "purple" : "blue"}>
                        {e.axis || "closeness"}
                      </Tag>
                      <Text
                        style={{
                          color: e.delta > 0 ? "#52c41a" : "#ff4d4f",
                          fontWeight: 600,
                          minWidth: 50,
                        }}
                      >
                        {e.delta > 0 ? "+" : ""}
                        {e.delta.toFixed(1)}
                      </Text>
                      <Text type="secondary">{e.reason}</Text>
                      <Text type="secondary" style={{ fontSize: 12 }}>
                        {formatJST(e.created_at)}
                      </Text>
                    </Space>
                  </List.Item>
                )}
              />
            </Card>
          )}

          {user.metadata && Object.keys(user.metadata).length > 0 && (
            <Card title="Metadata" size="small">
              <pre style={{ margin: 0, fontSize: 12 }}>
                {JSON.stringify(user.metadata, null, 2)}
              </pre>
            </Card>
          )}
        </Space>
      )}
    </Modal>
  );
}
