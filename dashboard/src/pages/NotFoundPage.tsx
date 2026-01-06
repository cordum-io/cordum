import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";

export function NotFoundPage() {
  return (
    <div className="flex min-h-[60vh] items-center justify-center">
      <Card className="max-w-md text-center">
        <h2 className="font-display text-2xl font-semibold text-ink">Page not found</h2>
        <p className="mt-2 text-sm text-muted">The page you are looking for does not exist.</p>
        <Button className="mt-4" variant="primary" type="button" onClick={() => (window.location.href = "/")}
        >
          Go home
        </Button>
      </Card>
    </div>
  );
}
