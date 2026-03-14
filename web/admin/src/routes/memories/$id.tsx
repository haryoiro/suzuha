import { useEffect, useRef, memo } from "react";
import {
  Card,
  Form,
  Input,
  Select,
  Button,
  Space,
  Spin,
  Upload,
  Image,
  message,
  Descriptions,
} from "antd";
import { ArrowLeftOutlined, UploadOutlined, PaperClipOutlined } from "@ant-design/icons";
import { useQueryClient } from "@tanstack/react-query";
import { useMemory, useUpdateMemory, useDeleteMemory } from "../../hooks/useMemories";
import { memoriesApi, getMediaURL, getAttachments } from "../../lib/api";
import type { MemoryAttachment } from "../../lib/api";
import { formatJST } from "../../lib/date";

const { TextArea } = Input;

interface Props {
  id: string;
  onBack: () => void;
}

export const MemoryDetailPage = memo(function MemoryDetailPage({ id, onBack }: Props) {
  const queryClient = useQueryClient();
  const { data: memory, isLoading } = useMemory(id);
  const updateMutation = useUpdateMemory();
  const deleteMutation = useDeleteMemory();
  const [form] = Form.useForm();

  useEffect(() => {
    if (memory) {
      form.setFieldsValue({
        type: memory.type,
        content: memory.content,
        metadata: memory.metadata
          ? JSON.stringify(memory.metadata, null, 2)
          : "",
      });
    }
  }, [memory, form]);

  if (isLoading) {
    return (
      <div style={{ textAlign: "center", padding: 48 }}>
        <Spin size="large" />
      </div>
    );
  }

  if (!memory) {
    return <div>Memory not found</div>;
  }

  const handleSave = async () => {
    try {
      const values = await form.validateFields();
      let metadata: Record<string, unknown> | undefined;
      if (values.metadata) {
        try {
          metadata = JSON.parse(values.metadata);
        } catch {
          message.error("Invalid JSON in metadata");
          return;
        }
      }
      await updateMutation.mutateAsync({
        id,
        type: values.type,
        content: values.content,
        metadata,
      });
      message.success("Updated");
    } catch {
      // validation error
    }
  };

  const handleDelete = async () => {
    await deleteMutation.mutateAsync(id);
    message.success("Deleted");
    onBack();
  };

  return (
    <div>
      <Button
        type="text"
        icon={<ArrowLeftOutlined />}
        onClick={onBack}
        style={{ marginBottom: 16 }}
      >
        Back to list
      </Button>

      <Card title={`Memory: ${id.slice(0, 8)}...`}>
        <Descriptions size="small" column={2} style={{ marginBottom: 24 }}>
          <Descriptions.Item label="Created">
            {formatJST(memory.created_at)}
          </Descriptions.Item>
          <Descriptions.Item label="Updated">
            {formatJST(memory.updated_at)}
          </Descriptions.Item>
          <Descriptions.Item label="ID">{memory.id}</Descriptions.Item>
        </Descriptions>

        <Form form={form} layout="vertical">
          <Form.Item name="type" label="Type" rules={[{ required: true }]}>
            <Select
              options={[
                { label: "user", value: "user" },
                { label: "world", value: "world" },
                { label: "tool", value: "tool" },
                { label: "episode", value: "episode" },
                { label: "self", value: "self" },
              ]}
            />
          </Form.Item>
          <Form.Item
            name="content"
            label="Content"
            rules={[{ required: true }]}
          >
            <TextArea rows={6} />
          </Form.Item>
          <Form.Item name="metadata" label="Metadata (JSON)">
            <TextArea rows={4} style={{ fontFamily: "monospace" }} />
          </Form.Item>
          <Form.Item>
            <Space>
              <Button
                type="primary"
                onClick={handleSave}
                loading={updateMutation.isPending}
              >
                Save
              </Button>
              <Button danger onClick={handleDelete} loading={deleteMutation.isPending}>
                Delete
              </Button>
            </Space>
          </Form.Item>
        </Form>
      </Card>

      <Card
        title={<><PaperClipOutlined /> Attachments</>}
        size="small"
        style={{ marginTop: 16 }}
        extra={
          <Upload
            showUploadList={false}
            accept="image/*,audio/*"
            customRequest={async ({ file, onSuccess, onError }) => {
              try {
                await memoriesApi.uploadMedia(id, file as File);
                queryClient.invalidateQueries({ queryKey: ["memory", id] });
                message.success("Uploaded");
                onSuccess?.(null);
              } catch (err) {
                message.error("Upload failed");
                onError?.(err as Error);
              }
            }}
          >
            <Button icon={<UploadOutlined />} size="small">Upload</Button>
          </Upload>
        }
      >
        {getAttachments(memory).length > 0 ? (
          <div style={{ display: "flex", flexWrap: "wrap", gap: 12 }}>
            {getAttachments(memory).map((att: MemoryAttachment) => (
              <div key={att.key} style={{ textAlign: "center" }}>
                {att.modality === "image" ? (
                  <Image
                    src={getMediaURL(att.key)}
                    alt={att.key}
                    width={120}
                    height={120}
                    style={{ objectFit: "cover", borderRadius: 4 }}
                  />
                ) : (
                  <audio controls src={getMediaURL(att.key)} style={{ width: 200 }} />
                )}
                <div style={{ fontSize: 11, color: "rgba(255,255,255,0.45)", marginTop: 4 }}>
                  {att.mime_type}
                </div>
              </div>
            ))}
          </div>
        ) : (
          <div style={{ color: "rgba(255,255,255,0.3)", padding: 12, textAlign: "center" }}>
            No attachments
          </div>
        )}
      </Card>
    </div>
  );
});

export default MemoryDetailPage;
