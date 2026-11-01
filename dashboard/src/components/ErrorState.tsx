import { useState } from "react";
import { Link } from "react-router-dom";
import { ApiError, getApiBaseUrl } from "../api/client";
import { errorMessage } from "../lib/format";
import { ConnectionLost, type ConnectionPhase } from "./connection-lost";

export type ErrorStateVariant = "gateway" | "app";

export interface ErrorStateProps {
  error: unknown;
  /** Optional retry handler; a retry button is rendered when present. */
  onRetry?: () => void;
  title?: string;
  /**
   * gateway (default): unreachable panel — retry countdown/backoff
   * (connection-lost) plus an unplugged illustration, mono detail, Try again.
   * app: generic failure panel — copyable details plus a report action
   * (something-went-wrong pattern).
   */
  variant?: ErrorStateVariant;
  /** Body copy under the title. */
  description?: string;
  /** Gateway retry backoff steps in seconds (connection-lost schedule). */
  retryDelays?: number[];
  /** Hint for the reconnect banner, kept for parity with connection-lost. */
  pendingChanges?: number;
  /** Override the reconnect probe; defaults to firing `onRetry`. */
  checkConnection?: () => Promise<boolean>;
  /** Gateway reachability flag driving the reconnect banner. */
  online?: boolean;
  onPhaseChange?: (phase: ConnectionPhase) => void;
  /** App-variant error reference, shown, copied and reported. */
  errorId?: string;
  /** App-variant request context (route, operation) shown in the details. */
  request?: string;
  onReport?: (errorId: string) => void;
  /** Report link; preferred over `onReport` when both are set. */
  reportHref?: string;
  /** Destination of the app-variant dashboard action. */
  dashboardHref?: string;
}

/** Static unplugged-cable illustration (unplugged pattern): token strokes only, no motion. */
function UnpluggedScene() {
  return (
    <svg viewBox="0 0 160 72" width="148" height="66" aria-hidden="true" focusable="false">
      {/* wall cable into the socket */}
      <path
        d="M160 36 H148"
        fill="none"
        stroke="var(--border-strong)"
        strokeLinecap="round"
        strokeWidth="5"
      />
      {/* socket */}
      <rect x="126" y="22" width="22" height="28" rx="6" fill="var(--surface-raised)" stroke="var(--border-strong)" strokeWidth="1.5" />
      <rect x="131" y="31" width="4" height="10" rx="2" fill="var(--muted-foreground)" />
      <rect x="139" y="31" width="4" height="10" rx="2" fill="var(--muted-foreground)" />
      {/* plug, pulled clear of the socket */}
      <rect x="80" y="24" width="26" height="24" rx="6" fill="var(--warning-bg)" stroke="var(--warning)" strokeWidth="1.5" />
      <path d="M106 31 H118 M106 41 H118" stroke="var(--warning)" strokeLinecap="round" strokeWidth="2.5" />
      {/* spark ticks in the gap */}
      <path d="M121 28 l3 -4 M121 44 l3 4" stroke="var(--warning)" strokeLinecap="round" strokeWidth="1.5" />
      {/* loose cable off the plug */}
      <path
        d="M80 36 C 62 36 56 54 34 54 C 22 54 14 49 4 50"
        fill="none"
        stroke="var(--muted-foreground)"
        strokeLinecap="round"
        strokeWidth="5"
      />
    </svg>
  );
}

/** Gateway-unreachable panel: reconnect countdown/backoff, mono detail, Try again. */
function GatewayError({
  error,
  onRetry,
  heading,
  retryDelays,
  pendingChanges,
  checkConnection,
  online,
  onPhaseChange,
}: {
  error: unknown;
  onRetry?: () => void;
  heading: string;
  retryDelays?: number[];
  pendingChanges?: number;
  checkConnection?: () => Promise<boolean>;
  online: boolean;
  onPhaseChange?: (phase: ConnectionPhase) => void;
}) {
  const detail = error instanceof ApiError ? `HTTP ${error.status} — ${error.message}` : errorMessage(error);
  // Banner retries drive the same refetch; the panel unmounts once it succeeds,
  // so the probe reports "not yet" and the backoff schedule advances on failure.
  const probe = checkConnection ?? (onRetry ? () => Promise.resolve(onRetry()).then(() => false) : undefined);
  return (
    <div>
      <ConnectionLost
        online={online}
        retryDelays={retryDelays}
        pendingChanges={pendingChanges}
        checkConnection={probe}
        onPhaseChange={onPhaseChange}
      />
      <div className="state-card" role="alert" style={{ marginTop: 12 }}>
        <UnpluggedScene />
        <p className="state-title">{heading}</p>
        <p className="state-mono">{detail}</p>
        <div className="state-action">
          {onRetry ? (
            <button type="button" className="button button-primary" onClick={onRetry}>
              Try again
            </button>
          ) : null}
          <Link className="button" to="/settings">
            Check gateway URL · {getApiBaseUrl()}
          </Link>
        </div>
      </div>
    </div>
  );
}

/** Generic app-error panel: copyable mono details plus a report action. */
function AppError({
  error,
  onRetry,
  heading,
  description,
  errorId,
  request,
  onReport,
  reportHref,
  dashboardHref,
}: {
  error: unknown;
  onRetry?: () => void;
  heading: string;
  description?: string;
  errorId?: string;
  request?: string;
  onReport?: (errorId: string) => void;
  reportHref?: string;
  dashboardHref: string;
}) {
  const [copied, setCopied] = useState(false);
  const statusLine = error instanceof ApiError ? `HTTP ${error.status} · ${error.code}` : undefined;
  const resolvedId = errorId ?? statusLine;
  const message = errorMessage(error);
  const copyText = [
    message,
    statusLine ? `Status: ${statusLine}` : null,
    resolvedId && resolvedId !== statusLine ? `Error ID: ${resolvedId}` : null,
    request ? `Request: ${request}` : null,
  ]
    .filter((line): line is string => line !== null)
    .join("\n");

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(copyText);
    } catch {
      // Clipboard can be unavailable (permissions, non-secure context):
      // the details stay visible in mono text regardless.
    }
    setCopied(true);
  };

  return (
    <div className="state-card" role="alert">
      <span className="state-icon state-icon-danger" aria-hidden="true">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round">
          <path d="M12 9v4m0 4h.01M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0Z" />
        </svg>
      </span>
      <p className="state-title">{heading}</p>
      <p className="state-description">{description ?? "Your data is safe. Copy the details below if you report this."}</p>
      <div className="state-mono" style={{ textAlign: "left", width: "100%" }}>
        <span style={{ display: "block" }}>{message}</span>
        {statusLine ? <span style={{ display: "block" }}>{statusLine}</span> : null}
        {resolvedId && resolvedId !== statusLine ? <span style={{ display: "block" }}>{resolvedId}</span> : null}
        {request ? <span style={{ display: "block" }}>{request}</span> : null}
      </div>
      <div className="state-action">
        <button type="button" className="button" onClick={copy}>
          {copied ? "Copied" : "Copy details"}
        </button>
        {reportHref ? (
          <a className="button button-link" href={reportHref}>
            Report this issue
          </a>
        ) : onReport && resolvedId ? (
          <button type="button" className="button button-link" onClick={() => onReport(resolvedId)}>
            Report this issue
          </button>
        ) : null}
      </div>
      <div className="state-action">
        {onRetry ? (
          <button type="button" className="button button-primary" onClick={onRetry}>
            Try again
          </button>
        ) : null}
        <Link className="button" to={dashboardHref}>
          Go to dashboard
        </Link>
      </div>
    </div>
  );
}

/** Designed error card: icon, title, mono detail, primary retry + secondary URL check. */
export function ErrorState({
  error,
  onRetry,
  title,
  variant = "gateway",
  description,
  retryDelays,
  pendingChanges,
  checkConnection,
  online = false,
  onPhaseChange,
  errorId,
  request,
  onReport,
  reportHref,
  dashboardHref = "/",
}: ErrorStateProps) {
  const heading = title ?? (variant === "app" ? "Something went wrong" : "Can't reach the gateway");
  if (variant === "app") {
    return (
      <AppError
        error={error}
        onRetry={onRetry}
        heading={heading}
        description={description}
        errorId={errorId}
        request={request}
        onReport={onReport}
        reportHref={reportHref}
        dashboardHref={dashboardHref}
      />
    );
  }
  return (
    <GatewayError
      error={error}
      onRetry={onRetry}
      heading={heading}
      retryDelays={retryDelays}
      pendingChanges={pendingChanges}
      checkConnection={checkConnection}
      online={online}
      onPhaseChange={onPhaseChange}
    />
  );
}
