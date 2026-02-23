import { useState, useEffect, useCallback, useRef } from "react";
import { connectLogStream, type LogEntry } from "../lib/api";

const MAX_ENTRIES = 1000;

export function useLogStream(params?: { level?: string; source?: string }) {
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [connected, setConnected] = useState(false);
  const seenSeqs = useRef(new Set<number>());

  const clear = useCallback(() => {
    setLogs([]);
    seenSeqs.current.clear();
  }, []);

  useEffect(() => {
    // Clear on filter change.
    setLogs([]);
    seenSeqs.current.clear();

    const es = connectLogStream(params);

    es.onopen = () => setConnected(true);

    es.onerror = () => {
      if (es.readyState === EventSource.CLOSED) {
        setConnected(false);
      }
    };

    es.onmessage = (event) => {
      try {
        const entry: LogEntry = JSON.parse(event.data);
        // Deduplicate by seq — upstream replays buffer on reconnect.
        if (seenSeqs.current.has(entry.seq)) return;
        seenSeqs.current.add(entry.seq);

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
