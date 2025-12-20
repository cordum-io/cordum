import { NavLink } from "react-router-dom";
import {
  Boxes,
  Cpu,
  FileText,
  MessageSquare,
  Settings,
  Share2,
  Sparkles,
  Workflow,
} from "lucide-react";

type NavItem = {
  to: string;
  label: string;
  icon: React.ReactNode;
};

const nav: NavItem[] = [
  { to: "/dashboard", label: "Dashboard", icon: <Sparkles className="h-4 w-4" /> },
  { to: "/workflows", label: "Workflows", icon: <Workflow className="h-4 w-4" /> },
  { to: "/runs", label: "Runs", icon: <Boxes className="h-4 w-4" /> },
  { to: "/jobs", label: "Jobs", icon: <FileText className="h-4 w-4" /> },
  { to: "/traces", label: "Traces", icon: <Share2 className="h-4 w-4" /> },
  { to: "/workers", label: "Workers", icon: <Cpu className="h-4 w-4" /> },
  { to: "/chat", label: "Chat", icon: <MessageSquare className="h-4 w-4" /> },
  { to: "/settings", label: "Settings", icon: <Settings className="h-4 w-4" /> },
];

export default function Sidebar() {
  return (
    <aside className="glass w-[240px] border-r border-primary-border">
      <nav className="p-3">
        <div className="mb-2 px-2 text-[11px] uppercase tracking-wider text-tertiary-text">
          Control Plane
        </div>
        <div className="space-y-1">
          {nav.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              className={({ isActive }) =>
                [
                  "flex items-center gap-3 rounded-lg px-3 py-2 text-sm",
                  isActive
                    ? "bg-primary-background/60 text-primary-text"
                    : "text-secondary-text hover:bg-primary-background/40 hover:text-primary-text",
                ].join(" ")
              }
            >
              <span className="text-tertiary-text">{item.icon}</span>
              <span>{item.label}</span>
            </NavLink>
          ))}
        </div>
      </nav>
    </aside>
  );
}
