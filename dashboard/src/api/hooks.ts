/**
 * React Query hooks, one per management API endpoint.
 *
 * All requests go through `api/client.ts`; the response types are derived
 * from the generated OpenAPI schema (see `api/types.ts`).
 */
import { useQuery, type UseQueryResult } from "@tanstack/react-query";
import { apiFetch, type QueryParams } from "./client";
import type {
  AgentsResponse,
  CostGroupBy,
  CostReport,
  DecisionKind,
  DecisionPage,
  Graph,
  Overview,
  Policy,
  Settings,
  TaskDetail,
  TaskPage,
  TaskStatus,
} from "./types";

/** Poll interval used by the live views (PRD UI-5). */
export const LIVE_REFETCH_INTERVAL_MS = 5_000;

export interface QueryOptions {
  /** Poll interval in milliseconds; `false` disables polling. */
  refetchInterval?: number | false;
  /** How long a cached answer is considered fresh. */
  staleTime?: number;
  /** Skip the request while this is false. */
  enabled?: boolean;
}

export type TaskFilters = {
  from?: string;
  to?: string;
  status?: TaskStatus | "";
  agent?: string;
  task_id?: string;
  min_cost_usd?: number;
  max_cost_usd?: number;
  limit?: number;
  offset?: number;
};

function useApiQuery<T>(
  queryKey: readonly unknown[],
  path: string,
  query: QueryParams | undefined,
  options: QueryOptions = {},
): UseQueryResult<T, Error> {
  return useQuery<T, Error>({
    queryKey,
    queryFn: ({ signal }) => apiFetch<T>(path, { query, signal }),
    staleTime: options.staleTime ?? 5_000,
    refetchInterval: options.refetchInterval ?? false,
    enabled: options.enabled ?? true,
  });
}

/** Dashboard summary for a time window. */
export function useOverview(
  window: QueryParams,
  options: QueryOptions = {},
): UseQueryResult<Overview, Error> {
  return useApiQuery<Overview>(["overview", window], "/api/v1/overview", window, options);
}

/** Paginated, filtered task list. */
export function useTasks(
  filters: TaskFilters,
  options: QueryOptions = {},
): UseQueryResult<TaskPage, Error> {
  return useApiQuery<TaskPage>(["tasks", filters], "/api/v1/tasks", filters, options);
}

/** Task detail: span tree, root-cause hint, cost breakdown and events. */
export function useTaskDetail(
  taskId: string,
  options: QueryOptions = {},
): UseQueryResult<TaskDetail, Error> {
  return useApiQuery<TaskDetail>(
    ["task", taskId],
    `/api/v1/tasks/${encodeURIComponent(taskId)}`,
    undefined,
    options,
  );
}

/** Agent-to-agent call graph for a time window. */
export function useGraph(window: QueryParams, options: QueryOptions = {}): UseQueryResult<Graph, Error> {
  return useApiQuery<Graph>(["graph", window], "/api/v1/graph", window, {
    staleTime: 10_000,
    ...options,
  });
}

/** Registry of agents with card, verification and traffic statistics. */
export function useAgents(
  window: QueryParams,
  options: QueryOptions = {},
): UseQueryResult<AgentsResponse, Error> {
  return useApiQuery<AgentsResponse>(["agents", window], "/api/v1/agents", window, {
    staleTime: 10_000,
    ...options,
  });
}

/** Cost and token aggregates, grouped as requested. */
export function useCosts(
  window: QueryParams,
  groupBy: CostGroupBy,
  options: QueryOptions = {},
): UseQueryResult<CostReport, Error> {
  return useApiQuery<CostReport>(
    ["costs", window, groupBy],
    "/api/v1/costs",
    { ...window, group_by: groupBy },
    options,
  );
}

/** Gateway status, versions, telemetry counters and storage driver. */
export function useSettings(options: QueryOptions = {}): UseQueryResult<Settings, Error> {
  return useApiQuery<Settings>(["settings"], "/api/v1/settings", undefined, options);
}

/** Authorization policy and the caller x callee matrix. */
export function usePolicy(options: QueryOptions = {}): UseQueryResult<Policy, Error> {
  return useApiQuery<Policy>(["policy"], "/api/v1/policy", undefined, {
    staleTime: 10_000,
    ...options,
  });
}

/** Recent policy, trust and authentication decisions. */
export function useDecisions(
  kind: DecisionKind | "",
  limit = 100,
  options: QueryOptions = {},
): UseQueryResult<DecisionPage, Error> {
  return useApiQuery<DecisionPage>(
    ["decisions", kind, limit],
    "/api/v1/decisions",
    { kind, limit },
    options,
  );
}
