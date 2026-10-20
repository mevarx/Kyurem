export interface LoadingStateProps {
  label?: string;
}

/** Shown while a request is in flight. */
export function LoadingState({ label = "Loading…" }: LoadingStateProps) {
  return (
    <div className="state-panel state-loading" role="status" aria-live="polite">
      <span className="spinner" aria-hidden="true" />
      <p className="state-description">{label}</p>
    </div>
  );
}
