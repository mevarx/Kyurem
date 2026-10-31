import { useState } from "react";

/** Mono id text with a hover-reveal copy button. */
export function CopyButton({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);

  const copy = async (event: React.MouseEvent) => {
    event.stopPropagation();
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1400);
    } catch {
      // Clipboard unavailable; the id text itself remains selectable.
    }
  };

  return (
    <button
      type="button"
      className="copy-button"
      onClick={copy}
      title={copied ? "Copied" : `Copy ${value}`}
      aria-label={copied ? "Copied" : `Copy ${value}`}
    >
        {copied ? (
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="M20 6 9 17l-5-5" />
          </svg>
        ) : (
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <rect width="14" height="14" x="8" y="8" rx="2" />
            <path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2" />
          </svg>
        )}
      </button>
  );
}

/** Deterministic initial avatar for an agent name. */
export function AgentAvatar({ name }: { name: string }) {
  const initials = name
    .split(/[-_\s]+/)
    .map((part) => part[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();
  return (
    <span className="avatar" aria-hidden="true">
      {initials || "?"}
    </span>
  );
}
