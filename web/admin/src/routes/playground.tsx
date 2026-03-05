import { memo, useState, useRef, useEffect, useCallback } from "react";
import { Button, Spin, Typography, Tag } from "antd";
import { SendOutlined, ClearOutlined, RobotOutlined, UserOutlined } from "@ant-design/icons";
import { useMutation, useQuery } from "@tanstack/react-query";
import { playgroundApi, identityApi, usersApi } from "../lib/api";
import type { PlaygroundResponse, User } from "../lib/api";

const { Text } = Typography;

const STORAGE_KEY = "suzuha-playground-messages";

interface ChatMessage {
  role: "user" | "assistant";
  content: string;
  reasoning?: string;
  usage?: PlaygroundResponse["usage"];
  elapsed_ms?: number;
  tok_per_sec?: number;
  context_messages?: number;
}

function loadMessages(): ChatMessage[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    return raw ? JSON.parse(raw) : [];
  } catch {
    return [];
  }
}

function saveMessages(msgs: ChatMessage[]) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(msgs));
  } catch { /* quota exceeded — ignore */ }
}

const MessageItem = memo(function MessageItem({
  msg,
  botName,
}: {
  msg: ChatMessage;
  botName?: string;
}) {
  const [showReasoning, setShowReasoning] = useState(false);
  const isUser = msg.role === "user";

  return (
    <div style={{ padding: "20px 0", borderBottom: "1px solid rgba(255,255,255,0.06)" }}>
      <div style={{ maxWidth: 720, margin: "0 auto", display: "flex", gap: 16 }}>
        <div
          style={{
            width: 32,
            height: 32,
            borderRadius: "50%",
            background: isUser ? "rgba(6,182,212,0.2)" : "rgba(139,92,246,0.2)",
            border: `1px solid ${isUser ? "rgba(6,182,212,0.4)" : "rgba(139,92,246,0.4)"}`,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            flexShrink: 0,
            fontSize: 14,
            color: isUser ? "#06b6d4" : "#8b5cf6",
          }}
        >
          {isUser ? <UserOutlined /> : <RobotOutlined />}
        </div>

        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ fontWeight: 600, fontSize: 13, marginBottom: 6, color: "rgba(255,255,255,0.9)" }}>
            {isUser ? "You" : (botName || "Assistant")}
          </div>

          {msg.reasoning && (
            <div style={{ marginBottom: 8, borderLeft: "2px solid rgba(139,92,246,0.3)", paddingLeft: 12 }}>
              <div
                onClick={() => setShowReasoning(!showReasoning)}
                style={{
                  cursor: "pointer",
                  userSelect: "none",
                  fontSize: 12,
                  color: "rgba(139,92,246,0.7)",
                  display: "flex",
                  alignItems: "center",
                  gap: 4,
                }}
              >
                <span style={{ fontSize: 10 }}>{showReasoning ? "▼" : "▶"}</span>
                Thinking ({msg.reasoning.length} chars)
              </div>
              {showReasoning && (
                <pre
                  style={{
                    margin: "6px 0 0",
                    padding: 10,
                    background: "rgba(139,92,246,0.06)",
                    borderRadius: 6,
                    fontSize: 12,
                    lineHeight: 1.6,
                    whiteSpace: "pre-wrap",
                    wordBreak: "break-word",
                    color: "rgba(255,255,255,0.5)",
                    maxHeight: 300,
                    overflow: "auto",
                  }}
                >
                  {msg.reasoning}
                </pre>
              )}
            </div>
          )}

          <div
            style={{
              fontSize: 14,
              lineHeight: 1.7,
              whiteSpace: "pre-wrap",
              wordBreak: "break-word",
              color: "rgba(255,255,255,0.85)",
            }}
          >
            {msg.content || <Text type="secondary" italic>(empty response)</Text>}
          </div>

          {!isUser && (msg.usage || msg.elapsed_ms != null) && (
            <div
              style={{
                marginTop: 8,
                display: "flex",
                gap: 12,
                flexWrap: "wrap",
                fontSize: 11,
                color: "rgba(255,255,255,0.35)",
              }}
            >
              {msg.elapsed_ms != null && (
                <span>{msg.elapsed_ms >= 1000 ? `${(msg.elapsed_ms / 1000).toFixed(1)}s` : `${msg.elapsed_ms}ms`}</span>
              )}
              {msg.tok_per_sec != null && msg.tok_per_sec > 0 && <span>{msg.tok_per_sec.toFixed(1)} tok/s</span>}
              {msg.usage && <span>{msg.usage.prompt_tokens} in / {msg.usage.completion_tokens} out</span>}
              {msg.context_messages != null && <span>{msg.context_messages} ctx msgs</span>}
            </div>
          )}
        </div>
      </div>
    </div>
  );
});

function IdentityBar({ botName, owner }: { botName?: string; owner?: User }) {
  return (
    <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
      {botName && (
        <Tag color="purple" style={{ margin: 0 }}>
          <RobotOutlined style={{ marginRight: 4 }} />
          {botName}
        </Tag>
      )}
      {owner && (
        <Tag color="cyan" style={{ margin: 0 }}>
          <UserOutlined style={{ marginRight: 4 }} />
          {owner.display_name} (owner)
        </Tag>
      )}
    </div>
  );
}

export const PlaygroundPage = memo(function PlaygroundPage() {
  const [messages, setMessages] = useState<ChatMessage[]>(loadMessages);
  const [input, setInput] = useState("");
  const scrollRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  // Fetch bot identity.
  const { data: identity } = useQuery({
    queryKey: ["identity"],
    queryFn: identityApi.get,
    staleTime: Infinity,
  });

  // Fetch owner user (filter client-side since API has no role filter).
  const { data: usersData } = useQuery({
    queryKey: ["users", "all-for-owner"],
    queryFn: () => usersApi.list({ limit: 200, offset: 0 }),
    staleTime: Infinity,
  });
  const owner = usersData?.data?.find((u: User) => u.role === "owner");

  // Persist to localStorage on change.
  useEffect(() => {
    saveMessages(messages);
  }, [messages]);

  const mutation = useMutation({
    mutationFn: playgroundApi.send,
    onSuccess: (data) => {
      setMessages((prev) => [
        ...prev,
        {
          role: "assistant",
          content: data.text,
          reasoning: data.reasoning || undefined,
          usage: data.usage,
          elapsed_ms: data.elapsed_ms,
          tok_per_sec: data.tok_per_sec,
          context_messages: data.context_messages,
        },
      ]);
    },
    onError: (err) => {
      setMessages((prev) => [
        ...prev,
        { role: "assistant", content: `Error: ${err instanceof Error ? err.message : String(err)}` },
      ]);
    },
  });

  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight, behavior: "smooth" });
  }, [messages]);

  useEffect(() => {
    if (!mutation.isPending) inputRef.current?.focus();
  }, [mutation.isPending]);

  const handleSend = useCallback(() => {
    const text = input.trim();
    if (!text || mutation.isPending) return;
    setInput("");
    setMessages((prev) => [...prev, { role: "user", content: text }]);
    mutation.mutate(text);
  }, [input, mutation]);

  const handleClear = useCallback(() => {
    setMessages([]);
    setInput("");
    localStorage.removeItem(STORAGE_KEY);
  }, []);

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "calc(100vh - 88px)" }}>
      {/* Header */}
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 8, flexWrap: "wrap", gap: 8 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
          <h2 style={{ margin: 0 }}>Playground</h2>
          <IdentityBar botName={identity?.bot_name} owner={owner} />
        </div>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <Text type="secondary" style={{ fontSize: 11 }}>context snapshot (read-only)</Text>
          {messages.length > 0 && (
            <Button icon={<ClearOutlined />} size="small" onClick={handleClear} disabled={mutation.isPending}>
              Clear
            </Button>
          )}
        </div>
      </div>

      {/* Messages */}
      <div ref={scrollRef} style={{ flex: 1, overflow: "auto" }}>
        {messages.length === 0 ? (
          <div
            style={{
              display: "flex",
              flexDirection: "column",
              alignItems: "center",
              justifyContent: "center",
              height: "100%",
              color: "rgba(255,255,255,0.3)",
              gap: 12,
            }}
          >
            <RobotOutlined style={{ fontSize: 40 }} />
            <div style={{ fontSize: 15 }}>Playground</div>
            <div style={{ fontSize: 12, maxWidth: 400, textAlign: "center", lineHeight: 1.6 }}>
              Agent context snapshot + ephemeral (read-only)
            </div>
          </div>
        ) : (
          <>
            {messages.map((msg, i) => (
              <MessageItem key={i} msg={msg} botName={identity?.bot_name} />
            ))}
            {mutation.isPending && (
              <div style={{ padding: "20px 0" }}>
                <div style={{ maxWidth: 720, margin: "0 auto", display: "flex", gap: 16 }}>
                  <div
                    style={{
                      width: 32,
                      height: 32,
                      borderRadius: "50%",
                      background: "rgba(139,92,246,0.2)",
                      border: "1px solid rgba(139,92,246,0.4)",
                      display: "flex",
                      alignItems: "center",
                      justifyContent: "center",
                      flexShrink: 0,
                      fontSize: 14,
                      color: "#8b5cf6",
                    }}
                  >
                    <RobotOutlined />
                  </div>
                  <Spin size="small" style={{ marginTop: 8 }} />
                </div>
              </div>
            )}
          </>
        )}
      </div>

      {/* Input */}
      <div style={{ borderTop: "1px solid rgba(255,255,255,0.08)", padding: "16px 0 0" }}>
        <div
          style={{
            maxWidth: 720,
            margin: "0 auto",
            position: "relative",
            background: "rgba(255,255,255,0.04)",
            borderRadius: 12,
            border: "1px solid rgba(255,255,255,0.1)",
          }}
        >
          <textarea
            ref={inputRef}
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                handleSend();
              }
            }}
            placeholder="Message..."
            disabled={mutation.isPending}
            rows={1}
            style={{
              width: "100%",
              background: "transparent",
              border: "none",
              outline: "none",
              resize: "none",
              padding: "14px 52px 14px 16px",
              fontSize: 14,
              lineHeight: 1.5,
              color: "rgba(255,255,255,0.9)",
              fontFamily: "inherit",
              maxHeight: 150,
              overflow: "auto",
            }}
            onInput={(e) => {
              const el = e.currentTarget;
              el.style.height = "auto";
              el.style.height = Math.min(el.scrollHeight, 150) + "px";
            }}
          />
          <div style={{ position: "absolute", right: 8, bottom: 8, display: "flex", gap: 4 }}>
            <Button
              type="text"
              size="small"
              icon={<SendOutlined />}
              onClick={handleSend}
              loading={mutation.isPending}
              disabled={!input.trim()}
              style={{ color: input.trim() ? "#06b6d4" : "rgba(255,255,255,0.2)" }}
            />
          </div>
        </div>
        <div style={{ textAlign: "center", padding: "8px 0 4px", fontSize: 11, color: "rgba(255,255,255,0.2)" }}>
          Enter to send / Shift+Enter for newline
        </div>
      </div>
    </div>
  );
});

export default PlaygroundPage;
