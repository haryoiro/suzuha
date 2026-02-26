import { useState } from "react";
import {
  Table,
  Tag,
  Space,
  Typography,
  Button,
  Modal,
  Form,
  Input,
  Switch,
  Popconfirm,
  Card,
  Row,
  Col,
  Statistic,
  message,
} from "antd";
import {
  PlusOutlined,
  DeleteOutlined,
  LinkOutlined,
  WifiOutlined,
} from "@ant-design/icons";
import type { ColumnsType } from "antd/es/table";
import {
  useFeeds,
  useCreateFeed,
  useUpdateFeed,
  useDeleteFeed,
  useFeedItems,
  useFeedStats,
} from "../../hooks/useFeeds";
import type { Feed, FeedItem } from "../../lib/api";

const { Title, Text } = Typography;

export function FeedsPage() {
  const [createOpen, setCreateOpen] = useState(false);
  const [itemsFeedId, setItemsFeedId] = useState<string | null>(null);
  const [form] = Form.useForm();

  const { data, isLoading } = useFeeds();
  const { data: stats } = useFeedStats();
  const createFeed = useCreateFeed();
  const updateFeed = useUpdateFeed();
  const deleteFeed = useDeleteFeed();

  const handleCreate = async () => {
    try {
      const values = await form.validateFields();
      await createFeed.mutateAsync(values);
      form.resetFields();
      setCreateOpen(false);
      message.success("Feed added");
    } catch {
      // validation or API error
    }
  };

  const handleToggle = (record: Feed, enabled: boolean) => {
    updateFeed.mutate({ id: record.id, enabled });
  };

  const handleDelete = (id: string) => {
    deleteFeed.mutate(id, {
      onSuccess: () => message.success("Feed deleted"),
    });
  };

  const columns: ColumnsType<Feed> = [
    {
      title: "Name",
      dataIndex: "name",
      key: "name",
      render: (name: string, record: Feed) => (
        <a onClick={() => setItemsFeedId(record.id)}>{name}</a>
      ),
    },
    {
      title: "URL",
      dataIndex: "url",
      key: "url",
      ellipsis: true,
      render: (url: string) => (
        <a href={url} target="_blank" rel="noopener noreferrer">
          <LinkOutlined /> {url}
        </a>
      ),
    },
    {
      title: "Enabled",
      dataIndex: "enabled",
      key: "enabled",
      width: 100,
      render: (enabled: boolean, record: Feed) => (
        <Switch
          checked={enabled}
          onChange={(checked) => handleToggle(record, checked)}
          loading={updateFeed.isPending}
        />
      ),
    },
    {
      title: "Channel ID",
      dataIndex: "channel_id",
      key: "channel_id",
      width: 180,
      responsive: ["lg"],
      render: (v: string) => <Text code copyable>{v}</Text>,
    },
    {
      title: "Last Polled",
      dataIndex: "last_polled",
      key: "last_polled",
      width: 180,
      responsive: ["lg"],
      render: (v?: string) =>
        v ? new Date(v).toLocaleString() : <Text type="secondary">Never</Text>,
    },
    {
      title: "Actions",
      key: "actions",
      width: 80,
      render: (_: unknown, record: Feed) => (
        <Popconfirm
          title="Delete this feed?"
          onConfirm={() => handleDelete(record.id)}
          okText="Delete"
          okType="danger"
        >
          <Button type="text" danger icon={<DeleteOutlined />} size="small" />
        </Popconfirm>
      ),
    },
  ];

  return (
    <div>
      <Title level={3}>RSS Feeds</Title>

      <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
        <Col xs={12} sm={8}>
          <Card size="small">
            <Statistic
              title="Total Feeds"
              value={stats?.total ?? 0}
              prefix={<WifiOutlined />}
            />
          </Card>
        </Col>
        <Col xs={12} sm={8}>
          <Card size="small">
            <Statistic
              title="Enabled"
              value={stats?.enabled ?? 0}
              valueStyle={{ color: "#52c41a" }}
            />
          </Card>
        </Col>
        <Col xs={12} sm={8}>
          <Card size="small">
            <Statistic
              title="Disabled"
              value={(stats?.total ?? 0) - (stats?.enabled ?? 0)}
              valueStyle={{ color: "#8c8c8c" }}
            />
          </Card>
        </Col>
      </Row>

      <Space style={{ marginBottom: 16 }}>
        <Button
          type="primary"
          icon={<PlusOutlined />}
          onClick={() => setCreateOpen(true)}
        >
          Add Feed
        </Button>
      </Space>

      <Table<Feed>
        columns={columns}
        dataSource={data?.data ?? []}
        rowKey="id"
        loading={isLoading}
        pagination={false}
        scroll={{ x: 500 }}
      />

      <Modal
        title="Add RSS Feed"
        open={createOpen}
        onOk={handleCreate}
        onCancel={() => {
          setCreateOpen(false);
          form.resetFields();
        }}
        confirmLoading={createFeed.isPending}
        okText="Add"
      >
        <Form form={form} layout="vertical">
          <Form.Item
            name="name"
            label="Name"
            rules={[{ required: true, message: "Name is required" }]}
          >
            <Input placeholder="e.g. Hacker News" />
          </Form.Item>
          <Form.Item
            name="url"
            label="Feed URL"
            rules={[
              { required: true, message: "URL is required" },
              { type: "url", message: "Must be a valid URL" },
            ]}
          >
            <Input placeholder="https://example.com/feed.xml" />
          </Form.Item>
          <Form.Item
            name="channel_id"
            label="Discord Channel ID"
            rules={[{ required: true, message: "Channel ID is required" }]}
          >
            <Input placeholder="123456789012345678" />
          </Form.Item>
        </Form>
      </Modal>

      <FeedItemsModal
        feedId={itemsFeedId}
        onClose={() => setItemsFeedId(null)}
      />
    </div>
  );
}

function FeedItemsModal({
  feedId,
  onClose,
}: {
  feedId: string | null;
  onClose: () => void;
}) {
  const [offset, setOffset] = useState(0);
  const [limit] = useState(20);

  const { data, isLoading } = useFeedItems(feedId ?? "", { offset, limit });

  const columns: ColumnsType<FeedItem> = [
    {
      title: "Title",
      dataIndex: "title",
      key: "title",
      ellipsis: true,
      render: (title: string, record: FeedItem) => (
        <a href={record.link} target="_blank" rel="noopener noreferrer">
          {title}
        </a>
      ),
    },
    {
      title: "Published",
      dataIndex: "published_at",
      key: "published_at",
      width: 180,
      render: (v?: string) =>
        v ? new Date(v).toLocaleString() : "-",
    },
    {
      title: "Notified",
      dataIndex: "notified",
      key: "notified",
      width: 100,
      render: (v: boolean) =>
        v ? <Tag color="green">Yes</Tag> : <Tag>No</Tag>,
    },
    {
      title: "Memory",
      dataIndex: "memory_id",
      key: "memory_id",
      width: 100,
      render: (v: string) =>
        v ? <Tag color="blue">Linked</Tag> : <Text type="secondary">-</Text>,
    },
  ];

  return (
    <Modal
      title="Feed Items"
      open={!!feedId}
      onCancel={onClose}
      footer={null}
      width="90vw"
      style={{ maxWidth: 900 }}
    >
      <Table<FeedItem>
        columns={columns}
        dataSource={data?.data ?? []}
        rowKey="id"
        loading={isLoading}
        size="small"
        pagination={{
          total: data?.total ?? 0,
          current: Math.floor(offset / limit) + 1,
          pageSize: limit,
          onChange: (page) => setOffset((page - 1) * limit),
          showTotal: (total) => `${total} items`,
        }}
      />
    </Modal>
  );
}
