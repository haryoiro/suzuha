interface VoiceButtonProps {
  active: boolean;
  onToggle: () => void;
  disabled?: boolean;
}

// Nothing-style: minimal outline circle, 1.5px stroke, monochrome.
// Active = filled ring (opacity cue), inactive = thin outline.
export default function VoiceButton({ active, onToggle, disabled }: VoiceButtonProps) {
  return (
    <button
      onClick={onToggle}
      disabled={disabled}
      aria-label={active ? "MUTE MIC" : "START MIC"}
      style={{
        width: 72,
        height: 72,
        borderRadius: "50%",
        border: "1.5px solid",
        borderColor: disabled ? "var(--fg-dim)" : active ? "var(--fg-primary)" : "var(--fg-secondary)",
        background: "transparent",
        color: disabled ? "var(--fg-dim)" : active ? "var(--fg-primary)" : "var(--fg-secondary)",
        cursor: disabled ? "not-allowed" : "pointer",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        transition: "opacity 200ms ease-out, border-color 200ms ease-out, color 200ms ease-out",
        opacity: disabled ? 0.4 : 1,
        padding: 0,
      }}
    >
      <svg
        width="28"
        height="28"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        {active ? (
          <>
            <rect x="9" y="3" width="6" height="12" rx="3" />
            <path d="M19 11v1a7 7 0 0 1-14 0v-1" />
            <line x1="12" y1="19" x2="12" y2="22" />
          </>
        ) : (
          <>
            <path d="M9 9v2a3 3 0 0 0 5.12 2.12" />
            <path d="M15 9V6a3 3 0 0 0-5.94-.6" />
            <path d="M17 16.95A7 7 0 0 1 5 12v-1" />
            <path d="M19 11v1a7 7 0 0 1-.11 1.23" />
            <line x1="12" y1="19" x2="12" y2="22" />
            <line x1="2" y1="2" x2="22" y2="22" />
          </>
        )}
      </svg>
    </button>
  );
}
