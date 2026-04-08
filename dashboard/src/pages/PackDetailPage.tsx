import { useNavigate, useParams } from "react-router-dom";
import PackDetail from "@/components/packs/PackDetail";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { Button } from "@/components/ui/Button";

export default function PackDetailPage() {
  const navigate = useNavigate();
  const { id } = useParams<{ id: string }>();

  if (!id) {
    return (
      <div className="space-y-4">
        <ErrorBanner
          title="Pack not found"
          message="This page is missing a pack ID. Return to the packs list and open a pack from there."
        />
        <div className="flex justify-center">
          <Button
            variant="outline"
            size="sm"
            onClick={() => navigate("/packs")}
          >
            Back to packs
          </Button>
        </div>
      </div>
    );
  }

  return <PackDetail packId={id} onClose={() => navigate("/packs")} />;
}
