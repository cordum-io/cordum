import { Package } from "lucide-react";
import { EmptyState } from "@/components/ui/EmptyState";
import { BundleListItem } from "./BundleListItem";
import type { PolicyBundle } from "@/api/types";

interface BundleListProps {
  bundles: PolicyBundle[];
  canPublish: boolean;
  onOpenBundle: (bundleId: string) => void;
}

export function BundleList({ bundles, canPublish, onOpenBundle }: BundleListProps) {
  if (bundles.length === 0) {
    return (
      <EmptyState
        icon={<Package className="h-6 w-6" />}
        title="No policy bundles found"
        description="No bundles are currently returned by the policy API."
      />
    );
  }

  return (
    <section className="space-y-3">
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {bundles.map((bundle) => (
          <BundleListItem
            key={bundle.id}
            bundle={bundle}
            canPublish={canPublish}
            onOpen={onOpenBundle}
          />
        ))}
      </div>
    </section>
  );
}
