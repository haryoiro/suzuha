import { useState, useCallback, useRef } from "react";
import Face from "./components/Face";
import VoiceButton from "./components/VoiceButton";
import { useWebSocket } from "./hooks/useWebSocket";
import { useAudio } from "./hooks/useAudio";
import { EXPRESSION_NAMES } from "./lib/face-params";
import "./App.css";

export default function App() {
  const [expression, setExpression] = useState(0);
  const prevExpressionRef = useRef(0);

  // TTS enqueue function — initialized by useAudio, used by useWebSocket callback
  const enqueueTTSRef = useRef<(payload: ArrayBuffer) => void>(() => {});

  const { connected, sendAudio, debugLog } = useWebSocket({
    onExpression: useCallback((expr: number) => {
      setExpression(expr);
    }, []),
    onTTS: useCallback((payload: ArrayBuffer) => {
      enqueueTTSRef.current(payload);
    }, []),
  });

  const { micActive, playing, toggleMic, enqueueTTS } = useAudio({
    onAudioChunk: sendAudio,
    onPlaybackStart: useCallback(() => {
      prevExpressionRef.current = expression;
      setExpression(7); // talking
    }, [expression]),
    onPlaybackEnd: useCallback(() => {
      setExpression(prevExpressionRef.current);
    }, []),
  });

  // Wire the ref so WebSocket TTS callbacks reach useAudio
  enqueueTTSRef.current = enqueueTTS;

  return (
    <div
      style={{
        width: "100vw",
        height: "100vh",
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        background: "#000",
        gap: 24,
        userSelect: "none",
      }}
    >
      {/* Status bar */}
      <div
        style={{
          position: "absolute",
          top: 12,
          left: 0,
          right: 0,
          display: "flex",
          justifyContent: "center",
          gap: 16,
          fontSize: 12,
          color: "#666",
        }}
      >
        <span style={{ color: connected ? "#4c8" : "#c44" }}>
          {connected ? "connected" : "disconnected"}
        </span>
        <span>{EXPRESSION_NAMES[expression] ?? "unknown"}</span>
        {playing && <span style={{ color: "#4cf" }}>speaking...</span>}
      </div>

      {/* Face */}
      <Face expression={expression} size={320} />

      {/* Voice button */}
      <VoiceButton active={micActive} onToggle={toggleMic} disabled={!connected} />

      {/* Debug log */}
      <div
        style={{
          position: "absolute",
          bottom: 8,
          left: 8,
          right: 8,
          fontSize: 10,
          color: "#555",
          fontFamily: "monospace",
          lineHeight: 1.4,
          maxHeight: 120,
          overflow: "auto",
        }}
      >
        {debugLog.map((line, i) => (
          <div key={i}>{line}</div>
        ))}
      </div>
    </div>
  );
}
