import { Navigate, Route, Routes } from "react-router-dom";
import AppShell from "./AppShell";
import DashboardPage from "../pages/DashboardPage";
import WorkersPage from "../pages/WorkersPage";
import JobsPage from "../pages/JobsPage";
import JobDetailPage from "../pages/JobDetailPage";
import TracesPage from "../pages/TracesPage";
import WorkflowsPage from "../pages/WorkflowsPage";
import RunsPage from "../pages/RunsPage";
import RunDetailPage from "../pages/RunDetailPage";
import ChatPage from "../pages/ChatPage";
import SettingsPage from "../pages/SettingsPage";

export default function App() {
  return (
    <AppShell>
      <Routes>
        <Route path="/" element={<Navigate to="/dashboard" replace />} />
        <Route path="/dashboard" element={<DashboardPage />} />
        <Route path="/workers" element={<WorkersPage />} />
        <Route path="/jobs" element={<JobsPage />} />
        <Route path="/jobs/:id" element={<JobDetailPage />} />
        <Route path="/traces" element={<TracesPage />} />
        <Route path="/workflows" element={<WorkflowsPage />} />
        <Route path="/runs" element={<RunsPage />} />
        <Route path="/runs/:id" element={<RunDetailPage />} />
        <Route path="/chat" element={<ChatPage />} />
        <Route path="/settings" element={<SettingsPage />} />
      </Routes>
    </AppShell>
  );
}
