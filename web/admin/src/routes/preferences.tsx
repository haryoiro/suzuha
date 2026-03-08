import { useState, memo } from "react";
import {
  Table,
  Tag,
  Typography,
  Button,
  Popconfirm,
  Card,
  Row,
  Col,
  Statistic,
  Select,
  Progress,
  Space,
  Input,
  message,
} from "antd";
import {
  HeartOutlined,
  DislikeOutlined,
  QuestionOutlined,
  DeleteOutlined,
  CheckOutlined,
  CloseOutlined,
} from "@ant-design/icons";
import type { ColumnsType } from "antd/es/table";
import {
  usePreferences,
  usePreferenceStats,
  useUpdatePreference,
  useDeletePreference,
} from "../hooks/usePreferences";
import type { Preference } from "../lib/api";
import { formatJST } from "../lib/date";

const { Title, Text } = Typography;

const stanceColors: Record<string, string> = {
  liked: "green",
  disliked: "red",
  curious: "blue",
  undecided: "default",
};

const stanceLabels: Record<string, string> = {
  liked: "Liked",
  disliked: "Disliked",
  curious: "Curious",
  undecided: "Undecided",
};

function EditableCell({
  value,
  onSave,
  type = "text",
}: {
  value: string;
  onSave: (v: string) => void;
  type?: "text" | "number";
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(value);

  if (!editing) {
    return (
      <span
        style={{ cursor: "pointer", borderBottom: "1px dashed rgba(255,255,255,0.2)" }}
        onClick={() => {
          setDraft(value);
          setEditing(true);
        }}
      >
        {value}
      </span>
    );
  }

  return (
    <Space size={4}>
      <Input
        size="small"
        value={draft}
        type={type}
        style={{ width: type === "number" ? 70 : 140 }}
        onChange={(e) => setDraft(e.target.value)}
        onPressEnter={() => {
          onSave(draft);
          setEditing(false);
        }}
        autoFocus
      />
      <Button
        size="small"
        type="text"
        icon={<CheckOutlined />}
        onClick={() => {
          onSave(draft);
          setEditing(false);
        }}
      />
      <Button
        size="small"
        type="text"
        icon={<CloseOutlined />}
        onClick={() => setEditing(false)}
      />
    </Space>
  );
}

export const PreferencesPage = memo(function PreferencesPage() {
  const [stanceFilter, setStanceFilter] = useState<string>("all");
  const { data, isLoading } = usePreferences(
    stanceFilter === "all" ? undefined : stanceFilter
  );
  const { data: stats } = usePreferenceStats();
  const updatePref = useUpdatePreference();
  const deletePref = useDeletePreference();

  const handleStanceChange = (id: number, stance: string) => {
    updatePref.mutate(
      { id, stance },
      { onSuccess: () => message.success("Updated") }
    );
  };

  const handleConfidenceChange = (id: number, val: string) => {
    const confidence = parseFloat(val);
    if (isNaN(confidence) || confidence < 0 || confidence > 1) {
      message.error("Confidence must be 0.0-1.0");
      return;
    }
    updatePref.mutate(
      { id, confidence },
      { onSuccess: () => message.success("Updated") }
    );
  };

  const handleReasoningChange = (id: number, reasoning: string) => {
    updatePref.mutate(
      { id, reasoning },
      { onSuccess: () => message.success("Updated") }
    );
  };

  const handleDelete = (id: number) => {
    deletePref.mutate(id, {
      onSuccess: () => message.success("Deleted"),
    });
  };

  const columns: ColumnsType<Preference> = [
    {
      title: "Topic",
      dataIndex: "topic",
      key: "topic",
      ellipsis: true,
      width: 200,
      render: (topic: string) => <Text strong>{topic}</Text>,
    },
    {
      title: "Category",
      dataIndex: "category",
      key: "category",
      width: 120,
      render: (v: string) => <Tag>{v}</Tag>,
    },
    {
      title: "Stance",
      dataIndex: "stance",
      key: "stance",
      width: 130,
      render: (stance: string, record: Preference) => (
        <Select
          size="small"
          value={stance}
          onChange={(v) => handleStanceChange(record.id, v)}
          style={{ width: 110 }}
          options={[
            { value: "liked", label: "Liked" },
            { value: "disliked", label: "Disliked" },
            { value: "curious", label: "Curious" },
            { value: "undecided", label: "Undecided" },
          ]}
        />
      ),
    },
    {
      title: "Confidence",
      dataIndex: "confidence",
      key: "confidence",
      width: 130,
      sorter: (a: Preference, b: Preference) => a.confidence - b.confidence,
      render: (v: number, record: Preference) => (
        <Space>
          <Progress
            type="circle"
            percent={Math.round(v * 100)}
            size={28}
            strokeColor={v >= 0.7 ? "#52c41a" : v >= 0.4 ? "#faad14" : "#8c8c8c"}
          />
          <EditableCell
            value={v.toFixed(2)}
            type="number"
            onSave={(val) => handleConfidenceChange(record.id, val)}
          />
        </Space>
      ),
    },
    {
      title: "Encounters",
      dataIndex: "encounters",
      key: "encounters",
      width: 90,
      sorter: (a: Preference, b: Preference) => a.encounters - b.encounters,
      render: (v: number) => <Text>{v}</Text>,
    },
    {
      title: "Reasoning",
      dataIndex: "reasoning",
      key: "reasoning",
      ellipsis: true,
      render: (v: string, record: Preference) => (
        <EditableCell
          value={v}
          onSave={(val) => handleReasoningChange(record.id, val)}
        />
      ),
    },
    {
      title: "Shared",
      dataIndex: "shared",
      key: "shared",
      width: 80,
      render: (v: boolean) =>
        v ? <Tag color="cyan">Yes</Tag> : <Text type="secondary">No</Text>,
    },
    {
      title: "Updated",
      dataIndex: "updated_at",
      key: "updated_at",
      width: 160,
      responsive: ["lg"],
      render: (v: string) => (v ? formatJST(v) : "-"),
    },
    {
      title: "",
      key: "actions",
      width: 50,
      render: (_: unknown, record: Preference) => (
        <Popconfirm
          title="Delete this preference?"
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
      <Title level={3}>Preferences</Title>

      <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
        <Col xs={12} sm={4}>
          <Card size="small">
            <Statistic title="Total" value={stats?.total ?? 0} />
          </Card>
        </Col>
        <Col xs={12} sm={4}>
          <Card size="small">
            <Statistic
              title="Liked"
              value={stats?.liked ?? 0}
              prefix={<HeartOutlined />}
              valueStyle={{ color: "#52c41a" }}
            />
          </Card>
        </Col>
        <Col xs={12} sm={4}>
          <Card size="small">
            <Statistic
              title="Disliked"
              value={stats?.disliked ?? 0}
              prefix={<DislikeOutlined />}
              valueStyle={{ color: "#ff4d4f" }}
            />
          </Card>
        </Col>
        <Col xs={12} sm={4}>
          <Card size="small">
            <Statistic
              title="Curious"
              value={stats?.curious ?? 0}
              prefix={<QuestionOutlined />}
              valueStyle={{ color: "#1890ff" }}
            />
          </Card>
        </Col>
        <Col xs={12} sm={4}>
          <Card size="small">
            <Statistic
              title="Undecided"
              value={stats?.undecided ?? 0}
              valueStyle={{ color: "#8c8c8c" }}
            />
          </Card>
        </Col>
      </Row>

      <Space style={{ marginBottom: 16 }}>
        <Text>Filter:</Text>
        <Select
          value={stanceFilter}
          onChange={setStanceFilter}
          style={{ width: 140 }}
          options={[
            { value: "all", label: "All" },
            { value: "liked", label: "Liked" },
            { value: "disliked", label: "Disliked" },
            { value: "curious", label: "Curious" },
            { value: "undecided", label: "Undecided" },
          ]}
        />
      </Space>

      <Table<Preference>
        columns={columns}
        dataSource={data?.data ?? []}
        rowKey="id"
        loading={isLoading}
        pagination={{ pageSize: 50, showTotal: (t) => `${t} items` }}
        scroll={{ x: 800 }}
        size="small"
      />
    </div>
  );
});

export default PreferencesPage;
