import { useNavigate } from "react-router-dom";
import { ShieldOff } from "lucide-react";
import { Card } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { useConfigStore } from "../state/config";
import { usePageTitle } from "../hooks/usePageTitle";

export default function ForbiddenPage() {
  usePageTitle("Access Denied");
  const navigate = useNavigate();
  const logout = useConfigStore((s) => s.logout);

  return (
    <div className="flex min-h-[60vh] items-center justify-center px-4">
      <Card className="w-full max-w-md p-8 text-center">
        <ShieldOff className="mx-auto mb-4 h-14 w-14 text-danger opacity-60" />
        <h1 className="font-display text-4xl font-bold text-ink">403</h1>
        <p className="mt-2 text-sm font-semibold text-ink">Access denied</p>
        <p className="mt-1 text-xs text-muted">
          You don't have permission to view this page. Contact your administrator
          if you believe this is an error.
        </p>
        <div className="mt-6 flex items-center justify-center gap-3">
          <Button variant="outline" onClick={() => navigate("/")}>
            Go to Overview
          </Button>
          <Button
            variant="ghost"
            onClick={() => {
              logout();
              navigate("/login");
            }}
          >
            Log out
          </Button>
        </div>
      </Card>
    </div>
  );
}
