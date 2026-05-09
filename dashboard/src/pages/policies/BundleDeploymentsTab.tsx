import { Layers } from "lucide-react";
import { EmptyState } from "@/components/ui/EmptyState";

interface BundleDeploymentsTabProps {
  bundleId: string;
}

/**
 * Bundle detail — Deployments tab (Dashboard 5 step-7 placeholder).
 *
 * Step-7 ships the scope×version matrix + Promote/Rollback ConfirmDialog
 * mutations once Backend 2's `DeployVersionToScope` /
 * `RollbackDeployment` endpoints land. This placeholder keeps the tab
 * route + URL contract stable so deep-links work in the meantime.
 */
export default function BundleDeploymentsTab({ bundleId }: BundleDeploymentsTabProps) {
  return (
    <EmptyState
      icon={<Layers className="h-5 w-5" />}
      title="Deployments matrix coming online"
      description={`The scope×version matrix + Promote/Rollback flows ship with Dashboard 5 step-7 once Backend 2's deploy endpoints land. Bundle id: ${bundleId}.`}
    />
  );
}
