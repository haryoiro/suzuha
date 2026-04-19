import { useState, useCallback, useRef } from "react";
import Face from "./components/Face";
import VoiceButton from "./components/VoiceButton";
import { useWebSocket, type TranscriptEntry } from "./hooks/useWebSocket";
import { useAudio } from "./hooks/useAudio";
import { EXPRESSION_NAMES } from "./lib/face-params";
import "./App.css";

// Nothing-inspired design:
// - Monochrome palette (opacity hierarchy, no blues/greens for state)
// - Typography: Space Grotesk (body) + Space Mono (labels/caps)
// - Accent red only for errors / urgent / stop
// - Dot-matrix background motif as subtle identity cue
// - Ease-out opacity transitions, no bounce

export default function App() {
  const [expression, setExpression] = useState(0);
  const prevExpressionRef = useRef(0);
  const [transcript, setTranscript] = useState<TranscriptEntry[]>([]);
  const [thinking, setThinking] = useState(false);

  const enqueueTTSRef = useRef<(payload: ArrayBuffer) => void>(() => {});
  const endTTSRef = useRef<() => void>(() => {});

  const { connected, sendAudio, debugLog } = useWebSocket({
    onExpression: useCallback((expr: number) => {
      setExpression(expr);
    }, []),
    onTTS: useCallback((payload: ArrayBuffer) => {
      setThinking(false);
      enqueueTTSRef.current(payload);
    }, []),
    onTTSEnd: useCallback(() => {
      endTTSRef.current();
    }, []),
    onTranscript: useCallback((entry: TranscriptEntry) => {
      setTranscript((prev) => [...prev.slice(-19), entry]);
      if (entry.kind === "user") setThinking(true);
      else if (entry.kind === "bot") setThinking(false);
    }, []),
  });

  const { micActive, playing, voiceLevel, toggleMic, enqueueTTS, endTTS } = useAudio({
    onAudioChunk: sendAudio,
    onPlaybackStart: useCallback(() => {
      prevExpressionRef.current = expression;
      setExpression(7); // talking
    }, [expression]),
    onPlaybackEnd: useCallback(() => {
      setExpression(prevExpressionRef.current);
    }, []),
  });

  enqueueTTSRef.current = enqueueTTS;
  endTTSRef.current = endTTS;

  // State machine: single source of truth for UI state.
  type UiState = "OFFLINE" | "IDLE" | "LISTENING" | "HEARING" | "THINKING" | "SPEAKING";
  let state: UiState = "IDLE";
  if (!connected) state = "OFFLINE";
  else if (playing) state = "SPEAKING";
  else if (thinking) state = "THINKING";
  else if (micActive && voiceLevel > 0.15) state = "HEARING";
  else if (micActive) state = "LISTENING";
  else state = "IDLE";

  const latest = transcript[transcript.length - 1];

  return (
    <div
      style={{
        width: "100vw",
        height: "100dvh",
        display: "grid",
        gridTemplateRows: "auto 1fr auto",
        background: "var(--bg)",
        color: "var(--fg-primary)",
        userSelect: "none",
        // iOS safe-area (ノッチ / ホームインジケータ) の余白を確保。
        paddingBottom: "env(safe-area-inset-bottom)",
        paddingTop: "env(safe-area-inset-top)",
      }}
    >
      {/* ───── HEADER: Status bar + dot-matrix background ───── */}
      <header
        className="dot-matrix"
        style={{
          padding: "24px 24px 16px",
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          gap: 16,
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
          <StatusIndicator state={state} />
          <span
            className="mono"
            style={{
              fontSize: 11,
              color: "var(--fg-primary)",
              fontWeight: 500,
            }}
          >
            {state}
          </span>
        </div>

        <div style={{ display: "flex", alignItems: "center", gap: 16 }}>
          {micActive && (
            <span
              className="mono"
              style={{ fontSize: 11, color: "var(--fg-tertiary)" }}
            >
              LVL {String(Math.round(voiceLevel * 100)).padStart(2, "0")}
            </span>
          )}
          <span
            className="mono"
            style={{ fontSize: 11, color: "var(--fg-tertiary)" }}
          >
            {(EXPRESSION_NAMES[expression] ?? "?").toUpperCase()}
          </span>
        </div>
      </header>

      {/* ───── MAIN: Face + latest transcript ───── */}
      <main
        style={{
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          justifyContent: "center",
          gap: 32,
          padding: "0 24px",
          minHeight: 0,
        }}
      >
        <Face expression={expression} size={280} />

        {latest && (
          <div
            style={{
              maxWidth: 560,
              minHeight: 56,
              textAlign: "center",
              display: "flex",
              flexDirection: "column",
              gap: 8,
            }}
          >
            <div
              className="mono"
              style={{
                fontSize: 10,
                color: "var(--fg-tertiary)",
              }}
            >
              {latest.kind === "user" ? "YOU" : "NONO"}
            </div>
            <div
              style={{
                fontSize: 20,
                fontWeight: 400,
                lineHeight: 1.4,
                color: latest.kind === "user" ? "var(--accent-user)" : "var(--accent-bot)",
                wordBreak: "break-word",
              }}
            >
              {latest.text}
            </div>
          </div>
        )}
      </main>

      {/* ───── FOOTER: History + Voice button + debug ───── */}
      <footer
        style={{
          padding: "16px 24px 24px",
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          gap: 24,
          borderTop: "1px solid var(--fg-dim)",
        }}
      >
        {transcript.length > 1 && (
          <div
            style={{
              width: "100%",
              maxHeight: 96,
              overflowY: "auto",
              display: "flex",
              flexDirection: "column",
              gap: 4,
              pointerEvents: "none",
            }}
          >
            {transcript
              .slice(0, -1)
              .slice(-5)
              .map((entry, i) => (
                <div
                  key={i}
                  style={{
                    display: "flex",
                    gap: 12,
                    alignItems: "baseline",
                    fontSize: 13,
                    lineHeight: 1.5,
                    color: "var(--fg-tertiary)",
                  }}
                >
                  <span
                    className="mono"
                    style={{
                      fontSize: 9,
                      color: "var(--fg-dim)",
                      minWidth: 36,
                    }}
                  >
                    {entry.kind === "user" ? "YOU" : "NONO"}
                  </span>
                  <span style={{ flex: 1 }}>{entry.text}</span>
                </div>
              ))}
          </div>
        )}

        <VoiceButton active={micActive} onToggle={toggleMic} disabled={!connected} />

        {debugLog.length > 0 && (
          <details
            style={{
              width: "100%",
              maxHeight: 100,
            }}
          >
            <summary
              className="mono"
              style={{
                fontSize: 9,
                color: "var(--fg-dim)",
                cursor: "pointer",
                listStyle: "none",
              }}
            >
              LOG
            </summary>
            <div
              className="mono"
              style={{
                fontSize: 9,
                color: "var(--fg-dim)",
                lineHeight: 1.5,
                marginTop: 4,
                maxHeight: 80,
                overflowY: "auto",
              }}
            >
              {debugLog.map((line, i) => (
                <div key={i}>{line}</div>
              ))}
            </div>
          </details>
        )}
      </footer>
    </div>
  );
}

// Small dot indicator that reflects state via opacity/animation (no colors except red for error).
function StatusIndicator({ state }: { state: string }) {
  const isError = state === "OFFLINE";
  const isPulsing = state === "THINKING";
  const isActive = state === "HEARING" || state === "SPEAKING";
  return (
    <span
      style={{
        width: 10,
        height: 10,
        borderRadius: "50%",
        background: isError ? "var(--accent-red)" : "var(--fg-primary)",
        opacity: isError ? 1 : isActive ? 1 : 0.48,
        animation: isPulsing ? "pulse 1.2s ease-in-out infinite" : undefined,
        transition: "opacity 200ms ease-out",
      }}
    />
  );
}
