import { GlobalYamlPane } from "@/components/policy/global/GlobalYamlPane";
import type { GlobalPolicyParseIssue } from "@/types/policy";

interface InputRulesYamlPaneProps {
  yaml: string;
  editable: boolean;
  activeRuleId?: string | null;
  parseIssues: GlobalPolicyParseIssue[];
  onChange: (nextYaml: string) => void;
}

export function InputRulesYamlPane({
  yaml,
  editable,
  activeRuleId,
  parseIssues,
  onChange,
}: InputRulesYamlPaneProps) {
  return (
    <div className="space-y-3">
      {!editable && (
        <div className="rounded-2xl border border-[var(--color-warning)]/30 bg-[var(--color-warning)]/10 px-3 py-2 text-xs text-[var(--color-warning)]">
          YAML is read-only for viewer role.
        </div>
      )}
      <GlobalYamlPane
        yaml={yaml}
        editable={editable}
        activeRuleId={activeRuleId}
        parseIssues={parseIssues}
        onChange={onChange}
      />
    </div>
  );
}
