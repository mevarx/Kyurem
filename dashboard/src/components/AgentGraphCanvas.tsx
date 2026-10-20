import { useMemo } from "react";
import dagre from "dagre";
import {
  Background,
  BaseEdge,
  Controls,
  EdgeLabelRenderer,
  Handle,
  MiniMap,
  Position,
  ReactFlow,
  getBezierPath,
  type Edge,
  type EdgeProps,
  type Node,
  type NodeProps,
} from "@xyflow/react";
import type { AgentInfo, GraphEdge, GraphNode } from "../api/types";
import { formatCost, formatDuration, formatNumber, formatPercent } from "../lib/format";
import { breakerStateTone, cardStatusTone, errorRateTone, isUnknownState } from "../lib/status";
import { StatusBadge } from "./StatusBadge";

export type AgentNodeData = {
  name: string;
  calls: number;
  errors: number;
  errorRate: number;
  p95: number;
  cost: number | null;
  cardStatus?: string;
  verification?: string;
  breakerState?: string;
  lastSeen?: string | null;
};

export type AgentEdgeData = {
  calls: number;
  errors: number;
  errorRate: number;
  p95: number;
  cost: number | null;
  from: string;
  to: string;
};

type AgentFlowNode = Node<AgentNodeData, "agent">;
type AgentFlowEdge = Edge<AgentEdgeData, "agent">;

const NODE_WIDTH = 220;
const NODE_HEIGHT = 96;

function AgentNode({ data }: NodeProps<AgentFlowNode>) {
  const cardStatus = data.cardStatus ?? "unknown";
  const verification = data.verification ?? "unknown";
  return (
    <div className="graph-node">
      <Handle type="target" position={Position.Left} />
      <div className="graph-node-head">
        <span className="graph-node-name">{data.name}</span>
        <StatusBadge label={cardStatus} tone={cardStatusTone(cardStatus)} title="Agent card status" />
      </div>
      <div className="graph-node-badges">
        <StatusBadge
          label={`verify: ${verification}`}
          tone={isUnknownState(verification) ? "neutral" : "good"}
          title="Card signature verification"
        />
        {data.breakerState ? (
          <StatusBadge
            label={`breaker: ${data.breakerState}`}
            tone={breakerStateTone(data.breakerState)}
            title="Circuit breaker state"
          />
        ) : null}
        <StatusBadge
          label={`${formatPercent(data.errorRate)} errors`}
          tone={errorRateTone(data.errorRate)}
          title="Error rate"
        />
      </div>
      <div className="graph-node-stats">
        <span>{formatNumber(data.calls)} calls</span>
        <span>p95 {formatDuration(data.p95)}</span>
        <span>{formatCost(data.cost)}</span>
      </div>
      <Handle type="source" position={Position.Right} />
    </div>
  );
}

function edgeColor(errorRate: number): string {
  if (errorRate >= 0.25) return "#d64545";
  if (errorRate > 0) return "#d99b28";
  return "#5b8c5a";
}

function AgentEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  data,
  markerEnd,
}: EdgeProps<AgentFlowEdge>) {
  const [path, labelX, labelY] = getBezierPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
  });
  const errorRate = data?.errorRate ?? 0;
  const calls = data?.calls ?? 0;
  const tooltip = data
    ? `${data.from} → ${data.to}: ${formatNumber(data.calls)} calls, ${formatNumber(
        data.errors,
      )} errors (${formatPercent(data.errorRate)}), p95 ${formatDuration(data.p95)}, cost ${formatCost(data.cost)}`
    : undefined;

  return (
    <>
      <BaseEdge
        id={id}
        path={path}
        markerEnd={markerEnd}
        style={{
          stroke: edgeColor(errorRate),
          strokeWidth: Math.min(6, 1 + calls / 10),
        }}
      >
        {tooltip ? <title>{tooltip}</title> : null}
      </BaseEdge>
      <EdgeLabelRenderer>
        <div
          className="graph-edge-label"
          style={{
            transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`,
          }}
          title={tooltip}
        >
          {formatNumber(calls)}
        </div>
      </EdgeLabelRenderer>
    </>
  );
}

const nodeTypes = { agent: AgentNode };
const edgeTypes = { agent: AgentEdge };

export interface AgentGraphCanvasProps {
  nodes: GraphNode[];
  edges: GraphEdge[];
  agents?: AgentInfo[];
  selectedAgent?: string | null;
  onSelectAgent?: (name: string) => void;
}

/** Interactive agent-to-agent call graph (React Flow + dagre layout). */
export function AgentGraphCanvas({
  nodes,
  edges,
  agents = [],
  selectedAgent,
  onSelectAgent,
}: AgentGraphCanvasProps) {
  const agentsByName = useMemo(() => {
    const map = new Map<string, AgentInfo>();
    for (const agent of agents) map.set(agent.name, agent);
    return map;
  }, [agents]);

  const flow = useMemo(() => {
    const graph = new dagre.graphlib.Graph();
    graph.setDefaultEdgeLabel(() => ({}));
    graph.setGraph({ rankdir: "LR", nodesep: 40, ranksep: 160 });

    const agentNames = new Set(nodes.map((node) => node.name));
    for (const edge of edges) {
      agentNames.add(edge.from);
      agentNames.add(edge.to);
    }

    const flowNodes: AgentFlowNode[] = [...agentNames].map((name) => {
      const stats = nodes.find((node) => node.name === name);
      const agent = agentsByName.get(name);
      return {
        id: name,
        type: "agent",
        position: { x: 0, y: 0 },
        selected: name === selectedAgent,
        data: {
          name,
          calls: stats?.calls ?? 0,
          errors: stats?.errors ?? 0,
          errorRate: stats?.error_rate ?? 0,
          p95: stats?.p95_ms ?? 0,
          cost: stats?.cost_usd ?? null,
          cardStatus: agent?.card_status,
          verification: agent?.verification,
          breakerState: agent?.breaker_state,
          lastSeen: stats?.last_seen ?? null,
        },
      };
    });

    const flowEdges: AgentFlowEdge[] = edges.map((edge) => ({
      id: `${edge.from}->${edge.to}`,
      source: edge.from,
      target: edge.to,
      type: "agent",
      data: {
        calls: edge.calls,
        errors: edge.errors,
        errorRate: edge.error_rate,
        p95: edge.p95_ms,
        cost: edge.cost_usd ?? null,
        from: edge.from,
        to: edge.to,
      },
    }));

    for (const node of flowNodes) graph.setNode(node.id, { width: NODE_WIDTH, height: NODE_HEIGHT });
    for (const edge of flowEdges) graph.setEdge(edge.source, edge.target);
    dagre.layout(graph);

    const positioned = flowNodes.map((node) => {
      const position = graph.node(node.id);
      return {
        ...node,
        targetPosition: Position.Left,
        sourcePosition: Position.Right,
        position: {
          x: (position?.x ?? 0) - NODE_WIDTH / 2,
          y: (position?.y ?? 0) - NODE_HEIGHT / 2,
        },
      };
    });

    return { nodes: positioned, edges: flowEdges };
  }, [nodes, edges, agentsByName, selectedAgent]);

  return (
    <div className="graph-canvas" role="group" aria-label="Agent call graph">
      <ReactFlow
        nodes={flow.nodes}
        edges={flow.edges}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        onNodeClick={(_event, node) => onSelectAgent?.(node.id)}
        fitView
        minZoom={0.2}
        maxZoom={2}
      >
        <Background />
        <Controls />
        <MiniMap pannable zoomable />
      </ReactFlow>
    </div>
  );
}
