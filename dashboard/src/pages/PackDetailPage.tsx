import { useNavigate, useParams } from "react-router-dom";
import { motion } from "framer-motion";
import PackDetail from "@/components/packs/PackDetail";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { Button } from "@/components/ui/Button";

const pageEnterMotion = {
  initial: { opacity: 0, y: 12 },
  animate: { opacity: 1, y: 0 },
  transition: { duration: 0.3 },
} as const;

export default function PackDetailPage() {
  const navigate = useNavigate();
  const { id } = useParams<{ id: string }>();

  if (!id) {
    return (
      <motion.div className="instrument-card space-y-4 p-6" {...pageEnterMotion}>
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
      </motion.div>
    );
  }

  return (
    <motion.div {...pageEnterMotion}>
      <PackDetail packId={id} onClose={() => navigate("/packs")} />
    </motion.div>
  );
}
