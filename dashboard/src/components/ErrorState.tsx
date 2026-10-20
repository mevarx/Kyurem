import { ApiError } from "../api/client";
import { errorMessage } from "../lib/format";

export interface ErrorStateProps {
  error: unknown;
  /** Optional retry handler; a retry button is rendered when present. */
  onRetry?: () => void;
  title?: string;
}

/** Shown when a request failed. Never renders the raw error object. */
export function ErrorState({ error, onRetry, title = "Could not load data" }: ErrorStateProps) {
  const detail = error instanceof ApiError ? `HTTP ${error.status} — ${error.message}` : errorMessage(error);
  return (
    <div className="state-panel state-error" role="alert">
      <p className="state-title">{title}</p>
      <p className="state-description">{detail}</p>
      {onRetry ? (
        <div className="state-action">
          <button type="button" className="button" onClick={onRetry}>
            Try again
          </button>
        </div>
      ) : null}
    </div>
  );
}
