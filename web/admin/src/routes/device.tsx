import { useState, useEffect, useRef, useCallback } from "react";
import { Card, Tag, Typography, Space, Empty, List, Switch, Slider } from "antd";
import {
  CameraOutlined,
  DisconnectOutlined,
  LinkOutlined,
  SoundOutlined,
} from "@ant-design/icons";
import {
  getDeviceFrameURL,
  connectDetectionStream,
  deviceVisionApi,
  deviceVolumeApi,
  type DeviceDetectionEvent,
  type DeviceDetection,
} from "../lib/api";

const { Title, Text } = Typography;

const COLORS = [
  "#06b6d4",
  "#f59e0b",
  "#10b981",
  "#ef4444",
  "#8b5cf6",
  "#ec4899",
  "#14b8a6",
  "#f97316",
];

function labelColor(label: string): string {
  let hash = 0;
  for (let i = 0; i < label.length; i++) {
    hash = label.charCodeAt(i) + ((hash << 5) - hash);
  }
  return COLORS[Math.abs(hash) % COLORS.length];
}

export default function DevicePage() {
  const [connected, setConnected] = useState(false);
  const [detections, setDetections] = useState<DeviceDetection[]>([]);
  const [inferenceMs, setInferenceMs] = useState(0);
  const [frameSize, setFrameSize] = useState({ w: 640, h: 480 });
  const [hasFrame, setHasFrame] = useState(false);
  const [visionEnabled, setVisionEnabled] = useState(true);
  const [volume, setVolume] = useState(50);

  const imgRef = useRef<HTMLImageElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const frameTimerRef = useRef<number>(0);

  // Fetch vision toggle state.
  useEffect(() => {
    deviceVisionApi.get().then((r) => setVisionEnabled(r.enabled)).catch(() => {});
  }, []);

  // Poll frame image.
  useEffect(() => {
    let alive = true;
    const poll = () => {
      if (!alive) return;
      const img = imgRef.current;
      if (img) {
        const newSrc = `${getDeviceFrameURL()}?t=${Date.now()}`;
        img.src = newSrc;
      }
      frameTimerRef.current = window.setTimeout(poll, 200);
    };
    poll();
    return () => {
      alive = false;
      clearTimeout(frameTimerRef.current);
    };
  }, []);

  // Handle image load/error for connection status.
  const onImgLoad = useCallback(() => {
    setHasFrame(true);
    setConnected(true);
  }, []);
  const onImgError = useCallback(() => {
    setConnected(false);
  }, []);

  // SSE detection stream.
  useEffect(() => {
    const es = connectDetectionStream();
    es.onmessage = (e) => {
      try {
        const evt: DeviceDetectionEvent = JSON.parse(e.data);
        setDetections(evt.detections);
        setInferenceMs(evt.inference_ms);
        if (evt.frame_width && evt.frame_height) {
          setFrameSize({ w: evt.frame_width, h: evt.frame_height });
        }
      } catch {
        // ignore
      }
    };
    es.onerror = () => {
      setDetections([]);
    };
    return () => es.close();
  }, []);

  // Draw bounding boxes on canvas overlay.
  useEffect(() => {
    const canvas = canvasRef.current;
    const img = imgRef.current;
    if (!canvas || !img) return;

    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const displayW = img.clientWidth;
    const displayH = img.clientHeight;
    canvas.width = displayW;
    canvas.height = displayH;
    ctx.clearRect(0, 0, displayW, displayH);

    if (!detections.length || !frameSize.w) return;

    const scaleX = displayW / frameSize.w;
    const scaleY = displayH / frameSize.h;

    for (const det of detections) {
      const [x1, y1, x2, y2] = det.bbox;
      const dx = x1 * scaleX;
      const dy = y1 * scaleY;
      const dw = (x2 - x1) * scaleX;
      const dh = (y2 - y1) * scaleY;
      const color = labelColor(det.label);

      ctx.strokeStyle = color;
      ctx.lineWidth = 2;
      ctx.strokeRect(dx, dy, dw, dh);

      const label = `${det.label} ${(det.confidence * 100).toFixed(0)}%`;
      ctx.font = "bold 13px sans-serif";
      const metrics = ctx.measureText(label);
      const labelH = 18;
      ctx.fillStyle = color;
      ctx.fillRect(dx, dy - labelH, metrics.width + 8, labelH);
      ctx.fillStyle = "#fff";
      ctx.fillText(label, dx + 4, dy - 4);
    }
  }, [detections, frameSize]);

  return (
    <div>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 12,
          marginBottom: 20,
        }}
      >
        <Title level={3} style={{ margin: 0 }}>
          Device Camera
        </Title>
        <Tag
          icon={connected ? <LinkOutlined /> : <DisconnectOutlined />}
          color={connected ? "success" : "error"}
        >
          {connected ? "Connected" : "Disconnected"}
        </Tag>
        {inferenceMs > 0 && (
          <Tag color="blue">{inferenceMs.toFixed(0)}ms inference</Tag>
        )}
        <Switch
          checked={visionEnabled}
          onChange={(v) => {
            setVisionEnabled(v);
            deviceVisionApi.set(v);
          }}
          checkedChildren="Vision ON"
          unCheckedChildren="Vision OFF"
        />
        <SoundOutlined style={{ marginLeft: 16 }} />
        <Slider
          min={0}
          max={100}
          value={volume}
          onChange={(v) => setVolume(v)}
          onChangeComplete={(v) => deviceVolumeApi.set(v)}
          style={{ width: 120, margin: 0 }}
          tooltip={{ formatter: (v) => `${v}%` }}
        />
      </div>

      <div style={{ display: "flex", gap: 16, flexWrap: "wrap" }}>
        <Card
          style={{ flex: "1 1 640px", minWidth: 320 }}
          styles={{ body: { padding: 0 } }}
        >
          {hasFrame ? (
            <div style={{ position: "relative", display: "inline-block", width: "100%" }}>
              <img
                ref={imgRef}
                onLoad={onImgLoad}
                onError={onImgError}
                alt="Device camera"
                style={{
                  width: "100%",
                  display: "block",
                  borderRadius: 8,
                  background: "#000",
                }}
              />
              <canvas
                ref={canvasRef}
                style={{
                  position: "absolute",
                  top: 0,
                  left: 0,
                  width: "100%",
                  height: "100%",
                  pointerEvents: "none",
                }}
              />
            </div>
          ) : (
            <div style={{ padding: 48 }}>
              <Empty
                image={<CameraOutlined style={{ fontSize: 48, color: "rgba(255,255,255,0.2)" }} />}
                description="No camera frame available"
              />
              {/* Hidden img for polling */}
              <img
                ref={imgRef}
                onLoad={onImgLoad}
                onError={onImgError}
                alt=""
                style={{ display: "none" }}
              />
            </div>
          )}
        </Card>

        <Card
          title={
            <Space>
              <CameraOutlined />
              <span>Detections</span>
              <Tag>{detections.length}</Tag>
            </Space>
          }
          style={{ flex: "0 0 280px", minWidth: 240 }}
        >
          {detections.length > 0 ? (
            <List
              size="small"
              dataSource={detections}
              renderItem={(det) => (
                <List.Item style={{ padding: "6px 0" }}>
                  <div
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: 8,
                      width: "100%",
                    }}
                  >
                    <div
                      style={{
                        width: 10,
                        height: 10,
                        borderRadius: "50%",
                        background: labelColor(det.label),
                        flexShrink: 0,
                      }}
                    />
                    <Text style={{ flex: 1 }}>{det.label}</Text>
                    <Text type="secondary">
                      {(det.confidence * 100).toFixed(1)}%
                    </Text>
                  </div>
                </List.Item>
              )}
            />
          ) : (
            <Text type="secondary">No objects detected</Text>
          )}
        </Card>
      </div>
    </div>
  );
}
