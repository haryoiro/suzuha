import { useState, memo } from "react";
import { Table, Typography, Space, Button, Modal, Form, Input, InputNumber, Select, message, Popconfirm, Tabs } from "antd";
import { PlusOutlined, DeleteOutlined, EditOutlined } from "@ant-design/icons";
import type { ColumnsType } from "antd/es/table";
import {
  useLocationDevices,
  useLocationPlaces,
  useUpsertDevice,
  useDeleteDevice,
  useCreatePlace,
  useUpdatePlace,
  useDeletePlace,
} from "../hooks/useLocation";
import { useUsers } from "../hooks/useUsers";
import type { LocationDevice, LocationPlace } from "../lib/api";

const { Title } = Typography;

// --- Device Section ---

const DeviceFormModal = memo(function DeviceFormModal({
  device,
  open,
  onClose,
}: {
  device: LocationDevice | null;
  open: boolean;
  onClose: () => void;
}) {
  const [form] = Form.useForm();
  const upsert = useUpsertDevice();
  const { data: usersData } = useUsers({ limit: 100 });
  const users = usersData?.data ?? [];
  const isEdit = !!device;

  const handleSubmit = async (values: { device_id: string; owner_name: string; user_id?: string }) => {
    try {
      await upsert.mutateAsync({ deviceId: values.device_id, ownerName: values.owner_name, userId: values.user_id });
      message.success(isEdit ? "Updated" : "Created");
      onClose();
    } catch {
      message.error("Failed");
    }
  };

  const handleUserChange = (userId: string | undefined) => {
    if (userId) {
      const user = users.find((u) => u.id === userId);
      if (user) {
        form.setFieldValue("owner_name", user.display_name);
      }
    }
  };

  return (
    <Modal
      title={isEdit ? "Edit Device" : "Add Device"}
      open={open}
      onCancel={onClose}
      footer={null}
      destroyOnClose
    >
      <Form
        form={form}
        layout="vertical"
        initialValues={device ? { device_id: device.device_id, owner_name: device.owner_name, user_id: device.user_id } : {}}
        onFinish={handleSubmit}
      >
        <Form.Item label="Device ID" name="device_id" rules={[{ required: true }]}>
          <Input placeholder="e.g. iPhone" disabled={isEdit} />
        </Form.Item>
        <Form.Item label="User" name="user_id">
          <Select
            allowClear
            showSearch
            placeholder="Select user"
            optionFilterProp="label"
            onChange={handleUserChange}
            options={users.map((u) => ({ value: u.id, label: u.display_name }))}
          />
        </Form.Item>
        <Form.Item label="Owner Name" name="owner_name" rules={[{ required: true }]}>
          <Input placeholder="e.g. はりょ" />
        </Form.Item>
        <Space>
          <Button type="primary" htmlType="submit" loading={upsert.isPending}>
            {isEdit ? "Update" : "Add"}
          </Button>
          <Button onClick={onClose}>Cancel</Button>
        </Space>
      </Form>
    </Modal>
  );
});

function DevicesSection() {
  const { data, isLoading } = useLocationDevices();
  const deleteDevice = useDeleteDevice();
  const [editingDevice, setEditingDevice] = useState<LocationDevice | null>(null);
  const [creating, setCreating] = useState(false);
  const devices = data?.data ?? [];

  const handleDelete = async (deviceId: string) => {
    try {
      await deleteDevice.mutateAsync(deviceId);
      message.success("Deleted");
    } catch {
      message.error("Delete failed");
    }
  };

  const columns: ColumnsType<LocationDevice> = [
    { title: "Device ID", dataIndex: "device_id", key: "device_id" },
    { title: "User", dataIndex: "user_display_name", key: "user", render: (name: string) => name || "-" },
    { title: "Owner Name", dataIndex: "owner_name", key: "owner_name" },
    {
      title: "",
      key: "actions",
      width: 80,
      render: (_, record) => (
        <Space>
          <Button size="small" icon={<EditOutlined />} onClick={() => setEditingDevice(record)} />
          <Popconfirm title="Delete?" onConfirm={() => handleDelete(record.device_id)}>
            <Button size="small" danger icon={<DeleteOutlined />} />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <>
      <Space style={{ marginBottom: 16, width: "100%", justifyContent: "flex-end" }}>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreating(true)}>
          Add Device
        </Button>
      </Space>
      <Table<LocationDevice>
        columns={columns}
        dataSource={devices}
        rowKey="device_id"
        loading={isLoading}
        pagination={false}
        scroll={{ x: 400 }}
      />
      <DeviceFormModal
        device={editingDevice}
        open={!!editingDevice || creating}
        onClose={() => { setEditingDevice(null); setCreating(false); }}
      />
    </>
  );
}

// --- Places Section ---

const PlaceFormModal = memo(function PlaceFormModal({
  place,
  open,
  onClose,
}: {
  place: LocationPlace | null;
  open: boolean;
  onClose: () => void;
}) {
  const [form] = Form.useForm();
  const createPlace = useCreatePlace();
  const updatePlace = useUpdatePlace();
  const isEdit = !!place;

  const handleSubmit = async (values: { name: string; latitude: number; longitude: number; radius_m: number }) => {
    try {
      if (isEdit && place) {
        await updatePlace.mutateAsync({ id: place.id, ...values });
      } else {
        await createPlace.mutateAsync(values);
      }
      message.success(isEdit ? "Updated" : "Created");
      onClose();
    } catch {
      message.error("Failed");
    }
  };

  return (
    <Modal
      title={isEdit ? "Edit Place" : "Add Place"}
      open={open}
      onCancel={onClose}
      footer={null}
      destroyOnClose
    >
      <Form
        form={form}
        layout="vertical"
        initialValues={
          place
            ? { name: place.name, latitude: place.latitude, longitude: place.longitude, radius_m: place.radius_m }
            : { radius_m: 200 }
        }
        onFinish={handleSubmit}
      >
        <Form.Item label="Name" name="name" rules={[{ required: true }]}>
          <Input placeholder="e.g. 自宅" />
        </Form.Item>
        <Form.Item label="Latitude" name="latitude" rules={[{ required: true }]}>
          <InputNumber style={{ width: "100%" }} step={0.000001} placeholder="35.681236" />
        </Form.Item>
        <Form.Item label="Longitude" name="longitude" rules={[{ required: true }]}>
          <InputNumber style={{ width: "100%" }} step={0.000001} placeholder="139.767125" />
        </Form.Item>
        <Form.Item label="Radius (m)" name="radius_m" rules={[{ required: true }]}>
          <InputNumber style={{ width: "100%" }} min={1} placeholder="200" />
        </Form.Item>
        <Space>
          <Button type="primary" htmlType="submit" loading={createPlace.isPending || updatePlace.isPending}>
            {isEdit ? "Update" : "Add"}
          </Button>
          <Button onClick={onClose}>Cancel</Button>
        </Space>
      </Form>
    </Modal>
  );
});

function PlacesSection() {
  const { data, isLoading } = useLocationPlaces();
  const deletePlaceMut = useDeletePlace();
  const [editingPlace, setEditingPlace] = useState<LocationPlace | null>(null);
  const [creating, setCreating] = useState(false);
  const places = data?.data ?? [];

  const handleDelete = async (id: string) => {
    try {
      await deletePlaceMut.mutateAsync(id);
      message.success("Deleted");
    } catch {
      message.error("Delete failed");
    }
  };

  const columns: ColumnsType<LocationPlace> = [
    { title: "Name", dataIndex: "name", key: "name" },
    { title: "Latitude", dataIndex: "latitude", key: "latitude", width: 140 },
    { title: "Longitude", dataIndex: "longitude", key: "longitude", width: 140 },
    { title: "Radius (m)", dataIndex: "radius_m", key: "radius_m", width: 110 },
    {
      title: "",
      key: "actions",
      width: 80,
      render: (_, record) => (
        <Space>
          <Button size="small" icon={<EditOutlined />} onClick={() => setEditingPlace(record)} />
          <Popconfirm title="Delete?" onConfirm={() => handleDelete(record.id)}>
            <Button size="small" danger icon={<DeleteOutlined />} />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <>
      <Space style={{ marginBottom: 16, width: "100%", justifyContent: "flex-end" }}>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreating(true)}>
          Add Place
        </Button>
      </Space>
      <Table<LocationPlace>
        columns={columns}
        dataSource={places}
        rowKey="id"
        loading={isLoading}
        pagination={false}
        scroll={{ x: 500 }}
      />
      <PlaceFormModal
        place={editingPlace}
        open={!!editingPlace || creating}
        onClose={() => { setEditingPlace(null); setCreating(false); }}
      />
    </>
  );
}

// --- Main Page ---

export const LocationPage = memo(function LocationPage() {
  return (
    <div>
      <Title level={3} style={{ marginBottom: 16 }}>Location Settings</Title>
      <Tabs
        defaultActiveKey="devices"
        items={[
          { key: "devices", label: "Devices", children: <DevicesSection /> },
          { key: "places", label: "Places", children: <PlacesSection /> },
        ]}
      />
    </div>
  );
});

export default LocationPage;
