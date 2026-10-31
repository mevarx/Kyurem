import { Link } from "react-router-dom";
import { ApiError, getApiBaseUrl } from "../api/client";
import { errorMessage } from "../lib/format";

export interface ErrorStateProps {
  error: unknown;
  /** Optional retry handler; a retry button is rendered when present. */
  onRetry?: () => void;
  title?: string;
}

/** Designed error card: icon, title, mono detail, primary retry + secondary URL check. */
export function ErrorState({ error, onRetry, title = "Can't reach the gateway" }: ErrorStateProps) {
  const detail = error instanceof ApiError ? `HTTP ${error.status} — ${error.message}` : errorMessage(error);
  return (
    <div className="state-card" role="alert">
      <span className="state-icon state-icon-danger" aria-hidden="true">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round">
          <path d="M12 9v4m0 4h.01M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0Z" />
        </svg>
      </span>
      <p className="state-title">{title}</p>
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
  );
}
