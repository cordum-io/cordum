import { useNavigate } from "react-router-dom";
import {
  Briefcase,
  Shield,
  CheckSquare,
  Search,
  ScrollText,
  Package,
} from "lucide-react";
import { Card } from "../ui/Card";

// ---------------------------------------------------------------------------
// Action definitions
// ---------------------------------------------------------------------------

const ACTIONS = [
  {
    label: "Jobs",
    description: "View and manage jobs",
    icon: Briefcase,
    path: "/jobs",
  },
  {
    label: "Policy Studio",
    description: "Manage safety policies",
    icon: Shield,
    path: "/govern/overview",
  },
  {
    label: "Approvals",
    description: "Review pending approvals",
    icon: CheckSquare,
    path: "/approvals",
  },
  {
    label: "Search",
    description: "Search across resources",
    icon: Search,
    path: "/search",
  },
  {
    label: "Audit Log",
    description: "View audit trail",
    icon: ScrollText,
    path: "/audit",
  },
  {
    label: "Packs",
    description: "Browse worker packs",
    icon: Package,
    path: "/packs",
  },
] as const;

// ---------------------------------------------------------------------------
// QuickActions
// ---------------------------------------------------------------------------

export function QuickActions() {
  const navigate = useNavigate();

  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
      {ACTIONS.map((action) => {
        const Icon = action.icon;
        return (
          <Card
            key={action.path}
            className="cursor-pointer transition-transform hover:-translate-y-0.5"
            onClick={() => navigate(action.path)}
          >
            <div className="flex flex-col items-center gap-1.5 py-1 text-center">
              <Icon className="h-5 w-5 text-accent" />
              <span className="text-xs font-semibold text-ink">
                {action.label}
              </span>
              <span className="text-xs leading-tight text-muted-foreground">
                {action.description}
              </span>
            </div>
          </Card>
        );
      })}
    </div>
  );
}
