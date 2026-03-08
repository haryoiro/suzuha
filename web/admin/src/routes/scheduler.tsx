import React, { useMemo } from "react";
import { Card, Table, Button, Tag, Typography, message, Tooltip, Descriptions } from "antd";
import { PlayCircleOutlined, ClockCircleOutlined } from "@ant-design/icons";
import type { ColumnsType } from "antd/es/table";
import { useSchedulerJobs, useTriggerJob } from "../hooks/useScheduler";
import { formatJST, formatRelative } from "../lib/date";
import type { SchedulerJob } from "../lib/api";

const { Title, Text } = Typography;

const SchedulerPage: React.FC = React.memo(() => {
  const { data, isLoading } = useSchedulerJobs();
  const trigger = useTriggerJob();

  const jobs = useMemo(() => data?.data ?? [], [data]);

  const handleTrigger = async (task: string, name: string) => {
    try {
      await trigger.mutateAsync(task);
      message.success(`${name} を実行しました`);
    } catch {
      message.error(`${name} の実行に失敗しました`);
    }
  };

  const columns: ColumnsType<SchedulerJob> = [
    {
      title: "Name",
      dataIndex: "name",
      key: "name",
      render: (name: string) => <Text strong style={{ whiteSpace: "nowrap" }}>{name}</Text>,
    },
    {
      title: "Task",
      dataIndex: "task",
      key: "task",
      render: (task: string) => <Tag color="cyan" style={{ whiteSpace: "nowrap" }}>{task}</Tag>,
    },
    {
      title: "Cron",
      dataIndex: "cron",
      key: "cron",
      render: (cron: string) => (
        <Text code style={{ fontSize: 12, whiteSpace: "nowrap" }}>
          {cron}
        </Text>
      ),
    },
    {
      title: "Last Run",
      dataIndex: "prev",
      key: "prev",
      render: (prev: string) => {
        if (!prev || prev.startsWith("0001")) return <Text type="secondary">-</Text>;
        return (
          <Tooltip title={formatJST(prev, "YYYY-MM-DD HH:mm:ss")}>
            {formatRelative(prev)}
          </Tooltip>
        );
      },
    },
    {
      title: "Next Run",
      dataIndex: "next",
      key: "next",
      render: (next: string) => {
        if (!next || next.startsWith("0001")) return <Text type="secondary">-</Text>;
        return (
          <Tooltip title={formatJST(next, "YYYY-MM-DD HH:mm:ss")}>
            {formatJST(next, "HH:mm")}
          </Tooltip>
        );
      },
    },
    {
      title: "Config",
      dataIndex: "config",
      key: "config",
      responsive: ["lg"],
      render: (config: Record<string, unknown> | undefined) => {
        if (!config || Object.keys(config).length === 0)
          return <Text type="secondary">-</Text>;
        return (
          <Descriptions size="small" column={1} style={{ marginBottom: 0 }}>
            {Object.entries(config).map(([k, v]) => (
              <Descriptions.Item key={k} label={<Text type="secondary" style={{ fontSize: 11 }}>{k}</Text>}>
                <Text style={{ fontSize: 12 }}>{String(v)}</Text>
              </Descriptions.Item>
            ))}
          </Descriptions>
        );
      },
    },
    {
      title: "",
      key: "action",
      width: 80,
      render: (_, record) => (
        <Button
          type="text"
          icon={<PlayCircleOutlined />}
          loading={trigger.isPending && trigger.variables === record.task}
          onClick={() => handleTrigger(record.task, record.name)}
        >
          Run
        </Button>
      ),
    },
  ];

  return (
    <div style={{ maxWidth: 1200 }}>
      <div style={{ marginBottom: 16, display: "flex", alignItems: "center", gap: 8 }}>
        <ClockCircleOutlined style={{ fontSize: 20, color: "#06b6d4" }} />
        <Title level={4} style={{ margin: 0 }}>
          Scheduler
        </Title>
        <Text type="secondary" style={{ marginLeft: 8 }}>
          {jobs.length} jobs
        </Text>
      </div>

      <Card>
        <Table
          dataSource={jobs}
          columns={columns}
          rowKey="name"
          loading={isLoading}
          pagination={false}
          size="middle"
        />
      </Card>
    </div>
  );
});

export default SchedulerPage;
