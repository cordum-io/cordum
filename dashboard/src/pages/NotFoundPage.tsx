import { useNavigate } from "react-router-dom";
import { FileQuestion } from "lucide-react";
import { Card } from "../components/ui/Card";
import { Button } from "../components/ui/Button";
import { usePageTitle } from "../hooks/usePageTitle";

export default function NotFoundPage() {
  usePageTitle("Not Found");
  const navigate = useNavigate();

  return (
    <div className="flex min-h-[60vh] items-center justify-center px-4">
      <Card className="w-full max-w-md p-8 text-center">
        <FileQuestion className="mx-auto mb-4 h-14 w-14 text-muted opacity-60" />
        <h1 className="font-display text-4xl font-bold text-ink">404</h1>
        <p className="mt-2 text-sm font-semibold text-ink">Page not found</p>
        <p className="mt-1 text-xs text-muted">
          The page you're looking for doesn't exist or has been moved.
        </p>
        <Button
          variant="outline"
          className="mt-6"
          onClick={() => navigate("/")}
        >
          Go to Overview
        </Button>
      </Card>
    </div>
  );
}
