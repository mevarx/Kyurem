/**
 * Convenience aliases over the generated OpenAPI types.
 *
 * Everything below is derived from `src/api/schema.d.ts`, which is generated
 * from `../api/openapi.yaml` (`npm run gen:api`). Never hand-write response
 * shapes here - re-run the generator instead.
 */
import type { components, paths } from "./schema";

type Schemas = components["schemas"];

/* ------------------------------------------------------------------ */
/* Response bodies                                                     */
/* ------------------------------------------------------------------ */

export type Overview = paths["/api/v1/overview"]["get"]["responses"]["200"]["content"]["application/json"];
export type TaskPage = paths["/api/v1/tasks"]["get"]["responses"]["200"]["content"]["application/json"];
export type TaskDetail = paths["/api/v1/tasks/{task_id}"]["get"]["responses"]["200"]["content"]["application/json"];
export type Graph = paths["/api/v1/graph"]["get"]["responses"]["200"]["content"]["application/json"];
export type AgentsResponse = paths["/api/v1/agents"]["get"]["responses"]["200"]["content"]["application/json"];
export type CostReport = paths["/api/v1/costs"]["get"]["responses"]["200"]["content"]["application/json"];
export type Policy = paths["/api/v1/policy"]["get"]["responses"]["200"]["content"]["application/json"];
export type DecisionPage = paths["/api/v1/decisions"]["get"]["responses"]["200"]["content"]["application/json"];
export type Settings = paths["/api/v1/settings"]["get"]["responses"]["200"]["content"]["application/json"];
export type ApiErrorBody = components["schemas"]["Error"];

/* ------------------------------------------------------------------ */
/* Schema aliases                                                      */
/* ------------------------------------------------------------------ */

export type Task = Schemas["Task"];
export type Span = Schemas["Span"];
export type RootCause = Schemas["RootCause"];
export type CostBucket = Schemas["CostBucket"];
export type TimeBucket = Schemas["TimeBucket"];
export type FailingAgent = Schemas["FailingAgent"];
export type AgentInfo = Schemas["AgentInfo"];
export type GraphNode = Schemas["GraphNode"];
export type GraphEdge = Schemas["GraphEdge"];
export type Event = Schemas["Event"];
export type PolicyRule = Schemas["PolicyRule"];
export type PolicyMatrixCell = Schemas["PolicyMatrixCell"];
export type Decision = Schemas["Decision"];

/* ------------------------------------------------------------------ */
/* Enumerations                                                        */
/* ------------------------------------------------------------------ */

export type TaskStatus = Schemas["Task"]["status"];
export type CardStatus = Schemas["AgentInfo"]["card_status"];
export type BreakerState = NonNullable<Schemas["AgentInfo"]["breaker_state"]>;
export type VerificationStatus = Schemas["AgentInfo"]["verification"];
export type VerificationMode = Schemas["AgentInfo"]["verification_mode"];
export type PolicyMode = Schemas["Policy"]["mode"];
export type PolicyEffect = Schemas["PolicyRule"]["effect"];
export type PolicyDecision = Schemas["PolicyMatrixCell"]["decision"];
export type DecisionKind = Schemas["Decision"]["kind"];
export type CostGroupBy = "agent" | "model" | "task" | "window";
export type StorageDriver = Schemas["Settings"]["storage"]["driver"];
