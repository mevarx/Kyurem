import { Navigate, Route, Routes } from "react-router-dom";
import { Layout } from "./components/Layout";
import { AgentGraph } from "./pages/AgentGraph";
import { Agents } from "./pages/Agents";
import { Costs } from "./pages/Costs";
import { Overview } from "./pages/Overview";
import { Settings } from "./pages/Settings";
import { TaskDetail } from "./pages/TaskDetail";
import { Tasks } from "./pages/Tasks";

/** Route table of the dashboard. */
export function App() {
  return (
    <Routes>
      <Route element={<Layout />}>
        <Route index element={<Overview />} />
        <Route path="tasks" element={<Tasks />} />
        <Route path="tasks/:taskId" element={<TaskDetail />} />
        <Route path="graph" element={<AgentGraph />} />
        <Route path="costs" element={<Costs />} />
        <Route path="agents" element={<Agents />} />
        <Route path="settings" element={<Settings />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  );
}
