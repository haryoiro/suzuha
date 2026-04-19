import { useRef, useState, useEffect, useCallback } from "react";
import {
  decodeFrame,
  decodeCommand,
  FRAME_COMMAND,
  FRAME_TTS,
  FRAME_TTS_END,
  encodeAudioFrame,
} from "../lib/protocol";

export interface TranscriptEntry {
  kind: "user" | "bot";
  text: string;
  ts: number; // Date.now()
}

export interface WebSocketCallbacks {
  onExpression?: (expression: number) => void;
  onTTS?: (pcm: ArrayBuffer) => void;
  onTTSEnd?: () => void;
  onTranscript?: (entry: TranscriptEntry) => void;
}

export function useWebSocket(callbacks: WebSocketCallbacks) {
  const [connected, setConnected] = useState(false);
  const [debugLog, setDebugLog] = useState<string[]>([]);
  const wsRef = useRef<WebSocket | null>(null);
  const callbacksRef = useRef(callbacks);
  callbacksRef.current = callbacks;

  const reconnectDelayRef = useRef(1000);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  // unmount / HMR 時に cleanup から立てられる。これが true になったらもう reconnect しない。
  const abortedRef = useRef(false);

  const log = useCallback((msg: string) => {
    setDebugLog((prev) => [...prev.slice(-9), `${new Date().toLocaleTimeString()} ${msg}`]);
  }, []);

  useEffect(() => {
    abortedRef.current = false;

    const connect = () => {
      if (abortedRef.current) return;

      // 既存 ws が残ってたら先に close (多重接続防止)。
      if (wsRef.current) {
        try {
          wsRef.current.onclose = null;
          wsRef.current.close();
        } catch {
          // ignore
        }
        wsRef.current = null;
      }

      const proto = location.protocol === "https:" ? "wss:" : "ws:";
      const url = `${proto}//${location.host}/ws/web`;
      log(`connecting: ${url}`);

      try {
        const ws = new WebSocket(url);
        ws.binaryType = "arraybuffer";
        wsRef.current = ws;

        ws.onopen = () => {
          if (wsRef.current !== ws) {
            // 既に別の ws が代替してる (古いハンドラ発火)。捨てる。
            try {
              ws.close();
            } catch {
              // ignore
            }
            return;
          }
          log("connected!");
          setConnected(true);
          reconnectDelayRef.current = 1000;
        };

        ws.onclose = (ev) => {
          if (wsRef.current === ws) {
            wsRef.current = null;
            setConnected(false);
          }
          log(`closed: code=${ev.code} reason=${ev.reason || "(none)"}`);
          if (abortedRef.current) return;
          const delay = reconnectDelayRef.current;
          reconnectDelayRef.current = Math.min(delay * 2, 30000);
          // 既存の再接続 timer をキャンセルして 1 本化。
          if (reconnectTimerRef.current) clearTimeout(reconnectTimerRef.current);
          reconnectTimerRef.current = setTimeout(() => {
            reconnectTimerRef.current = null;
            connect();
          }, delay);
        };

        ws.onerror = () => {
          log("error (see close event for details)");
          try {
            ws.close();
          } catch {
            // ignore
          }
        };

        ws.onmessage = (ev: MessageEvent) => {
          // このハンドラは ws 固有。wsRef が別のものを指してたら古い ws のメッセージ → 捨てる。
          if (wsRef.current !== ws) return;
          if (!(ev.data instanceof ArrayBuffer) || ev.data.byteLength < 1) return;

          const frame = decodeFrame(ev.data);

          switch (frame.type) {
            case FRAME_COMMAND: {
              const cmd = decodeCommand(frame.payload);
              if (cmd.cmd === "face" && typeof cmd.expression === "number") {
                callbacksRef.current.onExpression?.(cmd.expression);
              } else if (
                cmd.cmd === "transcript" &&
                (cmd.kind === "user" || cmd.kind === "bot") &&
                typeof cmd.text === "string"
              ) {
                callbacksRef.current.onTranscript?.({
                  kind: cmd.kind,
                  text: cmd.text,
                  ts: Date.now(),
                });
              }
              break;
            }
            case FRAME_TTS: {
              callbacksRef.current.onTTS?.(frame.payload);
              break;
            }
            case FRAME_TTS_END: {
              callbacksRef.current.onTTSEnd?.();
              break;
            }
          }
        };
      } catch (e) {
        log(`exception: ${e}`);
      }
    };

    connect();

    return () => {
      abortedRef.current = true;
      if (reconnectTimerRef.current) {
        clearTimeout(reconnectTimerRef.current);
        reconnectTimerRef.current = null;
      }
      if (wsRef.current) {
        try {
          wsRef.current.close();
        } catch {
          // ignore
        }
        wsRef.current = null;
      }
      setConnected(false);
    };
  }, [log]);

  const sendAudio = useCallback((pcm: Int16Array) => {
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(encodeAudioFrame(pcm));
    }
  }, []);

  return { connected, sendAudio, debugLog };
}
