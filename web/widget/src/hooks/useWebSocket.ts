import { useRef, useState, useEffect, useCallback } from "react";
import {
  decodeFrame,
  decodeCommand,
  FRAME_COMMAND,
  FRAME_TTS,
  encodeAudioFrame,
} from "../lib/protocol";

export interface WebSocketCallbacks {
  onExpression?: (expression: number) => void;
  onTTS?: (pcm: ArrayBuffer) => void;
}

export function useWebSocket(callbacks: WebSocketCallbacks) {
  const [connected, setConnected] = useState(false);
  const [debugLog, setDebugLog] = useState<string[]>([]);
  const wsRef = useRef<WebSocket | null>(null);
  const callbacksRef = useRef(callbacks);
  callbacksRef.current = callbacks;
  const reconnectDelay = useRef(1000);

  const log = useCallback((msg: string) => {
    setDebugLog((prev) => [...prev.slice(-9), `${new Date().toLocaleTimeString()} ${msg}`]);
  }, []);

  const connect = useCallback(() => {
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const url = `${proto}//${location.host}/ws/web`;
    log(`connecting: ${url}`);

    try {
      const ws = new WebSocket(url);
      ws.binaryType = "arraybuffer";
      wsRef.current = ws;

      ws.onopen = () => {
        log("connected!");
        setConnected(true);
        reconnectDelay.current = 1000;
      };

      ws.onclose = (ev) => {
        log(`closed: code=${ev.code} reason=${ev.reason || "(none)"}`);
        setConnected(false);
        wsRef.current = null;
        const delay = reconnectDelay.current;
        reconnectDelay.current = Math.min(delay * 2, 30000);
        setTimeout(connect, delay);
      };

      ws.onerror = () => {
        log("error (see close event for details)");
        ws.close();
      };

      ws.onmessage = (ev: MessageEvent) => {
        if (!(ev.data instanceof ArrayBuffer) || ev.data.byteLength < 2) return;

        const frame = decodeFrame(ev.data);

        switch (frame.type) {
          case FRAME_COMMAND: {
            const cmd = decodeCommand(frame.payload);
            if (cmd.cmd === "face" && typeof cmd.expression === "number") {
              callbacksRef.current.onExpression?.(cmd.expression);
            }
            break;
          }
          case FRAME_TTS: {
            callbacksRef.current.onTTS?.(frame.payload);
            break;
          }
        }
      };
    } catch (e) {
      log(`exception: ${e}`);
    }
  }, [log]);

  useEffect(() => {
    connect();
    return () => {
      wsRef.current?.close();
    };
  }, [connect]);

  const sendAudio = useCallback((pcm: Int16Array) => {
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(encodeAudioFrame(pcm));
    }
  }, []);

  return { connected, sendAudio, debugLog };
}
