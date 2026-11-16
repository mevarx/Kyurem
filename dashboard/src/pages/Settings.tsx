import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { EyeIcon, EyeOffIcon } from "lucide-react";
import { useSettings } from "../api/hooks";
import { getApiBaseUrl, getStoredToken, setStoredToken } from "../api/client";
import { ErrorState } from "../components/ErrorState";
import { TableSkeleton } from "../components/Skeleton";
import {
  DashboardCard,
  DetailHeader,
} from "../components/watermelon/capitalio-dashboard/components/capitalio/shared";
import { ShortcutSheet } from "../components/shortcut-sheet";
import { StatusBadge } from "../components/StatusBadge";
import { formatNumber, formatUptime } from "../lib/format";

/** Gateway status, telemetry counters, storage and dashboard preferences. */
export function Settings() {
  const query = useSettings();
  const queryClient = useQueryClient();
  const [token, setToken] = useState(() => getStoredToken());
  const [tokenSaved, setTokenSaved] = useState(false);
  const [tokenVisible, setTokenVisible] = useState(false);

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
  const checking = query.isPending || query.isFetching;

  return (
    <div className="page">
      <header className="page-header">
        <div className="page-title-pair">
          <span className="page-kicker">Manage</span>
          <h1>Settings</h1>
          <p className="page-subtitle">Gateway status, API access and keyboard shortcuts.</p>
        </div>
        <StatusBadge
          label={query.isPending ? "checking gateway" : query.isError ? "settings unavailable" : "gateway reachable"}
          tone={query.isPending ? "neutral" : query.isError ? "bad" : "good"}
        />
      </header>

      {query.isPending ? (
        <DashboardCard aria-label="Gateway status loading">
          <TableSkeleton rows={5} />
        </DashboardCard>
      ) : query.isError ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : !settings ? (
        <DashboardCard aria-label="Gateway status loading">
          <TableSkeleton rows={5} />
        </DashboardCard>
      ) : (
        <>
          <DashboardCard aria-label="Gateway">
            <DetailHeader title="Gateway" subtitle="Service identity and build versions" />
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
          </DashboardCard>
        </>
      )}

      <DashboardCard aria-label="API connection">
        <DetailHeader title="API connection" subtitle="Resolved endpoint, credentials and live reachability" />
        <dl className="detail-grid">
          <div>
            <dt>Base URL</dt>
            <dd>
              <code>{getApiBaseUrl()}</code>
            </dd>
          </div>
          <div>
            <dt>Auth mode</dt>
            <dd>{getStoredToken() ? "bearer token" : "none (loopback)"}</dd>
          </div>
          <div>
            <dt>Reachability</dt>
            <dd>
              <StatusBadge
                label={checking ? "checking" : query.isError ? "unreachable" : "reachable"}
                tone={checking ? "neutral" : query.isError ? "bad" : "good"}
              />
            </dd>
          </div>
        </dl>
        {query.isError && !query.isFetching ? (
          <div className="button-row">
            <button type="button" className="button" onClick={() => void query.refetch()}>
              Retry
            </button>
          </div>
        ) : null}
      </DashboardCard>

      {settings ? (
        <>
          <DashboardCard aria-label="Telemetry">
            <DetailHeader title="Telemetry" subtitle="Export pipeline counters" />
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
          </DashboardCard>

          <DashboardCard aria-label="Storage">
            <DetailHeader title="Storage" subtitle="Persistence driver and database" />
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
          </DashboardCard>
        </>
      ) : null}

      <DashboardCard aria-label="Dashboard preferences">
        <DetailHeader title="Dashboard" subtitle="Appearance and API credentials" />
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
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <input
              id="api-token"
              type={tokenVisible ? "text" : "password"}
              autoComplete="off"
              placeholder="Bearer token for the management API"
              value={token}
              style={{ flex: 1, minWidth: 0 }}
              onChange={(event) => {
                setToken(event.target.value);
                setTokenSaved(false);
              }}
            />
            <button
              type="button"
              className="icon-button"
              aria-label={tokenVisible ? "Hide token" : "Show token"}
              aria-pressed={tokenVisible}
              title={tokenVisible ? "Hide token" : "Show token"}
              onClick={() => setTokenVisible((visible) => !visible)}
            >
              {tokenVisible ? <EyeOffIcon aria-hidden="true" /> : <EyeIcon aria-hidden="true" />}
            </button>
          </div>
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

      </DashboardCard>

      <DashboardCard aria-label="Keyboard shortcuts">
        <DetailHeader title="Keyboard shortcuts" subtitle="Command menu and shortcut panel" />
        <ShortcutSheet
          className="mt-4"
          groups={[
            {
              title: "Command menu",
              shortcuts: [
                { label: "Open command menu", keys: [["mod", "K"]], keywords: ["palette", "search", "go to"] },
                { label: "Run highlighted action", keys: [["enter"]], keywords: ["select", "confirm"] },
                { label: "Move selection up", keys: [["up"]], keywords: ["navigate", "arrows"] },
                { label: "Move selection down", keys: [["down"]], keywords: ["navigate", "arrows"] },
                { label: "Close menu", keys: [["esc"]], keywords: ["dismiss", "exit"] },
              ],
            },
            {
              title: "Shortcut panel",
              shortcuts: [
                { label: "Open this panel", keys: [["?"]], keywords: ["help", "shortcuts"] },
                { label: "Filter shortcuts", keys: [["type"]], keywords: ["search", "find"] },
                { label: "Close panel", keys: [["esc"]], keywords: ["dismiss", "exit"] },
              ],
            },
          ]}
        >
          <p className="muted px-4 py-4">Focus this card, then press ? to see shortcuts. The command menu works anywhere.</p>
        </ShortcutSheet>
      </DashboardCard>
    </div>
  );
}
