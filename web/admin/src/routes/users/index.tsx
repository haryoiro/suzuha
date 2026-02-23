import { useState } from "react";
import { Table, Tag, Input, Space, Typography, Card, Descriptions, Modal, List } from "antd";
import { SearchOutlined } from "@ant-design/icons";
import type { ColumnsType } from "antd/es/table";
import { useUsers, useUser, useAffinityEvents } from "../../hooks/useUsers";
import type { User, PlatformLink } from "../../lib/api";

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
        <a onClick={() => setSelectedUserId(record.id)}>{name || "(unnamed)"}</a>
      ),
    },
    {
      title: "Role",
      dataIndex: "role",
      key: "role",
      width: 120,
      render: (role: string) => {
        const color = role === "admin" ? "red" : role === "moderator" ? "orange" : "blue";
        return <Tag color={color}>{role}</Tag>;
      },
    },
    {
      title: "Affinity",
      dataIndex: "affinity",
      key: "affinity",
      width: 100,
      sorter: (a: User, b: User) => a.affinity - b.affinity,
      render: (val: number) => {
        const color = val > 0 ? "#52c41a" : val < 0 ? "#ff4d4f" : "#8c8c8c";
        return <Text style={{ color }}>{val.toFixed(1)}</Text>;
      },
    },
    {
      title: "Platforms",
      dataIndex: "platforms",
      key: "platforms",
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
      render: (v: string) => new Date(v).toLocaleString(),
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
          style={{ width: 300 }}
        />
      </Space>

      <Table<User>
        columns={columns}
        dataSource={data?.data ?? []}
        rowKey="id"
        loading={isLoading}
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

function UserDetailModal({
  userId,
  onClose,
}: {
  userId: string | null;
  onClose: () => void;
}) {
  const { data: userData } = useUser(userId ?? "");
  const { data: eventsData } = useAffinityEvents(userId ?? "", 20);

  const user = userData?.data;
  const events = eventsData?.data ?? [];

  return (
    <Modal
      title={user?.display_name || "User Detail"}
      open={!!userId}
      onCancel={onClose}
      footer={null}
      width={700}
    >
      {user && (
        <Space direction="vertical" style={{ width: "100%" }} size="large">
          <Card size="small">
            <Descriptions column={2} size="small">
              <Descriptions.Item label="ID">
                <Text code copyable>{user.id}</Text>
              </Descriptions.Item>
              <Descriptions.Item label="Display Name">
                {user.display_name}
              </Descriptions.Item>
              <Descriptions.Item label="Role">
                <Tag>{user.role}</Tag>
              </Descriptions.Item>
              <Descriptions.Item label="Affinity">
                <Text
                  style={{
                    color:
                      user.affinity > 0
                        ? "#52c41a"
                        : user.affinity < 0
                          ? "#ff4d4f"
                          : "#8c8c8c",
                  }}
                >
                  {user.affinity.toFixed(1)}
                </Text>
              </Descriptions.Item>
              <Descriptions.Item label="Created">
                {new Date(user.created_at).toLocaleString()}
              </Descriptions.Item>
              <Descriptions.Item label="Updated">
                {new Date(user.updated_at).toLocaleString()}
              </Descriptions.Item>
            </Descriptions>
          </Card>

          {user.platforms && user.platforms.length > 0 && (
            <Card title="Platform Links" size="small">
              {user.platforms.map((p) => (
                <Tag key={`${p.platform}-${p.platform_user_id}`}>
                  {p.platform}: {p.platform_name || p.platform_user_id}
                </Tag>
              ))}
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
                        {new Date(e.created_at).toLocaleString()}
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
