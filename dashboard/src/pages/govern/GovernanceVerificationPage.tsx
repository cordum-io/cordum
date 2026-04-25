import { motion } from "framer-motion";
import { ShieldCheck } from "lucide-react";
import { ChainIntegrityWidget } from "@/components/ChainIntegrityWidget";
import { GapAlertBanner } from "@/components/GapAlertBanner";
import { PageHeader } from "@/components/layout/PageHeader";
import { EmptyState } from "@/components/ui/EmptyState";
import { RequireRole } from "@/components/RequireRole";
import { useConfigStore } from "@/state/config";

export default function GovernanceVerificationPage() {
  const tenantId = useConfigStore((s) => s.tenantId) || "default";

  return (
    <RequireRole
      roles={["admin"]}
      fallback={
        <motion.div
          initial={{ opacity: 0, y: 8 }}
          animate={{ opacity: 1, y: 0 }}
          className="space-y-6"
        >
          <PageHeader
            label="Govern"
            title="Chain Verification"
            subtitle="Audit-chain integrity monitoring."
          />
          <EmptyState
            icon={<ShieldCheck className="w-5 h-5" />}
            title="Admin role required"
            description="Chain verification is restricted to administrators. Ask an admin on your team to run a chain integrity check."
          />
        </motion.div>
      }
    >
      <motion.div
        initial={{ opacity: 0, y: 8 }}
        animate={{ opacity: 1, y: 0 }}
        className="space-y-6"
      >
        <PageHeader
          label="Govern"
          title="Chain Verification"
          subtitle="Audit-chain integrity monitoring. Confirms the Merkle chain of audit events is unbroken end-to-end and surfaces any retention or tamper gaps."
        />

        <GapAlertBanner tenant={tenantId} />

        <ChainIntegrityWidget tenant={tenantId} />
      </motion.div>
    </RequireRole>
  );
}
