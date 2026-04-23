/*
 * PoliciesOutputPage — Output Policy with Global/Workflow-Scoped/Scanners sub-tabs.
 * Scanner config, output rule management, scanner tester.
 */
import { useState, useMemo, useCallback, useRef, useEffect } from "react";
import { motion, AnimatePresence } from "framer-motion";
import {
  Plus,
  Search,
  Scan,
  GitBranch,
  ChevronDown,
  ChevronRight,
  FlaskConical,
  X,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { PolicyStudioLayout } from "@/components/layout/PolicyStudioLayout";
import { ScopeTabs } from "@/components/policy/ScopeTabs";
import { OutputRuleCard, type OutputRule } from "@/components/policy/OutputRuleCard";
import { Button } from "@/components/ui/Button";
import { Input } from "@/components/ui/Input";
import { Select } from "@/components/ui/Select";
import { EmptyState } from "@/components/ui/EmptyState";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { useOutputRules, useToggleOutputRule } from "@/hooks/useOutputRules";
import type { OutputRule as ApiOutputRule } from "@/types/policy";
import { useOutputPolicyConfig, useUpdateOutputPolicy } from "@/hooks/useOutputPolicy";
import { useWorkflows } from "@/hooks/useWorkflows";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { DialogOverlay } from "@/components/ui/DialogOverlay";
import type { CustomPatternConfig, ScannerOverride } from "@/types/settings";
import { toast } from "sonner";

type ScopeTab = "global" | "workflow" | "scanners";
type DecisionFilter = "all" | "pass" | "quarantine" | "redact";
type SeverityFilter = "all" | "critical" | "high" | "medium" | "low";

// ── Scanner Config ─────────────────────────────────────────────────────

interface ScannerType {
  id: string;
  name: string;
  description: string;
  enabled: boolean;
  types?: { id: string; label: string; enabled: boolean }[];
  action: string;
  confidence: number;
  stats: { findings_7d: number; false_positives_7d: number };
}

const BUILTIN_SCANNERS: ScannerType[] = [
  {
    id: "pii",
    name: "PII Scanner",
    description: "Detects personally identifiable information in outputs",
    enabled: true,
    types: [
      { id: "email", label: "Email", enabled: true },
      { id: "phone", label: "Phone", enabled: true },
      { id: "ssn", label: "SSN", enabled: true },
      { id: "credit_card", label: "Credit Card", enabled: true },
      { id: "address", label: "Address", enabled: true },
      { id: "name", label: "Name", enabled: false },
      { id: "dob", label: "Date of Birth", enabled: false },
    ],
    action: "redact",
    confidence: 0.85,
    stats: { findings_7d: 0, false_positives_7d: 0 },
  },
  {
    id: "secret",
    name: "Secret Scanner",
    description: "Detects exposed secrets, API keys, and credentials",
    enabled: true,
    types: [
      { id: "api_keys", label: "API Keys", enabled: true },
      { id: "passwords", label: "Passwords", enabled: true },
      { id: "tokens", label: "Tokens", enabled: true },
      { id: "private_keys", label: "Private Keys", enabled: true },
      { id: "connection_strings", label: "Connection Strings", enabled: true },
      { id: "aws_credentials", label: "AWS Credentials", enabled: true },
    ],
    action: "quarantine",
    confidence: 0.9,
    stats: { findings_7d: 0, false_positives_7d: 0 },
  },
  {
    id: "injection",
    name: "Injection Scanner",
    description: "Detects prompt injection and jailbreak patterns",
    enabled: true,
    types: [
      { id: "prompt_injection", label: "Prompt Injection", enabled: true },
      { id: "jailbreak", label: "Jailbreak Patterns", enabled: true },
    ],
    action: "quarantine",
    confidence: 0.8,
    stats: { findings_7d: 0, false_positives_7d: 0 },
  },
];

interface CustomPattern {
  id: string;
  name: string;
  regex: string;
  category: string;
  action: string;
  enabled: boolean;
}

// Custom patterns are loaded from output policy config (see appliedCustomPatterns in the page component)

// ── Scanner Tester ─────────────────────────────────────────────────────

function ScannerTester({ scanner, onClose }: { scanner: ScannerType; onClose: () => void }) {
  const [input, setInput] = useState("");
  const [results, setResults] = useState<{ type: string; offset: number; length: number; matched: string; confidence: number }[] | null>(null);

  const runTest = () => {
    // Mock scanner results
    const findings: typeof results = [];
    if (scanner.id === "pii") {
      const emailMatch = input.match(/[\w.-]+@[\w.-]+\.\w+/);
      if (emailMatch) {
        findings.push({ type: "EMAIL", offset: emailMatch.index ?? 0, length: emailMatch[0].length, matched: emailMatch[0], confidence: 0.99 });
      }
      const ssnMatch = input.match(/\d{3}-\d{2}-\d{4}/);
      if (ssnMatch) {
        findings.push({ type: "SSN", offset: ssnMatch.index ?? 0, length: ssnMatch[0].length, matched: ssnMatch[0], confidence: 0.98 });
      }
    } else if (scanner.id === "secret") {
      const apiKeyMatch = input.match(/sk_live_[a-zA-Z0-9]+/);
      if (apiKeyMatch) {
        findings.push({ type: "API_KEY", offset: apiKeyMatch.index ?? 0, length: apiKeyMatch[0].length, matched: apiKeyMatch[0], confidence: 0.99 });
      }
    }
    setResults(findings);
  };

  return (
    <motion.div
      initial={{ opacity: 0, height: 0 }}
      animate={{ opacity: 1, height: "auto" }}
      exit={{ opacity: 0, height: 0 }}
      className="border-t border-border"
    >
      <div className="p-4 space-y-3">
        <div className="flex items-center justify-between">
          <p className="text-xs font-medium text-foreground">Test: {scanner.name}</p>
          <button onClick={onClose} className="p-1 rounded hover:bg-surface-2 text-muted-foreground"><X className="w-3.5 h-3.5" /></button>
        </div>
        <div>
          <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1">Paste output text to test:</label>
          <textarea
            value={input}
            onChange={(e) => setInput(e.target.value)}
            rows={3}
            placeholder="e.g. The user's email is john@acme.com and their SSN is 123-45-6789."
            className="w-full px-3 py-2 text-xs font-mono bg-surface-0 border border-border rounded-md text-foreground placeholder:text-muted-foreground/50 focus:outline-none focus:ring-1 focus:ring-cordum/30 resize-none"
          />
        </div>
        <Button size="sm" onClick={runTest} disabled={!input.trim()}>
          <FlaskConical className="w-3.5 h-3.5 mr-1" />
          Run Test
        </Button>

        {results !== null && (
          <div className="rounded-md bg-surface-0 border border-border p-3">
            <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider mb-2">Results</p>
            {results.length === 0 ? (
              <p className="text-xs text-muted-foreground">No findings detected.</p>
            ) : (
              <div className="space-y-2">
                {results.map((f, i) => (
                  <div key={i} className="text-xs">
                    <p className="font-medium text-foreground">
                      Finding {i + 1}: <span className="text-amber-400">{f.type}</span> at offset {f.offset}, len {f.length}
                    </p>
                    <p className="text-muted-foreground">
                      Matched: "<span className="text-foreground font-mono">{f.matched}</span>" — Confidence: {f.confidence}
                      {f.confidence < scanner.confidence && <span className="text-amber-400 ml-1">(below threshold)</span>}
                    </p>
                  </div>
                ))}
                <div className="pt-2 border-t border-border">
                  <p className="text-xs font-medium text-foreground">
                    Decision: <span className="uppercase text-red-400">{scanner.action}</span> ({results.filter(f => f.confidence >= scanner.confidence).length} findings above threshold)
                  </p>
                </div>
              </div>
            )}
          </div>
        )}
      </div>
    </motion.div>
  );
}

// ── Scanner Accordion ──────────────────────────────────────────────────

function ScannerAccordion({ scanner, onConfigChange }: { scanner: ScannerType; onConfigChange?: (id: string, patch: Partial<ScannerOverride>) => void }) {
  const [expanded, setExpanded] = useState(false);
  const [testing, setTesting] = useState(false);
  const [localEnabled, setLocalEnabled] = useState(scanner.enabled);
  const [localAction, setLocalAction] = useState(scanner.action);
  const [localConfidence, setLocalConfidence] = useState(scanner.confidence);
  const [localTypes, setLocalTypes] = useState(scanner.types ?? []);

  return (
    <div className="instrument-card overflow-hidden">
      <button
        onClick={() => setExpanded(!expanded)}
        className="flex items-center gap-3 w-full px-5 py-3.5 text-left hover:bg-surface-2/30 transition-colors"
      >
        <span className="text-sm font-semibold font-display text-foreground">{scanner.name}</span>
        <span className="ml-auto flex items-center gap-3">
          <label className="flex items-center gap-1.5 text-xs" onClick={(e) => e.stopPropagation()}>
            <input
              type="checkbox"
              checked={localEnabled}
              onChange={(e) => { setLocalEnabled(e.target.checked); onConfigChange?.(scanner.id, { enabled: e.target.checked }); toast.success(`${scanner.name} ${e.target.checked ? "enabled" : "disabled"}`); }}
              className="accent-[var(--color-cordum)]"
            />
            <span className={localEnabled ? "text-emerald-400" : "text-muted-foreground"}>
              {localEnabled ? "Enabled" : "Disabled"}
            </span>
          </label>
          {expanded ? <ChevronDown className="w-4 h-4 text-muted-foreground" /> : <ChevronRight className="w-4 h-4 text-muted-foreground" />}
        </span>
      </button>

      <AnimatePresence>
        {expanded && (
          <motion.div
            initial={{ height: 0, opacity: 0 }}
            animate={{ height: "auto", opacity: 1 }}
            exit={{ height: 0, opacity: 0 }}
            className="overflow-hidden"
          >
            <div className="px-5 pb-4 space-y-4 border-t border-border pt-3">
              <p className="text-xs text-muted-foreground">{scanner.description}</p>

              {/* Types to detect */}
              {localTypes.length > 0 && (
                <div>
                  <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider mb-2">Types to detect:</p>
                  <div className="flex flex-wrap gap-2">
                    {localTypes.map((t) => (
                      <label key={t.id} className="flex items-center gap-1.5 cursor-pointer">
                        <input
                          type="checkbox"
                          checked={t.enabled}
                          onChange={() => {
                            const updated = localTypes.map((lt) => lt.id === t.id ? { ...lt, enabled: !lt.enabled } : lt);
                            setLocalTypes(updated);
                            onConfigChange?.(scanner.id, { enabledTypes: updated.filter((lt) => lt.enabled).map((lt) => lt.id) });
                          }}
                          className="accent-[var(--color-cordum)]"
                        />
                        <span className="text-xs text-foreground">{t.label}</span>
                      </label>
                    ))}
                  </div>
                </div>
              )}

              {/* Action + Confidence */}
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1">Action on detect</label>
                  <Select
                    value={localAction}
                    onChange={(e) => { setLocalAction(e.target.value); onConfigChange?.(scanner.id, { action: e.target.value }); }}
                    options={[
                      { value: "pass", label: "Pass" },
                      { value: "quarantine", label: "Quarantine" },
                      { value: "redact", label: "Redact" },
                    ]}
                    className="w-full"
                  />
                </div>
                <div>
                  <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1">Confidence threshold</label>
                  <div className="flex items-center gap-2">
                    <input
                      type="range"
                      min={0}
                      max={1}
                      step={0.05}
                      value={localConfidence}
                      onChange={(e) => { const v = parseFloat(e.target.value); setLocalConfidence(v); onConfigChange?.(scanner.id, { confidence: v }); }}
                      className="flex-1 accent-[var(--color-cordum)]"
                    />
                    <span className="text-xs font-mono text-foreground w-10 text-right">{localConfidence.toFixed(2)}</span>
                  </div>
                </div>
              </div>

              {/* Stats */}
              <div className="rounded-md bg-surface-0 border border-border p-3">
                <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider mb-1">Stats (7d)</p>
                <p className="text-xs text-foreground">
                  {scanner.stats.findings_7d} findings · {scanner.stats.false_positives_7d} false positives
                  {scanner.stats.findings_7d > 0 && ` (${Math.round((scanner.stats.false_positives_7d / scanner.stats.findings_7d) * 100)}%)`}
                </p>
              </div>

              <Button variant="outline" size="sm" onClick={() => setTesting(!testing)}>
                <FlaskConical className="w-3.5 h-3.5 mr-1" />
                {testing ? "Hide Tester" : "Test Scanner"}
              </Button>
            </div>

            <AnimatePresence>
              {testing && <ScannerTester scanner={scanner} onClose={() => setTesting(false)} />}
            </AnimatePresence>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}

// ── Main Page ──────────────────────────────────────────────────────────

export default function PoliciesOutputPage() {
  const [activeScope, setActiveScope] = useState<ScopeTab>("global");
  const [search, setSearch] = useState("");
  const [decisionFilter, setDecisionFilter] = useState<DecisionFilter>("all");
  const [severityFilter, setSeverityFilter] = useState<SeverityFilter>("all");
  const [selectedWorkflow, setSelectedWorkflow] = useState<string | null>(null);
  const [showInherited, setShowInherited] = useState(true);
  const [addPatternOpen, setAddPatternOpen] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<OutputRule | null>(null);

  const { data: outputRulesData, isLoading } = useOutputRules();
  const { data: workflows } = useWorkflows();
  const { data: outputPolicyConfig } = useOutputPolicyConfig();
  const updateOutputPolicy = useUpdateOutputPolicy();
  const toggleRule = useToggleOutputRule();

  // ── Scanner config persistence ──────────────────────────────────────
  const scannerOverridesRef = useRef<ScannerOverride[]>(outputPolicyConfig?.scannerOverrides ?? []);
  useEffect(() => {
    if (outputPolicyConfig?.scannerOverrides) {
      scannerOverridesRef.current = outputPolicyConfig.scannerOverrides;
    }
  }, [outputPolicyConfig?.scannerOverrides]);
  const saveTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const appliedScanners: ScannerType[] = useMemo(() => {
    const overrides = outputPolicyConfig?.scannerOverrides ?? [];
    return BUILTIN_SCANNERS.map((s) => {
      const ov = overrides.find((o) => o.id === s.id);
      if (!ov) return s;
      const enabledTypes = ov.enabledTypes;
      return {
        ...s,
        enabled: ov.enabled ?? s.enabled,
        action: ov.action ?? s.action,
        confidence: ov.confidence ?? s.confidence,
        types: enabledTypes && s.types
          ? s.types.map((t) => ({ ...t, enabled: enabledTypes.includes(t.id) }))
          : s.types,
      };
    });
  }, [outputPolicyConfig?.scannerOverrides]);

  const handleScannerConfigChange = useCallback((scannerId: string, patch: Partial<ScannerOverride>) => {
    const current = [...scannerOverridesRef.current];
    const idx = current.findIndex((o) => o.id === scannerId);
    if (idx >= 0) {
      current[idx] = { ...current[idx], ...patch };
    } else {
      current.push({ id: scannerId, ...patch });
    }
    scannerOverridesRef.current = current;

    if (saveTimerRef.current) clearTimeout(saveTimerRef.current);
    saveTimerRef.current = setTimeout(() => {
      if (!outputPolicyConfig) return;
      updateOutputPolicy.mutate({ ...outputPolicyConfig, scannerOverrides: scannerOverridesRef.current });
    }, 1000);
  }, [outputPolicyConfig, updateOutputPolicy]);

  // ── Custom patterns from config ─────────────────────────────────────
  const appliedCustomPatterns: CustomPattern[] = useMemo(() => {
    return (outputPolicyConfig?.customPatterns ?? []).map((p) => ({
      id: p.id,
      name: p.name,
      regex: p.regex,
      category: p.category,
      action: p.action,
      enabled: p.enabled,
    }));
  }, [outputPolicyConfig?.customPatterns]);

  // Add Pattern form state
  const [patternName, setPatternName] = useState("");
  const [patternRegex, setPatternRegex] = useState("");
  const [patternCategory, setPatternCategory] = useState("");
  const [patternAction, setPatternAction] = useState("quarantine");
  const [patternTestInput, setPatternTestInput] = useState("");

  const handleAddPattern = useCallback(() => {
    if (!patternName.trim() || !patternRegex.trim()) {
      toast.error("Pattern name and regex are required");
      return;
    }
    if (!outputPolicyConfig) return;

    const newPattern: CustomPatternConfig = {
      id: `cp-${Date.now()}`,
      name: patternName.trim(),
      regex: patternRegex.trim(),
      category: patternCategory.trim() || "custom",
      action: patternAction,
      enabled: true,
    };

    const updatedPatterns = [...(outputPolicyConfig.customPatterns ?? []), newPattern];
    updateOutputPolicy.mutate(
      { ...outputPolicyConfig, customPatterns: updatedPatterns },
      {
        onSuccess: () => {
          toast.success("Custom pattern added");
          setAddPatternOpen(false);
          setPatternName("");
          setPatternRegex("");
          setPatternCategory("");
          setPatternAction("quarantine");
          setPatternTestInput("");
        },
      },
    );
  }, [outputPolicyConfig, updateOutputPolicy, patternName, patternRegex, patternCategory, patternAction]);

  const handleTogglePattern = useCallback((patternId: string) => {
    if (!outputPolicyConfig) return;
    const updatedPatterns = (outputPolicyConfig.customPatterns ?? []).map((p) =>
      p.id === patternId ? { ...p, enabled: !p.enabled } : p,
    );
    updateOutputPolicy.mutate({ ...outputPolicyConfig, customPatterns: updatedPatterns });
  }, [outputPolicyConfig, updateOutputPolicy]);

  const handleDeletePattern = useCallback((patternId: string) => {
    if (!outputPolicyConfig) return;
    const updatedPatterns = (outputPolicyConfig.customPatterns ?? []).filter((p) => p.id !== patternId);
    updateOutputPolicy.mutate(
      { ...outputPolicyConfig, customPatterns: updatedPatterns },
      { onSuccess: () => toast.success("Pattern deleted") },
    );
  }, [outputPolicyConfig, updateOutputPolicy]);

  // Transform output rules to OutputRule shape
  const allOutputRules: OutputRule[] = useMemo(() => {
    const rules = outputRulesData ?? [];
    return rules.map((r: ApiOutputRule): OutputRule => ({
      id: r.id,
      name: r.id,
      description: r.description,
      decision: r.decision ?? "pass",
      severity: r.severity ?? "medium",
      topics: r.topics ?? [],
      scanners: r.scanners ?? [],
      confidence_threshold: undefined,
      enabled: r.enabled ?? true,
      scope: "global",
      workflowId: undefined,
      triggered_7d: r.triggerCount24h ?? 0,
      false_positives_7d: 0,
    }));
  }, [outputRulesData]);

  const globalRules = useMemo(() => allOutputRules.filter((r) => r.scope !== "workflow"), [allOutputRules]);
  const workflowRulesMap = useMemo(() => {
    const m = new Map<string, OutputRule[]>();
    allOutputRules.filter((r) => r.scope === "workflow").forEach((r) => {
      const wfId = r.workflowId ?? "unknown";
      m.set(wfId, [...(m.get(wfId) ?? []), r]);
    });
    return m;
  }, [allOutputRules]);

  const totalScannerPatterns = appliedScanners.reduce((sum, s) => sum + (s.types?.filter((t) => t.enabled).length ?? 1), 0) + appliedCustomPatterns.filter((p) => p.enabled).length;

  const scopeTabs = [
    { id: "global", label: "Global", count: globalRules.length },
    { id: "workflow", label: "Workflow-Scoped", count: workflowRulesMap.size },
    { id: "scanners", label: "Scanners", count: totalScannerPatterns },
  ];

  const filteredGlobalRules = useMemo(() => {
    let filtered = globalRules;
    if (search) {
      const q = search.toLowerCase();
      filtered = filtered.filter((r) =>
        r.name.toLowerCase().includes(q) ||
        r.topics?.some((t) => t.toLowerCase().includes(q)) ||
        r.scanners?.some((s) => s.toLowerCase().includes(q))
      );
    }
    if (decisionFilter !== "all") {
      filtered = filtered.filter((r) => r.decision.toLowerCase() === decisionFilter);
    }
    if (severityFilter !== "all") {
      filtered = filtered.filter((r) => (r.severity?.toLowerCase() ?? "medium") === severityFilter);
    }
    return filtered;
  }, [globalRules, search, decisionFilter, severityFilter]);

  return (
    <PolicyStudioLayout>
      <div>
        <ScopeTabs
          tabs={scopeTabs}
          active={activeScope}
          onChange={(id) => setActiveScope(id as ScopeTab)}
        />

        {/* Global sub-tab */}
        {activeScope === "global" && (
          <motion.div initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.2 }}>
            <div className="flex items-center gap-3 mb-4">
              <div className="w-[280px]">
                <Input value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Search output rules..." icon={<Search className="w-3.5 h-3.5" />} />
              </div>
              <Select
                value={decisionFilter}
                onChange={(e) => setDecisionFilter(e.target.value as DecisionFilter)}
                options={[
                  { value: "all", label: "All Decisions" },
                  { value: "pass", label: "PASS" },
                  { value: "quarantine", label: "QUARANTINE" },
                  { value: "redact", label: "REDACT" },
                ]}
                className="w-40"
              />
              <Select
                value={severityFilter}
                onChange={(e) => setSeverityFilter(e.target.value as SeverityFilter)}
                options={[
                  { value: "all", label: "All Severities" },
                  { value: "critical", label: "CRITICAL" },
                  { value: "high", label: "HIGH" },
                  { value: "medium", label: "MEDIUM" },
                  { value: "low", label: "LOW" },
                ]}
                className="w-40"
              />
              <div className="ml-auto">
                <Button size="sm" onClick={() => toast.info("Create output rule — opening editor")}>
                  <Plus className="w-3.5 h-3.5 mr-1" />
                  New Rule
                </Button>
              </div>
            </div>

            {isLoading ? (
              <div className="space-y-3">{Array.from({ length: 3 }).map((_, i) => <SkeletonCard key={i} />)}</div>
            ) : filteredGlobalRules.length === 0 ? (
              <EmptyState
                icon={<Scan className="w-5 h-5" />}
                title="No output rules"
                description={search || decisionFilter !== "all" || severityFilter !== "all" ? "No rules match your filters." : "Create your first output policy rule to scan agent outputs."}
                action={<Button size="sm" onClick={() => toast.info("Create output rule")}><Plus className="w-3.5 h-3.5 mr-1" />Create Rule</Button>}
              />
            ) : (
              <div className="space-y-3">
                {filteredGlobalRules.map((rule) => (
                  <OutputRuleCard
                    key={rule.id}
                    rule={rule}
                    scope="global"
                    onEdit={() => toast.info("Rule editing requires the Policy Studio editor")}
                    onTest={() => { setActiveScope("scanners"); toast.info("Use the Scanner Tester to test output scanning"); }}
                    onDuplicate={() => toast.info("Rule duplication requires the Policy Studio editor")}
                    onToggle={() => toggleRule.mutate({ id: rule.id, enabled: rule.enabled === false })}
                    onDelete={() => setDeleteTarget(rule)}
                    onViewHistory={() => { window.location.href = `/audit?type=output&rule_id=${encodeURIComponent(rule.id)}`; }}
                  />
                ))}
              </div>
            )}
          </motion.div>
        )}

        {/* Workflow-Scoped sub-tab */}
        {activeScope === "workflow" && (
          <motion.div initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.2 }}>
            <div className="mb-5">
              <p className="text-xs text-muted-foreground mb-3">Select a workflow:</p>
              <div className="flex flex-wrap gap-2">
                {(workflows ?? []).map((wf) => {
                  const wfRules = workflowRulesMap.get(wf.id) ?? [];
                  const isSelected = selectedWorkflow === wf.id;
                  return (
                    <button
                      key={wf.id}
                      onClick={() => setSelectedWorkflow(isSelected ? null : wf.id)}
                      className={cn(
                        "instrument-card px-4 py-2.5 text-left transition-all min-w-[140px]",
                        isSelected ? "border-cordum/40 bg-cordum/5 shadow-sm" : "hover:border-border/80",
                      )}
                    >
                      <p className="text-xs font-medium text-foreground truncate">{wf.name || wf.id}</p>
                      <p className="text-[10px] text-muted-foreground mt-0.5">
                        {wfRules.length > 0 ? `+${wfRules.length} rule${wfRules.length !== 1 ? "s" : ""}` : "0 overrides"}
                      </p>
                      {wfRules.length > 0 && <span className="absolute top-2 right-2 w-2 h-2 rounded-full bg-cordum" />}
                    </button>
                  );
                })}
              </div>
            </div>

            {selectedWorkflow && (
              <div>
                <div className="flex items-center justify-between mb-4">
                  <label className="flex items-center gap-2 cursor-pointer">
                    <input type="checkbox" checked={showInherited} onChange={(e) => setShowInherited(e.target.checked)} className="accent-[var(--color-cordum)]" />
                    <span className="text-xs text-foreground">Show inherited global rules</span>
                  </label>
                  <Button size="sm" onClick={() => toast.info("Create workflow output rule")}>
                    <Plus className="w-3.5 h-3.5 mr-1" />
                    New Rule
                  </Button>
                </div>

                {(() => {
                  const wfRules = workflowRulesMap.get(selectedWorkflow) ?? [];
                  return wfRules.length > 0 ? (
                    <div className="space-y-3 mb-6">
                      <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Workflow Rules ({wfRules.length})</p>
                      {wfRules.map((rule) => (
                        <OutputRuleCard key={rule.id} rule={rule} scope="workflow" scopeLabel={selectedWorkflow} onEdit={() => toast.info("Rule editing requires the Policy Studio editor")} onTest={() => { setActiveScope("scanners"); toast.info("Use the Scanner Tester to test output scanning"); }} onToggle={() => toggleRule.mutate({ id: rule.id, enabled: rule.enabled === false })} onDelete={() => setDeleteTarget(rule)} />
                      ))}
                    </div>
                  ) : (
                    <EmptyState
                      icon={<GitBranch className="w-5 h-5" />}
                      title="No workflow-specific output rules"
                      description="This workflow inherits all global output rules."
                      action={<Button size="sm" onClick={() => toast.info("Add first workflow output rule")}>Add First Workflow Rule</Button>}
                    />
                  );
                })()}

                {showInherited && globalRules.length > 0 && (
                  <div className="space-y-3">
                    <p className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider">Inherited from Global ({globalRules.length} rules)</p>
                    {globalRules.map((rule) => (
                      <OutputRuleCard key={rule.id} rule={rule} scope="inherited" dimmed onEdit={() => { setActiveScope("global"); toast.info(`Navigated to Global tab`); }} />
                    ))}
                  </div>
                )}
              </div>
            )}

            {!selectedWorkflow && (workflows ?? []).length > 0 && (
              <div className="text-center py-12">
                <p className="text-xs text-muted-foreground">Select a workflow above to view its output policy rules.</p>
              </div>
            )}
          </motion.div>
        )}

        {/* Scanners sub-tab */}
        {activeScope === "scanners" && (
          <motion.div initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.2 }}>
            {/* Built-in Scanners */}
            <div className="mb-6">
              <h3 className="text-xs font-semibold text-foreground mb-3">Built-in Scanners</h3>
              <div className="space-y-3">
                {appliedScanners.map((scanner) => (
                  <ScannerAccordion key={scanner.id} scanner={scanner} onConfigChange={handleScannerConfigChange} />
                ))}
              </div>
            </div>

            {/* Custom Patterns */}
            <div>
              <div className="flex items-center justify-between mb-3">
                <h3 className="text-xs font-semibold text-foreground">Custom Patterns</h3>
                <Button size="sm" variant="outline" onClick={() => setAddPatternOpen(true)}>
                  <Plus className="w-3.5 h-3.5 mr-1" />
                  Add Pattern
                </Button>
              </div>

              {appliedCustomPatterns.length === 0 ? (
                <EmptyState
                  icon={<Scan className="w-5 h-5" />}
                  title="No custom patterns"
                  description="Add custom regex patterns to detect domain-specific content."
                  action={<Button size="sm" onClick={() => setAddPatternOpen(true)}>Add Pattern</Button>}
                />
              ) : (
                <div className="rounded-lg border border-border overflow-hidden">
                  <table className="w-full text-xs">
                    <thead>
                      <tr className="bg-surface-0 border-b border-border">
                        <th className="text-left px-4 py-2.5 text-[10px] font-mono text-muted-foreground uppercase tracking-widest">Name</th>
                        <th className="text-left px-4 py-2.5 text-[10px] font-mono text-muted-foreground uppercase tracking-widest">Regex</th>
                        <th className="text-left px-4 py-2.5 text-[10px] font-mono text-muted-foreground uppercase tracking-widest">Category</th>
                        <th className="text-left px-4 py-2.5 text-[10px] font-mono text-muted-foreground uppercase tracking-widest">Action</th>
                        <th className="text-left px-4 py-2.5 text-[10px] font-mono text-muted-foreground uppercase tracking-widest">Enabled</th>
                        <th className="w-10"></th>
                      </tr>
                    </thead>
                    <tbody>
                      {appliedCustomPatterns.map((p) => (
                        <tr key={p.id} className="border-b border-border last:border-0 hover:bg-surface-2/30">
                          <td className="px-4 py-2.5 font-medium text-foreground">{p.name}</td>
                          <td className="px-4 py-2.5 font-mono text-muted-foreground">{p.regex}</td>
                          <td className="px-4 py-2.5">
                            <span className="px-2 py-0.5 rounded bg-surface-2 text-muted-foreground text-[10px]">{p.category}</span>
                          </td>
                          <td className="px-4 py-2.5 uppercase text-muted-foreground">{p.action}</td>
                          <td className="px-4 py-2.5">
                            <input type="checkbox" checked={p.enabled} onChange={() => handleTogglePattern(p.id)} className="accent-[var(--color-cordum)]" />
                          </td>
                          <td className="px-4 py-2.5">
                            <button onClick={() => handleDeletePattern(p.id)} className="p-1 rounded hover:bg-red-500/10 text-muted-foreground hover:text-red-400">
                              <X className="w-3 h-3" />
                            </button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </div>

            {/* Add Pattern Dialog */}
            <DialogOverlay
              open={addPatternOpen}
              onClose={() => setAddPatternOpen(false)}
              label="Add custom pattern"
              className="w-[480px] max-w-[90vw] bg-surface-1 border border-border rounded-xl shadow-2xl"
            >
              <div className="px-6 py-4 border-b border-border flex items-center justify-between">
                <h3 className="font-display font-semibold text-foreground">Add Custom Pattern</h3>
                <button onClick={() => setAddPatternOpen(false)} className="p-1 rounded hover:bg-surface-2 text-muted-foreground"><X className="w-4 h-4" /></button>
              </div>
              <div className="px-6 py-5 space-y-4">
                <div>
                  <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1">Pattern Name</label>
                  <Input value={patternName} onChange={(e) => setPatternName(e.target.value)} placeholder="e.g. Internal IP Address" />
                </div>
                <div>
                  <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1">Regex</label>
                  <Input value={patternRegex} onChange={(e) => setPatternRegex(e.target.value)} placeholder="e.g. 10\.\d{1,3}\.\d{1,3}\.\d{1,3}" className="font-mono" />
                </div>
                <div>
                  <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1">Category</label>
                  <Input value={patternCategory} onChange={(e) => setPatternCategory(e.target.value)} placeholder="e.g. network, secret, pii" />
                </div>
                <div>
                  <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1">Action</label>
                  <Select
                    value={patternAction}
                    onChange={(e) => setPatternAction(e.target.value)}
                    options={[
                      { value: "pass", label: "PASS" },
                      { value: "quarantine", label: "QUARANTINE" },
                      { value: "redact", label: "REDACT" },
                    ]}
                  />
                </div>
                <div>
                  <label className="text-[10px] font-mono text-muted-foreground uppercase tracking-wider block mb-1">Test Input</label>
                  <textarea
                    value={patternTestInput}
                    onChange={(e) => setPatternTestInput(e.target.value)}
                    rows={3}
                    placeholder="Paste sample text to test the pattern..."
                    className="w-full px-3 py-2 text-xs font-mono bg-surface-0 border border-border rounded-md text-foreground placeholder:text-muted-foreground/50 focus:outline-none focus:ring-1 focus:ring-cordum/30 resize-none"
                  />
                  {patternTestInput && patternRegex && (() => {
                    try {
                      const re = new RegExp(patternRegex, "g");
                      const matches = [...patternTestInput.matchAll(re)];
                      return (
                        <p className="text-[10px] font-mono mt-1 text-muted-foreground">
                          {matches.length > 0 ? (
                            <span className="text-amber-400">{matches.length} match{matches.length !== 1 ? "es" : ""} found</span>
                          ) : (
                            "No matches"
                          )}
                        </p>
                      );
                    } catch {
                      return <p className="text-[10px] font-mono mt-1 text-red-400">Invalid regex</p>;
                    }
                  })()}
                </div>
              </div>
              <div className="px-6 py-4 border-t border-border flex justify-end gap-2">
                <Button variant="outline" size="sm" onClick={() => setAddPatternOpen(false)}>Cancel</Button>
                <Button size="sm" onClick={handleAddPattern} disabled={!patternName.trim() || !patternRegex.trim() || updateOutputPolicy.isPending}>
                  {updateOutputPolicy.isPending ? "Saving..." : "Add Pattern"}
                </Button>
              </div>
            </DialogOverlay>
          </motion.div>
        )}
      </div>

      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (deleteTarget) {
            toggleRule.mutate(
              { id: deleteTarget.id, enabled: false },
              { onSuccess: () => { toast.success("Rule disabled"); setDeleteTarget(null); } },
            );
          }
        }}
        title="Disable output rule"
        description={`This will disable the rule "${deleteTarget?.name ?? deleteTarget?.id}". Disabled rules no longer trigger on output scanning.`}
        confirmLabel="Disable Rule"
        variant="destructive"
        loading={toggleRule.isPending}
      />
    </PolicyStudioLayout>
  );
}
