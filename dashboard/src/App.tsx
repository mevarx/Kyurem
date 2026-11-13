import { Suspense, lazy } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { Layout } from "./components/Layout";
import { OverviewSkeleton, TableSkeleton } from "./components/Skeleton";

const AgentGraph = lazy(() => import("./pages/AgentGraph").then((m) => ({ default: m.AgentGraph })));
const Agents = lazy(() => import("./pages/Agents").then((m) => ({ default: m.Agents })));
const Costs = lazy(() => import("./pages/Costs").then((m) => ({ default: m.Costs })));
const Overview = lazy(() => import("./pages/Overview").then((m) => ({ default: m.Overview })));
const Policy = lazy(() => import("./pages/Policy").then((m) => ({ default: m.Policy })));
const Settings = lazy(() => import("./pages/Settings").then((m) => ({ default: m.Settings })));
const TaskDetail = lazy(() => import("./pages/TaskDetail").then((m) => ({ default: m.TaskDetail })));
const Tasks = lazy(() => import("./pages/Tasks").then((m) => ({ default: m.Tasks })));

/** Route table of the dashboard. */
export function App() {
  return (
    <Routes>
      <Route element={<Layout />}>
        <Route index element={<Suspense fallback={<div className="page"><OverviewSkeleton /></div>}><Overview /></Suspense>} />
        <Route path="tasks" element={<Suspense fallback={<div className="page"><TableSkeleton /></div>}><Tasks /></Suspense>} />
        <Route path="tasks/:taskId" element={<Suspense fallback={<div className="page"><TableSkeleton /></div>}><TaskDetail /></Suspense>} />
        <Route path="graph" element={<Suspense fallback={<div className="page"><TableSkeleton /></div>}><AgentGraph /></Suspense>} />
        <Route path="costs" element={<Suspense fallback={<div className="page"><TableSkeleton /></div>}><Costs /></Suspense>} />
      <Route path="policy" element={<Suspense fallback={<div className="page"><TableSkeleton /></div>}><Policy /></Suspense>} />
        <Route path="agents" element={<Suspense fallback={<div className="page"><TableSkeleton /></div>}><Agents /></Suspense>} />
        <Route path="settings" element={<Suspense fallback={<div className="page"><TableSkeleton /></div>}><Settings /></Suspense>} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  );
}
