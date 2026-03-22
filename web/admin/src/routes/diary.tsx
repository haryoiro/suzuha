import { memo, useState } from "react";
import { Spin, Empty, Button, DatePicker, Tag, Card } from "antd";
import { LeftOutlined, RightOutlined } from "@ant-design/icons";
import dayjs from "dayjs";
import { useDiary, useDiaryDates } from "../hooks/useDiary";

function formatHourLabel(hour: string): string {
  // "2026-03-22T15:00" → "15:00"
  const t = hour.split("T")[1];
  return t ?? hour;
}

const DiaryPage = memo(function DiaryPage() {
  const [date, setDate] = useState(() => dayjs().format("YYYY-MM-DD"));
  const { data, isLoading } = useDiary(date);
  const { data: diaryDates } = useDiaryDates();

  const prevDay = () => setDate(dayjs(date).subtract(1, "day").format("YYYY-MM-DD"));
  const nextDay = () => setDate(dayjs(date).add(1, "day").format("YYYY-MM-DD"));
  const isToday = date === dayjs().format("YYYY-MM-DD");

  // Highlight dates that have diary entries in the date picker.
  const dateSet = new Set(diaryDates ?? []);

  return (
    <div style={{ maxWidth: 720, margin: "0 auto" }}>
      {/* Header */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 8,
          marginBottom: 20,
          flexWrap: "wrap",
        }}
      >
        <h2 style={{ margin: 0 }}>Diary</h2>
        <div style={{ flex: 1 }} />
        <Button icon={<LeftOutlined />} size="small" onClick={prevDay} />
        <DatePicker
          value={dayjs(date)}
          onChange={(d) => d && setDate(d.format("YYYY-MM-DD"))}
          allowClear={false}
          size="small"
          cellRender={(current) => {
            const d = dayjs(current).format("YYYY-MM-DD");
            const has = dateSet.has(d);
            return (
              <div className="ant-picker-cell-inner" style={{ position: "relative" }}>
                {dayjs(current).date()}
                {has && (
                  <div
                    style={{
                      position: "absolute",
                      bottom: 2,
                      left: "50%",
                      transform: "translateX(-50%)",
                      width: 4,
                      height: 4,
                      borderRadius: "50%",
                      background: "#06b6d4",
                    }}
                  />
                )}
              </div>
            );
          }}
        />
        <Button
          icon={<RightOutlined />}
          size="small"
          onClick={nextDay}
          disabled={isToday}
        />
      </div>

      {isLoading && (
        <div style={{ textAlign: "center", padding: 48 }}>
          <Spin />
        </div>
      )}

      {!isLoading && data && (
        <>
          {/* Daily Summary */}
          {data.daily ? (
            <Card
              size="small"
              style={{
                marginBottom: 16,
                background: "rgba(6,182,212,0.06)",
                border: "1px solid rgba(6,182,212,0.2)",
              }}
            >
              <div style={{ marginBottom: 6, display: "flex", alignItems: "center", gap: 6 }}>
                <Tag color="cyan">Daily</Tag>
                <span style={{ fontSize: 12, color: "rgba(255,255,255,0.4)" }}>
                  {data.daily.date}
                </span>
              </div>
              <pre
                style={{
                  margin: 0,
                  fontFamily: "inherit",
                  fontSize: 13,
                  whiteSpace: "pre-wrap",
                  wordBreak: "break-word",
                  color: "rgba(255,255,255,0.85)",
                  lineHeight: 1.6,
                }}
              >
                {data.daily.content}
              </pre>
            </Card>
          ) : (
            <Card
              size="small"
              style={{
                marginBottom: 16,
                background: "rgba(255,255,255,0.02)",
                border: "1px solid rgba(255,255,255,0.06)",
              }}
            >
              <span style={{ color: "rgba(255,255,255,0.3)", fontSize: 13 }}>
                No daily summary yet
              </span>
            </Card>
          )}

          {/* Hourly Timeline */}
          {data.hourly.length > 0 ? (
            <div style={{ position: "relative", paddingLeft: 20 }}>
              {/* Vertical line */}
              <div
                style={{
                  position: "absolute",
                  left: 7,
                  top: 0,
                  bottom: 0,
                  width: 2,
                  background: "rgba(255,255,255,0.08)",
                  borderRadius: 1,
                }}
              />

              {data.hourly.map((h) => (
                <div
                  key={h.id}
                  style={{
                    position: "relative",
                    marginBottom: 12,
                  }}
                >
                  {/* Dot */}
                  <div
                    style={{
                      position: "absolute",
                      left: -17,
                      top: 6,
                      width: 8,
                      height: 8,
                      borderRadius: "50%",
                      background: "#06b6d4",
                      border: "2px solid #0b1120",
                    }}
                  />

                  <div
                    style={{
                      padding: "8px 12px",
                      background: "rgba(255,255,255,0.03)",
                      borderRadius: 6,
                      border: "1px solid rgba(255,255,255,0.06)",
                    }}
                  >
                    <div
                      style={{
                        display: "flex",
                        alignItems: "center",
                        gap: 6,
                        marginBottom: 4,
                      }}
                    >
                      <Tag
                        style={{
                          margin: 0,
                          fontSize: 12,
                          fontFamily: "monospace",
                          background: "rgba(255,255,255,0.06)",
                          border: "none",
                          color: "rgba(255,255,255,0.7)",
                        }}
                      >
                        {formatHourLabel(h.hour)}
                      </Tag>
                    </div>
                    <pre
                      style={{
                        margin: 0,
                        fontFamily: "inherit",
                        fontSize: 13,
                        whiteSpace: "pre-wrap",
                        wordBreak: "break-word",
                        color: "rgba(255,255,255,0.75)",
                        lineHeight: 1.5,
                      }}
                    >
                      {h.content}
                    </pre>
                  </div>
                </div>
              ))}
            </div>
          ) : (
            !data.daily && (
              <Empty
                description="No diary entries for this day"
                style={{ padding: 32 }}
              />
            )
          )}
        </>
      )}
    </div>
  );
});

export default DiaryPage;
