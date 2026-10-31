/**
 * Dev-only mock data mode.
 *
 * Enabled with `VITE_MOCK_DATA=1`. Intercepts `window.fetch` for the
 * management API paths and returns plausible canned responses so every
 * screen can be seen populated without a running gateway. Data fetching
 * logic (hooks, routes, client) is untouched: the mocks sit underneath
 * `fetch`, exactly where the network would be.
 */

const AGENTS = ["planner", "travel", "booking", "summarizer"];

function mulberry(seed: number): () => number {
  let state = seed;
  return () => {
    state |= 0;
    state = (state + 0x6d2b79f5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function hourlyBuckets(count: number, seed: number) {
  const rand = mulberry(seed);
  const now = Date.now();
  return Array.from({ length: count }, (_, i) => {
    const time = new Date(now - (count - 1 - i) * 3_600_000).toISOString();
    const succeeded = Math.round(40 + rand() * 120);
    const failed = Math.round(rand() * rand() * 18);
    const unknown = Math.round(rand() * 4);
    return {
      bucket: time,
      succeeded,
      failed,
      unknown,
      spans: succeeded + failed + unknown,
      cost_usd: Number(((succeeded + failed) * (0.004 + rand() * 0.01)).toFixed(4)),
    };
  });
}

function mockOverview() {
  const time = hourlyBuckets(24, 7);
  const succeeded = time.reduce((a, b) => a + b.succeeded, 0);
  const failed = time.reduce((a, b) => a + b.failed, 0);
  return {
    tasks_total: succeeded + failed + 41,
    tasks_succeeded: succeeded,
    tasks_failed: failed,
    error_rate: failed / Math.max(1, succeeded + failed),
    p50_task_duration_ms: 1420,
    p95_task_duration_ms: 30250,
    total_cost_usd: 18.42,
    active_agents: 4,
    spans_total: 5214,
    tasks_over_time: time,
    cost_over_time: time,
    top_agents_by_cost: [
      { key: "travel", cost_usd: 8.21, spans: 1210, unknown_spans: 12 },
      { key: "planner", cost_usd: 5.74, spans: 1840, unknown_spans: 4 },
      { key: "booking", cost_usd: 3.12, spans: 980, unknown_spans: 9 },
      { key: "summarizer", cost_usd: 1.35, spans: 640, unknown_spans: 2 },
    ],
    top_failing_agents: [
      { name: "booking", errors: 34, error_rate: 0.18 },
      { name: "travel", errors: 21, error_rate: 0.09 },
      { name: "planner", errors: 6, error_rate: 0.02 },
    ],
    recent_failed_tasks: [
      {
        task_id: "task-9f31c4",
        trace_id: "trace-9f31c4",
        status: "failed",
        started_at: new Date(Date.now() - 4 * 60_000).toISOString(),
        duration_ms: 30250,
        agents: ["planner", "booking"],
        root_caller: "planner",
        cost_usd: 0.0432,
        tokens_known: true,
        span_count: 7,
        error_type: "timeout",
        error_message: "booking did not answer within 30s",
      },
      {
        task_id: "task-77ad02",
        trace_id: "trace-77ad02",
        status: "failed",
        started_at: new Date(Date.now() - 26 * 60_000).toISOString(),
        duration_ms: 12480,
        agents: ["travel"],
        root_caller: "travel",
        cost_usd: 0.0188,
        tokens_known: true,
        span_count: 4,
        error_type: "upstream_5xx",
        error_message: "upstream returned 503 for /search",
      },
    ],
  };
}

function mockTasks() {
  return {
    items: [
      {
        task_id: "task-9f31c4",
        trace_id: "trace-9f31c4",
        status: "failed",
        started_at: new Date(Date.now() - 4 * 60_000).toISOString(),
        duration_ms: 30250,
        agents: ["planner", "booking"],
        root_caller: "planner",
        cost_usd: 0.0432,
        input_tokens: 4200,
        output_tokens: 900,
        tokens_known: true,
        span_count: 7,
        error_type: "timeout",
        error_message: "booking did not answer within 30s",
      },
      {
        task_id: "task-51bd77",
        trace_id: "trace-51bd77",
        status: "succeeded",
        started_at: new Date(Date.now() - 11 * 60_000).toISOString(),
        duration_ms: 2310,
        agents: ["planner", "travel"],
        root_caller: "planner",
        cost_usd: 0.0121,
        input_tokens: 1800,
        output_tokens: 420,
        tokens_known: true,
        span_count: 5,
      },
      {
        task_id: "task-c804e1",
        trace_id: "trace-c804e1",
        status: "unknown",
        started_at: new Date(Date.now() - 33 * 60_000).toISOString(),
        duration_ms: 980,
        agents: ["summarizer"],
        root_caller: "summarizer",
        cost_usd: null,
        input_tokens: null,
        output_tokens: null,
        tokens_known: false,
        span_count: 2,
      },
    ],
    total: 3,
    limit: 50,
    offset: 0,
  };
}

function mockTaskDetail(taskId: string) {
  const task = { ...mockTasks().items[0], task_id: taskId, trace_id: `trace-${taskId}` };
  return {
    task,
    spans: [
      {
        trace_id: task.trace_id,
        span_id: "s1",
        name: "planner: plan trip",
        kind: "server",
        agent: "planner",
        started_at: task.started_at,
        duration_ms: 30250,
        status_code: "ERROR",
        error_type: "timeout",
        attributes: { "kyurem.caller": "planner", "kyurem.callee": "booking" },
      },
      {
        trace_id: task.trace_id,
        span_id: "s2",
        name: "booking: hold seats",
        kind: "client",
        agent: "booking",
        started_at: task.started_at,
        duration_ms: 30000,
        status_code: "ERROR",
        error_type: "timeout",
        attributes: {},
      },
    ],
    root_cause: {
      span_id: "s2",
      span_name: "booking: hold seats",
      agent: "booking",
      error_type: "timeout",
      message: "The booking agent did not answer within 30s, so the caller gave up.",
      affected_span_ids: ["s2"],
      confidence: "heuristic",
    },
    cost: {
      group_by: "agent",
      total_usd: 0.0432,
      spans: 7,
      unknown_spans: 0,
      buckets: [
        { key: "planner", cost_usd: 0.019, input_tokens: 2400, output_tokens: 500, spans: 4, unknown_spans: 0 },
        { key: "booking", cost_usd: 0.0242, input_tokens: 1800, output_tokens: 400, spans: 3, unknown_spans: 0 },
      ],
    },
    events: [
      {
        time: task.started_at,
        name: "kyurem.policy.decision",
        span_id: "s1",
        attributes: { decision: "allow", caller: "planner", callee: "booking" },
      },
    ],
  };
}

function mockGraph() {
  return {
    nodes: [
      { name: "planner", calls: 812, errors: 18, error_rate: 0.022, p50_ms: 900, p95_ms: 30250, last_seen: new Date().toISOString(), cost_usd: 5.74 },
      { name: "travel", calls: 640, errors: 21, error_rate: 0.033, p50_ms: 1100, p95_ms: 18400, last_seen: new Date().toISOString(), cost_usd: 8.21 },
      { name: "booking", calls: 402, errors: 34, error_rate: 0.085, p50_ms: 2400, p95_ms: 31000, last_seen: new Date().toISOString(), cost_usd: 3.12 },
      { name: "summarizer", calls: 388, errors: 2, error_rate: 0.005, p50_ms: 700, p95_ms: 4200, last_seen: new Date().toISOString(), cost_usd: 1.35 },
    ],
    edges: [
      { from: "planner", to: "travel", calls: 512, errors: 9, error_rate: 0.018, p95_ms: 9200, cost_usd: 4.1 },
      { from: "planner", to: "booking", calls: 300, errors: 26, error_rate: 0.087, p95_ms: 30500, cost_usd: 2.4 },
      { from: "travel", to: "summarizer", calls: 388, errors: 2, error_rate: 0.005, p95_ms: 4200, cost_usd: 1.35 },
    ],
  };
}

function mockAgents() {
  return {
    items: AGENTS.map((name, i) => ({
      name,
      aliases: i === 0 ? ["trip-planner"] : [],
      upstreams: [`http://${name}:9000`],
      card_status: "ok",
      card_version: "1.0.0",
      verification: i === 3 ? "unchecked" : "verified",
      verification_mode: "enforce",
      breaker_state: i === 2 ? "half_open" : "closed",
      last_seen: new Date(Date.now() - i * 60_000).toISOString(),
      spans: [1840, 1210, 980, 640][i],
      errors: [6, 21, 34, 2][i],
      error_rate: [0.02, 0.09, 0.18, 0.01][i],
      p95_ms: [30250, 18400, 31000, 4200][i],
      cost_usd: [5.74, 8.21, 3.12, 1.35][i],
    })),
  };
}

function mockCosts(groupBy: string) {
  const buckets =
    groupBy === "model"
      ? [
          { key: "kyurem-ice-1", cost_usd: 11.4, input_tokens: 812000, output_tokens: 144000, spans: 3200, unknown_spans: 20 },
          { key: "kyurem-frost-0", cost_usd: 7.02, input_tokens: 402000, output_tokens: 88000, spans: 2014, unknown_spans: 7 },
        ]
      : groupBy === "window"
        ? hourlyBuckets(12, 21).map((b) => ({
            key: b.bucket,
            cost_usd: b.cost_usd,
            input_tokens: b.spans * 220,
            output_tokens: b.spans * 44,
            spans: b.spans,
            unknown_spans: 1,
          }))
        : groupBy === "task"
          ? [
              { key: "task-9f31c4", cost_usd: 0.0432, input_tokens: 4200, output_tokens: 900, spans: 7, unknown_spans: 0 },
              { key: "task-51bd77", cost_usd: 0.0121, input_tokens: 1800, output_tokens: 420, spans: 5, unknown_spans: 0 },
            ]
          : [
              { key: "travel", cost_usd: 8.21, input_tokens: 402000, output_tokens: 88000, spans: 1210, unknown_spans: 12 },
              { key: "planner", cost_usd: 5.74, input_tokens: 318000, output_tokens: 62000, spans: 1840, unknown_spans: 4 },
              { key: "booking", cost_usd: 3.12, input_tokens: 190000, output_tokens: 41000, spans: 980, unknown_spans: 9 },
              { key: "summarizer", cost_usd: 1.35, input_tokens: 92000, output_tokens: 19000, spans: 640, unknown_spans: 2 },
            ];
  return {
    group_by: groupBy,
    total_usd: 18.42,
    spans: 5214,
    unknown_spans: 27,
    buckets,
  };
}

function mockSettings() {
  return {
    version: "0.3.0",
    spec_version: "1.0.0",
    service_name: "kyurem-gateway",
    a2a_go_sdk_version: "0.9.2",
    uptime_seconds: 7322,
    agents: 4,
    telemetry: {
      exporting: true,
      otlp_endpoint: "http://otel:4317",
      protocol: "grpc",
      sample_ratio: 1,
      queue_depth: 12,
      queue_capacity: 2048,
      spans_ended: 5214,
      spans_exported: 5198,
      export_failures: 0,
      export_retries: 3,
      spans_dropped: {},
    },
    storage: { driver: "memory", database: null },
  };
}

function mockPolicy() {
  return {
    mode: "enforce",
    default: "deny",
    rules: [
      { name: "planner-may-call-travel", caller: "planner", callee: "travel", skills: ["search", "quote"], effect: "allow" },
      { name: "planner-may-call-booking", caller: "planner", callee: "booking", skills: ["hold"], effect: "allow" },
    ],
    callers: ["planner", "travel"],
    callees: ["travel", "booking", "summarizer"],
    matrix: [
      { caller: "planner", callee: "travel", skill: null, decision: "allow", rule: "planner-may-call-travel" },
      { caller: "planner", callee: "booking", skill: null, decision: "allow", rule: "planner-may-call-booking" },
      { caller: "planner", callee: "summarizer", skill: null, decision: "deny", rule: null },
      { caller: "travel", callee: "travel", skill: null, decision: "deny", rule: null },
      { caller: "travel", callee: "booking", skill: null, decision: "deny", rule: null },
      { caller: "travel", callee: "summarizer", skill: null, decision: "allow", rule: null },
    ],
  };
}

function mockDecisions() {
  const now = Date.now();
  return {
    items: [
      {
        timestamp: new Date(now - 4 * 60_000).toISOString(),
        kind: "policy",
        caller: "planner",
        callee: "booking",
        skill: "hold",
        decision: "allow",
        mode: "enforce",
        reason: "matched rule planner-may-call-booking",
        rule: "planner-may-call-booking",
      },
      {
        timestamp: new Date(now - 19 * 60_000).toISOString(),
        kind: "trust",
        caller: "travel",
        callee: "booking",
        skill: null,
        decision: "deny",
        mode: "enforce",
        reason: "no rule allows travel → booking",
        rule: null,
      },
      {
        timestamp: new Date(now - 42 * 60_000).toISOString(),
        kind: "auth",
        caller: "summarizer",
        callee: "planner",
        skill: null,
        decision: "warn",
        mode: "warn",
        reason: "unsigned card, warn mode",
        rule: null,
      },
    ],
    total: 3,
    limit: 100,
    offset: 0,
  };
}

/** Install the fetch interceptor. Safe to call once at startup. */
export function installMockApi(): void {
  const original = window.fetch.bind(window);
  window.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
    let path = url;
    try {
      path = new URL(url, window.location.origin).pathname;
    } catch {
      // relative URL that failed to parse: fall through to the original fetch
      return original(input, init);
    }
    if (!path.startsWith("/api/v1/")) return original(input, init);

    const respond = (body: unknown) =>
      new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });

    if (path === "/api/v1/overview") return respond(mockOverview());
    if (path === "/api/v1/tasks") return respond(mockTasks());
    if (path.startsWith("/api/v1/tasks/")) {
      return respond(mockTaskDetail(decodeURIComponent(path.split("/").pop() ?? "task-mock")));
    }
    if (path === "/api/v1/graph") return respond(mockGraph());
    if (path === "/api/v1/agents") return respond(mockAgents());
    if (path === "/api/v1/costs") {
      let groupBy = "agent";
      try {
        groupBy = new URL(url, window.location.origin).searchParams.get("group_by") ?? "agent";
      } catch {
        // keep default
      }
      return respond(mockCosts(groupBy));
    }
    if (path === "/api/v1/settings") return respond(mockSettings());
    if (path === "/api/v1/policy") return respond(mockPolicy());
    if (path === "/api/v1/decisions") return respond(mockDecisions());
    return original(input, init);
  }) as typeof window.fetch;
}
