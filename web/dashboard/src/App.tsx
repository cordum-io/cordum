import { BrowserRouter as Router, Routes, Route, Link, useLocation } from 'react-router-dom';
import MissionControl from './pages/MissionControl';
import WorkerMesh from './pages/WorkerMesh';
import JobExplorer from './pages/JobExplorer';
import Chat from './pages/Chat';
import RepoReview from './pages/RepoReview';
import { LayoutDashboard, Server, Search, Activity, MessageSquare, GitBranch } from 'lucide-react';
import clsx from 'clsx';

function NavItem({ to, icon: Icon, label }: { to: string; icon: any; label: string }) {
  const location = useLocation();
  const isActive = location.pathname === to;

  return (
    <li>
      <Link
        to={to}
        className={clsx(
          "flex items-center gap-3 px-3 py-2 rounded-md text-sm font-medium transition-colors duration-150",
          isActive 
            ? "bg-indigo-600/20 text-indigo-400 border border-indigo-600/30" 
            : "text-slate-400 hover:text-slate-200 hover:bg-slate-800"
        )}
      >
        <Icon size={16} />
        {label}
      </Link>
    </li>
  );
}

function Sidebar() {
  return (
    <aside className="w-64 bg-slate-900 border-r border-slate-800 flex flex-col">
      <div className="p-4 border-b border-slate-800 flex items-center gap-2">
        <div className="h-8 w-8 bg-indigo-600 rounded flex items-center justify-center shadow-lg shadow-indigo-500/20">
            <Activity className="text-white" size={20} />
        </div>
        <div>
          <h1 className="text-sm font-bold text-slate-100 tracking-wide">CORTEX<span className="text-indigo-500">OS</span></h1>
          <div className="text-[10px] text-slate-500 font-mono">CONTROLLER v1.0</div>
        </div>
      </div>

      <nav className="flex-1 p-4">
        <div className="text-[10px] font-bold text-slate-600 uppercase tracking-wider mb-2 pl-2">Platform</div>
        <ul className="space-y-1">
          <NavItem to="/" icon={LayoutDashboard} label="Mission Control" />
          <NavItem to="/workers" icon={Server} label="Worker Mesh" />
          <NavItem to="/jobs" icon={Search} label="Job Explorer" />
          <NavItem to="/repo-review" icon={GitBranch} label="Repo Review" />
        </ul>

        <div className="text-[10px] font-bold text-slate-600 uppercase tracking-wider mb-2 pl-2 mt-8">Apps</div>
        <ul className="space-y-1">
             <NavItem to="/chat" icon={MessageSquare} label="Cortex Chat" />
        </ul>
      </nav>

      <div className="p-4 border-t border-slate-800">
        <div className="flex items-center gap-3">
          <div className="h-8 w-8 rounded-full bg-slate-800 flex items-center justify-center text-xs font-bold text-slate-400 border border-slate-700">
            OP
          </div>
          <div>
            <div className="text-sm font-medium text-slate-200">Operator</div>
            <div className="text-xs text-slate-500">admin@cortex</div>
          </div>
        </div>
      </div>
    </aside>
  );
}

function App() {
  return (
    <Router>
      <div className="flex h-screen bg-slate-950 text-slate-200 font-sans selection:bg-indigo-500/30">
        <Sidebar />
        <main className="flex-1 overflow-auto bg-slate-950">
          <div className="max-w-7xl mx-auto">
             <Routes>
              <Route path="/" element={<MissionControl />} />
              <Route path="/workers" element={<WorkerMesh />} />
              <Route path="/jobs" element={<JobExplorer />} />
              <Route path="/chat" element={<Chat />} />
              <Route path="/repo-review" element={<RepoReview />} />
            </Routes>
          </div>
        </main>
      </div>
    </Router>
  );
}

export default App;
