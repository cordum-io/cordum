import { useCallback } from "react";
import { useNavigate } from "react-router-dom";
import { RulesTable } from "../components/policy/RulesTable";
import type { PolicyRule } from "../api/types";

export default function PoliciesRulesPage() {
  const navigate = useNavigate();

  const handleSelectRule = useCallback(
    (rule: PolicyRule) => {
      const source = rule.source as Record<string, unknown> | undefined;
      const bundleId =
        source && typeof source === "object" && "fragment_id" in source
          ? String(source.fragment_id ?? "").trim()
          : "";
      if (bundleId) {
        navigate(`/policies/rules/new?bundle=${encodeURIComponent(bundleId)}`);
      } else {
        navigate("/policies/rules/new");
      }
    },
    [navigate],
  );

  return (
    <RulesTable onSelectRule={handleSelectRule} />
  );
}
