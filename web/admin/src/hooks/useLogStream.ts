import { useState, useEffect, useCallback } from "react";
import { connectLogStream, type LogEntry } from "../lib/api";

const MAX_ENTRIES = 1000;

export function useLogStream(params?: { level?: string; source?: string }) {
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [connected, setConnected] = useState(false);

  const clear = useCallback(() => setLogs([]), []);

  useEffect(() => {
    const es = connectLogStream(params);

    es.onopen = () => setConnected(true);
    es.onerror = () => setConnected(false);

    es.onmessage = (event) => {
      try {
        const entry: LogEntry = JSON.parse(event.data);
        setLogs((prev) => {
          const next = [...prev, entry];
          return next.length > MAX_ENTRIES ? next.slice(-MAX_ENTRIES) : next;
        });
      } catch {
        // ignore parse errors
      }
    };

    return () => {
      es.close();
      setConnected(false);
    };
  }, [params?.level, params?.source]);

  return { logs, connected, clear };
}
