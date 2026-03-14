import { useState, useEffect, useMemo } from "react";
import { Card, Typography, Tag, Input, List, Button, message } from "antd";
import { SoundOutlined, CheckCircleOutlined } from "@ant-design/icons";
import { voicevoxApi, type VoicevoxSpeaker } from "../lib/api";

const { Title } = Typography;

export default function VoicePage() {
  const [speakers, setSpeakers] = useState<VoicevoxSpeaker[]>([]);
  const [currentId, setCurrentId] = useState<number>(-1);
  const [search, setSearch] = useState("");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    Promise.all([voicevoxApi.speakers(), voicevoxApi.currentSpeaker()])
      .then(([sp, cur]) => {
        setSpeakers(sp);
        setCurrentId(cur.speaker_id);
      })
      .finally(() => setLoading(false));
  }, []);

  const filtered = useMemo(() => {
    if (!search) return speakers;
    const q = search.toLowerCase();
    return speakers.filter(
      (s) =>
        s.name.toLowerCase().includes(q) ||
        s.styles.some((st) => st.name.toLowerCase().includes(q))
    );
  }, [speakers, search]);

  const handleSelect = async (styleId: number) => {
    try {
      await voicevoxApi.setSpeaker(styleId);
      setCurrentId(styleId);
      message.success("Voice changed");
    } catch {
      message.error("Failed to change voice");
    }
  };

  // Find current speaker name.
  const currentLabel = useMemo(() => {
    for (const s of speakers) {
      for (const st of s.styles) {
        if (st.id === currentId) return `${s.name} - ${st.name}`;
      }
    }
    return `ID: ${currentId}`;
  }, [speakers, currentId]);

  return (
    <div>
      <div style={{ display: "flex", alignItems: "center", gap: 12, marginBottom: 20 }}>
        <Title level={3} style={{ margin: 0 }}>Voice</Title>
        <Tag icon={<SoundOutlined />} color="cyan">{currentLabel}</Tag>
      </div>

      <Input
        placeholder="Search speakers..."
        value={search}
        onChange={(e) => setSearch(e.target.value)}
        style={{ marginBottom: 16, maxWidth: 400 }}
        allowClear
      />

      <List
        loading={loading}
        grid={{ gutter: 12, xs: 1, sm: 2, md: 3, lg: 3, xl: 4 }}
        dataSource={filtered}
        renderItem={(speaker) => (
          <List.Item>
            <Card
              size="small"
              title={speaker.name}
              style={{ height: "100%" }}
            >
              <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
                {speaker.styles.map((style) => {
                  const isActive = style.id === currentId;
                  return (
                    <Button
                      key={style.id}
                      size="small"
                      type={isActive ? "primary" : "default"}
                      icon={isActive ? <CheckCircleOutlined /> : undefined}
                      onClick={() => handleSelect(style.id)}
                    >
                      {style.name}
                    </Button>
                  );
                })}
              </div>
            </Card>
          </List.Item>
        )}
      />
    </div>
  );
}
