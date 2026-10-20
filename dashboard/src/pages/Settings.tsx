import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useSettings } from "../api/hooks";
import { getApiBaseUrl, getStoredToken, setStoredToken } from "../api/client";
import { ErrorState } from "../components/ErrorState";
import { LoadingState } from "../components/LoadingState";
import { StatusBadge } from "../components/StatusBadge";
import { ThemeToggle } from "../components/ThemeToggle";
import { formatNumber, formatUptime } from "../lib/format";

/** Gateway status, telemetry counters, storage and dashboard preferences. */
export function Settings() {
  const query = useSettings();
  const queryClient = useQueryClient();
  const [token, setToken] = useState(() => getStoredToken());
  const [tokenSaved, setTokenSaved] = useState(false);

  const saveToken = () => {
    setStoredToken(token);
    setTokenSaved(true);
    // The token changed: drop the cached answers so they are fetched again
    // with the new credentials.
    void queryClient.invalidateQueries();
  };

  const clearToken = () => {
    setStoredToken("");
    setToken("");
    setTokenSaved(false);
    void queryClient.invalidateQueries();
  };

  const settings = query.data;

  return (
    <div className="page">
      <header className="page-header">
        <div>
          <h1>Settings</h1>
          <p className="page-subtitle">Gateway status and dashboard preferences.</p>
        </div>
        <StatusBadge
          label={query.isError ? "gateway unreachable" : "gateway reachable"}
          tone={query.isError ? "bad" : "good"}
        />
      </header>

      {query.isPending ? (
        <LoadingState label="Loading gateway status…" />
      ) : query.isError ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} title="Could not reach the gateway" />
      ) : !settings ? (
        <LoadingState />
      ) : (
        <>
          <section className="panel" aria-labelledby="gateway-info">
            <h2 id="gateway-info">Gateway</h2>
            <dl className="detail-grid">
              <div>
                <dt>Service</dt>
                <dd>{settings.service_name}</dd>
              </div>
              <div>
                <dt>Version</dt>
                <dd>{settings.version}</dd>
              </div>
              <div>
                <dt>A2A spec version</dt>
                <dd>{settings.spec_version}</dd>
              </div>
              <div>
                <dt>a2a-go SDK</dt>
                <dd>{settings.a2a_go_sdk_version ?? "unknown"}</dd>
              </div>
              <div>
                <dt>Uptime</dt>
                <dd>
                  {settings.uptime_seconds === undefined
                    ? "unknown"
                    : formatUptime(settings.uptime_seconds)}
                </dd>
              </div>
              <div>
                <dt>Agents</dt>
                <dd>{formatNumber(settings.agents)}</dd>
              </div>
            </dl>
          </section>

          <section className="panel" aria-labelledby="telemetry-counters">
            <h2 id="telemetry-counters">Telemetry</h2>
            <dl className="detail-grid">
              <div>
                <dt>Exporting</dt>
                <dd>
                  <StatusBadge
                    label={settings.telemetry.exporting ? "enabled" : "disabled"}
                    tone={settings.telemetry.exporting ? "good" : "warn"}
                  />
                </dd>
              </div>
              <div>
                <dt>OTLP endpoint</dt>
                <dd>{settings.telemetry.otlp_endpoint ?? "unknown"}</dd>
              </div>
              <div>
                <dt>Protocol</dt>
                <dd>{settings.telemetry.protocol ?? "unknown"}</dd>
              </div>
              <div>
                <dt>Sample ratio</dt>
                <dd>
                  {settings.telemetry.sample_ratio === undefined
                    ? "unknown"
                    : settings.telemetry.sample_ratio.toFixed(2)}
                </dd>
              </div>
              <div>
                <dt>Queue depth</dt>
                <dd>
                  {formatNumber(settings.telemetry.queue_depth ?? null)} /{" "}
                  {formatNumber(settings.telemetry.queue_capacity ?? null)}
                </dd>
              </div>
              <div>
                <dt>Spans ended</dt>
                <dd>{formatNumber(settings.telemetry.spans_ended)}</dd>
              </div>
              <div>
                <dt>Spans exported</dt>
                <dd>{formatNumber(settings.telemetry.spans_exported)}</dd>
              </div>
              <div>
                <dt>Export failures</dt>
                <dd>{formatNumber(settings.telemetry.export_failures ?? null)}</dd>
              </div>
              <div>
                <dt>Export retries</dt>
                <dd>{formatNumber(settings.telemetry.export_retries ?? null)}</dd>
              </div>
            </dl>

            <h3 className="sub-heading">Dropped spans by reason</h3>
            {Object.keys(settings.telemetry.spans_dropped).length === 0 ? (
              <p className="muted">No spans were dropped.</p>
            ) : (
              <ul className="plain-list">
                {Object.entries(settings.telemetry.spans_dropped).map(([reason, count]) => (
                  <li key={reason}>
                    {reason}: {formatNumber(count)}
                  </li>
                ))}
              </ul>
            )}
          </section>

          <section className="panel" aria-labelledby="storage-info">
            <h2 id="storage-info">Storage</h2>
            <dl className="detail-grid">
              <div>
                <dt>Driver</dt>
                <dd>{settings.storage.driver}</dd>
              </div>
              <div>
                <dt>Database</dt>
                <dd>{settings.storage.database ?? "unknown"}</dd>
              </div>
            </dl>
          </section>
        </>
      )}

      <section className="panel" aria-labelledby="dashboard-preferences">
        <h2 id="dashboard-preferences">Dashboard</h2>
        <dl className="detail-grid">
          <div>
            <dt>API base URL</dt>
            <dd>
              <code>{getApiBaseUrl()}</code>
            </dd>
          </div>
        </dl>

        <div className="field">
          <label htmlFor="api-token">API token</label>
          <input
            id="api-token"
            type="password"
            autoComplete="off"
            placeholder="Bearer token for the management API"
            value={token}
            onChange={(event) => {
              setToken(event.target.value);
              setTokenSaved(false);
            }}
          />
          <p className="field-hint">
            Stored in this browser only (localStorage). Leave empty when the gateway runs unauthenticated on
            loopback.
          </p>
        </div>
        <div className="button-row">
          <button type="button" className="button button-primary" onClick={saveToken}>
            Save token
          </button>
          <button type="button" className="button" onClick={clearToken}>
            Clear token
          </button>
          {tokenSaved ? <span className="muted">Token saved.</span> : null}
        </div>

        <div className="theme-row">
          <span className="stat-label">Theme</span>
          <ThemeToggle />
        </div>
      </section>
    </div>
  );
}
