import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ApiError } from "@/api/client";
import type { PolicyBundle } from "@/api/types";
import {
  createDefaultGlobalPolicyDocument,
  parseGlobalPolicyYaml,
  serializeGlobalPolicyYaml,
} from "@/lib/policy-studio/globalPolicy";
import type {
  GlobalPolicyDefaultDecision,
  GlobalPolicyDocument,
  GlobalPolicyInputRule,
  GlobalPolicyParseIssue,
} from "@/types/policy";
import { usePolicyBundle, usePolicyBundles, useSimulatePolicy, useUpdatePolicyBundle } from "./usePolicies";

export type PolicyStudioGlobalErrorCode =
  | "yaml_validation"
  | "conflict"
  | "request_failed"
  | "unknown";

export interface PolicyStudioGlobalError {
  code: PolicyStudioGlobalErrorCode;
  message: string;
  status?: number;
  details?: string;
}

export interface PolicyStudioGlobalSaveResult {
  ok: boolean;
  error?: PolicyStudioGlobalError;
}

export interface PolicyStudioGlobalReorderResult {
  ok: boolean;
  error?: PolicyStudioGlobalError;
}

export interface PolicyStudioTenantMcpPolicy {
  allowServers: string[];
  denyServers: string[];
  allowTools: string[];
  denyTools: string[];
  allowResources: string[];
  denyResources: string[];
  allowActions: string[];
  denyActions: string[];
}

export interface PolicyStudioTenantPolicy {
  id: string;
  label?: string;
  allowTopics: string[];
  denyTopics: string[];
  allowedRepoHosts: string[];
  deniedRepoHosts: string[];
  maxConcurrentJobs?: number;
  mcp: PolicyStudioTenantMcpPolicy;
}

type PolicyStudioSetTenantPolicyInput =
  | PolicyStudioTenantPolicy
  | ((previous: PolicyStudioTenantPolicy | null) => PolicyStudioTenantPolicy | null)
  | null;

type PolicyStudioAction = "save" | "simulate";

function normalizeYaml(value: string): string {
  return value.replaceAll("\r\n", "\n").trim();
}

function readBundleContent(bundle?: PolicyBundle): string {
  if (!bundle) return "";
  return typeof bundle.content === "string" ? bundle.content : "";
}

function readErrorDetails(body: unknown): string | undefined {
  if (!body || typeof body !== "object") {
    return undefined;
  }
  const record = body as Record<string, unknown>;
  const details = [record.error, record.message, record.details]
    .map((value) => (typeof value === "string" ? value.trim() : ""))
    .filter(Boolean);
  return details[0];
}

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function toStringArray(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value
    .map((entry) => (typeof entry === "string" ? entry.trim() : ""))
    .filter(Boolean);
}

function dedupeStrings(values: string[]): string[] {
  const seen = new Set<string>();
  const unique: string[] = [];
  for (const value of values) {
    const trimmed = value.trim();
    if (!trimmed) continue;
    const normalized = trimmed.toLowerCase();
    if (seen.has(normalized)) continue;
    seen.add(normalized);
    unique.push(trimmed);
  }
  return unique;
}

function parseOptionalInteger(value: unknown): number | undefined {
  if (typeof value === "number" && Number.isFinite(value)) {
    return Math.max(0, Math.floor(value));
  }
  if (typeof value === "string" && value.trim()) {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) {
      return Math.max(0, Math.floor(parsed));
    }
  }
  return undefined;
}

function defaultTenantMcpPolicy(): PolicyStudioTenantMcpPolicy {
  return {
    allowServers: [],
    denyServers: [],
    allowTools: [],
    denyTools: [],
    allowResources: [],
    denyResources: [],
    allowActions: [],
    denyActions: [],
  };
}

function parseTenantPolicy(
  tenantId: string,
  rawValue: unknown,
): PolicyStudioTenantPolicy {
  const tenant = asRecord(rawValue);
  const mcp = asRecord(tenant.mcp);
  return {
    id: tenantId,
    label:
      (typeof tenant.label === "string" && tenant.label.trim()) ||
      (typeof tenant.name === "string" && tenant.name.trim()) ||
      undefined,
    allowTopics: dedupeStrings(toStringArray(tenant.allow_topics)),
    denyTopics: dedupeStrings(toStringArray(tenant.deny_topics)),
    allowedRepoHosts: dedupeStrings(toStringArray(tenant.allowed_repo_hosts)),
    deniedRepoHosts: dedupeStrings(toStringArray(tenant.denied_repo_hosts)),
    maxConcurrentJobs: parseOptionalInteger(tenant.max_concurrent_jobs),
    mcp: {
      allowServers: dedupeStrings(toStringArray(mcp.allow_servers)),
      denyServers: dedupeStrings(toStringArray(mcp.deny_servers)),
      allowTools: dedupeStrings(toStringArray(mcp.allow_tools)),
      denyTools: dedupeStrings(toStringArray(mcp.deny_tools)),
      allowResources: dedupeStrings(toStringArray(mcp.allow_resources)),
      denyResources: dedupeStrings(toStringArray(mcp.deny_resources)),
      allowActions: dedupeStrings(toStringArray(mcp.allow_actions)),
      denyActions: dedupeStrings(toStringArray(mcp.deny_actions)),
    },
  };
}

function parseTenantPoliciesFromSourceRoot(
  sourceRoot: Record<string, unknown>,
): Record<string, PolicyStudioTenantPolicy> {
  const tenantsRoot = asRecord(sourceRoot.tenants);
  const out: Record<string, PolicyStudioTenantPolicy> = {};

  for (const [rawId, rawTenant] of Object.entries(tenantsRoot)) {
    const tenantId = rawId.trim();
    if (!tenantId) continue;
    out[tenantId] = parseTenantPolicy(tenantId, rawTenant);
  }

  return out;
}

function setStringArrayField(
  target: Record<string, unknown>,
  key: string,
  values: string[],
) {
  if (values.length > 0) {
    target[key] = values;
  } else {
    delete target[key];
  }
}

function cloneRecordDeep(value: Record<string, unknown>): Record<string, unknown> {
  if (typeof structuredClone === "function") {
    return structuredClone(value);
  }
  return JSON.parse(JSON.stringify(value)) as Record<string, unknown>;
}

function describeParseIssues(issues: GlobalPolicyParseIssue[]): string | undefined {
  const errors = issues.filter((issue) => issue.severity === "error");
  if (errors.length === 0) return undefined;
  return errors
    .slice(0, 2)
    .map((issue) => {
      const line = typeof issue.line === "number" ? `line ${issue.line}` : "line ?";
      const column = typeof issue.column === "number" ? `, col ${issue.column}` : "";
      return `${line}${column}: ${issue.message}`;
    })
    .join(" | ");
}

function mapLoadError(error: unknown): PolicyStudioGlobalError {
  if (error instanceof ApiError) {
    return {
      code: "request_failed",
      status: error.status,
      message: "Failed to load policy bundle data.",
      details: readErrorDetails(error.body) ?? error.message,
    };
  }
  if (error instanceof Error) {
    return {
      code: "request_failed",
      message: "Failed to load policy bundle data.",
      details: error.message,
    };
  }
  return {
    code: "request_failed",
    message: "Failed to load policy bundle data.",
  };
}

function moveRule<T>(
  rules: T[],
  from: number,
  to: number,
): T[] {
  const next = [...rules];
  const [picked] = next.splice(from, 1);
  next.splice(to, 0, picked);
  return next;
}

function mapPolicyStudioError(
  error: unknown,
  action: PolicyStudioAction,
): PolicyStudioGlobalError {
  const actionVerb = action === "save" ? "saving" : "running simulation";
  if (error instanceof ApiError) {
    const details = readErrorDetails(error.body);
    if (error.status === 409) {
      return {
        code: "conflict",
        status: error.status,
        message: "Policy bundle changed on the server. Refresh and retry.",
        details,
      };
    }
    if (error.status === 400 || error.status === 422) {
      return {
        code: "yaml_validation",
        status: error.status,
        message:
          action === "save"
            ? "Policy YAML failed validation. Fix the YAML and retry saving."
            : "Simulation request failed validation. Check the request and YAML before retrying.",
        details: details ?? error.message,
      };
    }
    return {
      code: "request_failed",
      status: error.status,
      message: `Request failed while ${actionVerb}.`,
      details: details ?? error.message,
    };
  }
  if (error instanceof Error) {
    return { code: "unknown", message: `Unexpected error while ${actionVerb}.`, details: error.message };
  }
  return { code: "unknown", message: `Unexpected policy studio error while ${actionVerb}.` };
}

export function usePolicyStudioGlobal(initialBundleId = "") {
  const bundlesQuery = usePolicyBundles();
  const updateBundleMutation = useUpdatePolicyBundle();
  const simulateMutation = useSimulatePolicy();

  const bundles = bundlesQuery.data?.items ?? [];
  const [selectedBundleId, setSelectedBundleId] = useState(initialBundleId);
  const [policy, setPolicyState] = useState<GlobalPolicyDocument>(
    createDefaultGlobalPolicyDocument(),
  );
  const [yamlDraft, setYamlDraftState] = useState("");
  const [originalYaml, setOriginalYaml] = useState("");
  const [parseIssues, setParseIssues] = useState<GlobalPolicyParseIssue[]>([]);
  const [saveError, setSaveError] = useState<PolicyStudioGlobalError | null>(null);
  const [simulateError, setSimulateError] = useState<PolicyStudioGlobalError | null>(null);

  const lastLoadedSignatureRef = useRef("");

  useEffect(() => {
    if (selectedBundleId || bundles.length === 0) return;
    setSelectedBundleId(bundles[0].id);
  }, [bundles, selectedBundleId]);

  const selectedBundleSummary = useMemo(
    () => bundles.find((bundle) => bundle.id === selectedBundleId),
    [bundles, selectedBundleId],
  );

  const bundleDetailQuery = usePolicyBundle(selectedBundleId);
  const selectedBundle = bundleDetailQuery.data ?? selectedBundleSummary;
  const selectedBundleContent = readBundleContent(bundleDetailQuery.data) || readBundleContent(selectedBundleSummary);
  const loadError = useMemo(() => {
    if (bundlesQuery.error) return mapLoadError(bundlesQuery.error);
    if (bundleDetailQuery.error) return mapLoadError(bundleDetailQuery.error);
    return null;
  }, [bundlesQuery.error, bundleDetailQuery.error]);
  const tenantPolicies = useMemo(
    () => parseTenantPoliciesFromSourceRoot(policy.sourceRoot),
    [policy.sourceRoot],
  );

  useEffect(() => {
    if (!selectedBundleId) return;
    if (bundleDetailQuery.isLoading && !selectedBundleContent) return;

    const parsed = parseGlobalPolicyYaml(selectedBundleContent);
    const hydratedYaml = selectedBundleContent.trim()
      ? selectedBundleContent
      : serializeGlobalPolicyYaml(parsed.policy);
    const signature = `${selectedBundleId}:${normalizeYaml(hydratedYaml)}`;
    if (signature === lastLoadedSignatureRef.current) return;

    lastLoadedSignatureRef.current = signature;
    setPolicyState(parsed.policy);
    setYamlDraftState(hydratedYaml);
    setOriginalYaml(hydratedYaml);
    setParseIssues(parsed.issues);
    setSaveError(null);
    setSimulateError(null);
  }, [
    bundleDetailQuery.isLoading,
    selectedBundleContent,
    selectedBundleId,
  ]);

  const updatePolicy = useCallback(
    (
      next:
        | GlobalPolicyDocument
        | ((previous: GlobalPolicyDocument) => GlobalPolicyDocument),
    ) => {
      setPolicyState((previous) => {
        const resolved = typeof next === "function" ? next(previous) : next;
        setYamlDraftState(serializeGlobalPolicyYaml(resolved));
        setParseIssues([]);
        return resolved;
      });
    },
    [],
  );

  const setDefaultDecision = useCallback(
    (next: GlobalPolicyDefaultDecision) => {
      updatePolicy((previous) => ({ ...previous, defaultDecision: next }));
    },
    [updatePolicy],
  );

  const setInputRules = useCallback(
    (
      next:
        | GlobalPolicyInputRule[]
        | ((previous: GlobalPolicyInputRule[]) => GlobalPolicyInputRule[]),
    ) => {
      updatePolicy((previous) => {
        const resolved = typeof next === "function" ? next(previous.rules) : next;
        return { ...previous, rules: resolved };
      });
    },
    [updatePolicy],
  );

  const reorderInputRules = useCallback(
    (from: number, to: number): PolicyStudioGlobalReorderResult => {
      if (from === to) {
        return { ok: true };
      }
      if (
        from < 0
        || to < 0
        || from >= policy.rules.length
        || to >= policy.rules.length
      ) {
        return {
          ok: false,
          error: {
            code: "request_failed",
            message: "Unable to reorder input rules: target position is out of bounds.",
          },
        };
      }

      setInputRules(moveRule(policy.rules, from, to));
      return { ok: true };
    },
    [policy.rules, setInputRules],
  );

  const setOutputPolicy = useCallback(
    (
      next:
        | GlobalPolicyDocument["outputPolicy"]
        | ((previous: GlobalPolicyDocument["outputPolicy"]) => GlobalPolicyDocument["outputPolicy"]),
    ) => {
      updatePolicy((previous) => ({
        ...previous,
        outputPolicy: typeof next === "function" ? next(previous.outputPolicy) : next,
      }));
    },
    [updatePolicy],
  );

  const setOutputRules = useCallback(
    (
      next:
        | GlobalPolicyDocument["outputRules"]
        | ((previous: GlobalPolicyDocument["outputRules"]) => GlobalPolicyDocument["outputRules"]),
    ) => {
      updatePolicy((previous) => ({
        ...previous,
        outputRules: typeof next === "function" ? next(previous.outputRules) : next,
      }));
    },
    [updatePolicy],
  );

  const setTenantPolicy = useCallback(
    (
      tenantId: string,
      nextInput: PolicyStudioSetTenantPolicyInput,
    ): PolicyStudioGlobalReorderResult => {
      const normalizedTenantId = tenantId.trim();
      if (!normalizedTenantId) {
        return {
          ok: false,
          error: {
            code: "request_failed",
            message: "Unable to update tenant policy: tenant id is required.",
          },
        };
      }

      updatePolicy((previous) => {
        const sourceRoot = cloneRecordDeep(asRecord(previous.sourceRoot));
        const tenantsRoot = cloneRecordDeep(asRecord(sourceRoot.tenants));
        const existingTenantRaw = tenantsRoot[normalizedTenantId];
        const previousTenant = existingTenantRaw
          ? parseTenantPolicy(normalizedTenantId, existingTenantRaw)
          : null;
        const resolved =
          typeof nextInput === "function"
            ? nextInput(previousTenant)
            : nextInput;

        if (resolved === null) {
          delete tenantsRoot[normalizedTenantId];
        } else {
          const tenantRoot = cloneRecordDeep(asRecord(existingTenantRaw));
          if (resolved.label?.trim()) {
            tenantRoot.label = resolved.label.trim();
          } else {
            delete tenantRoot.label;
          }

          setStringArrayField(
            tenantRoot,
            "allow_topics",
            dedupeStrings(resolved.allowTopics),
          );
          setStringArrayField(
            tenantRoot,
            "deny_topics",
            dedupeStrings(resolved.denyTopics),
          );
          setStringArrayField(
            tenantRoot,
            "allowed_repo_hosts",
            dedupeStrings(resolved.allowedRepoHosts),
          );
          setStringArrayField(
            tenantRoot,
            "denied_repo_hosts",
            dedupeStrings(resolved.deniedRepoHosts),
          );

          if (
            typeof resolved.maxConcurrentJobs === "number"
            && Number.isFinite(resolved.maxConcurrentJobs)
          ) {
            tenantRoot.max_concurrent_jobs = Math.max(
              0,
              Math.floor(resolved.maxConcurrentJobs),
            );
          } else {
            delete tenantRoot.max_concurrent_jobs;
          }

          const mcp = cloneRecordDeep(asRecord(tenantRoot.mcp));
          const mcpInput = resolved.mcp ?? defaultTenantMcpPolicy();
          setStringArrayField(mcp, "allow_servers", dedupeStrings(mcpInput.allowServers));
          setStringArrayField(mcp, "deny_servers", dedupeStrings(mcpInput.denyServers));
          setStringArrayField(mcp, "allow_tools", dedupeStrings(mcpInput.allowTools));
          setStringArrayField(mcp, "deny_tools", dedupeStrings(mcpInput.denyTools));
          setStringArrayField(mcp, "allow_resources", dedupeStrings(mcpInput.allowResources));
          setStringArrayField(mcp, "deny_resources", dedupeStrings(mcpInput.denyResources));
          setStringArrayField(mcp, "allow_actions", dedupeStrings(mcpInput.allowActions));
          setStringArrayField(mcp, "deny_actions", dedupeStrings(mcpInput.denyActions));
          if (Object.keys(mcp).length > 0) {
            tenantRoot.mcp = mcp;
          } else {
            delete tenantRoot.mcp;
          }

          tenantsRoot[normalizedTenantId] = tenantRoot;
        }

        if (Object.keys(tenantsRoot).length > 0) {
          sourceRoot.tenants = tenantsRoot;
        } else {
          delete sourceRoot.tenants;
        }

        return {
          ...previous,
          sourceRoot,
        };
      });

      return { ok: true };
    },
    [updatePolicy],
  );

  const toggleOutputRuleEnabled = useCallback(
    (index: number): PolicyStudioGlobalReorderResult => {
      if (index < 0 || index >= policy.outputRules.length) {
        return {
          ok: false,
          error: {
            code: "request_failed",
            message: "Unable to toggle output rule: index is out of bounds.",
          },
        };
      }

      setOutputRules((previous) =>
        previous.map((rule, ruleIndex) =>
          ruleIndex === index ? { ...rule, enabled: !rule.enabled } : rule,
        ),
      );
      return { ok: true };
    },
    [policy.outputRules.length, setOutputRules],
  );

  const reorderOutputRules = useCallback(
    (from: number, to: number): PolicyStudioGlobalReorderResult => {
      if (from === to) {
        return { ok: true };
      }
      if (
        from < 0
        || to < 0
        || from >= policy.outputRules.length
        || to >= policy.outputRules.length
      ) {
        return {
          ok: false,
          error: {
            code: "request_failed",
            message: "Unable to reorder output rules: target position is out of bounds.",
          },
        };
      }

      setOutputRules(moveRule(policy.outputRules, from, to));
      return { ok: true };
    },
    [policy.outputRules, setOutputRules],
  );

  const setYamlDraft = useCallback((nextYaml: string) => {
    setYamlDraftState(nextYaml);
    const parsed = parseGlobalPolicyYaml(nextYaml);
    setParseIssues(parsed.issues);
    if (parsed.valid) {
      setPolicyState(parsed.policy);
    }
  }, []);

  const save = useCallback(async (): Promise<PolicyStudioGlobalSaveResult> => {
    if (!selectedBundleId.trim()) {
      const error: PolicyStudioGlobalError = {
        code: "request_failed",
        message: "Select a policy bundle before saving.",
      };
      setSaveError(error);
      return { ok: false, error };
    }

    const parsed = parseGlobalPolicyYaml(yamlDraft);
    setParseIssues(parsed.issues);
    if (!parsed.valid) {
      const details = describeParseIssues(parsed.issues);
      const error: PolicyStudioGlobalError = {
        code: "yaml_validation",
        message: "Policy YAML has validation errors. Fix them before saving.",
        details,
      };
      setSaveError(error);
      return { ok: false, error };
    }

    const contentToPersist = yamlDraft.trim()
      ? yamlDraft
      : serializeGlobalPolicyYaml(parsed.policy);

    try {
      await updateBundleMutation.mutateAsync({
        id: selectedBundleId,
        content: contentToPersist,
      });
      setSaveError(null);
      setOriginalYaml(contentToPersist);
      return { ok: true };
    } catch (error) {
      const mapped = mapPolicyStudioError(error, "save");
      setSaveError(mapped);
      return { ok: false, error: mapped };
    }
  }, [selectedBundleId, updateBundleMutation, yamlDraft]);

  const simulate = useCallback(
    async (request: Record<string, unknown>) => {
      if (!selectedBundleId.trim()) {
        const error: PolicyStudioGlobalError = {
          code: "request_failed",
          message: "Select a policy bundle before simulation.",
        };
        setSimulateError(error);
        return { ok: false as const, error };
      }
      try {
        const result = await simulateMutation.mutateAsync({
          bundleId: selectedBundleId,
          request,
          content: yamlDraft.trim() ? yamlDraft : undefined,
        });
        setSimulateError(null);
        return { ok: true as const, result };
      } catch (error) {
        const mapped = mapPolicyStudioError(error, "simulate");
        setSimulateError(mapped);
        return { ok: false as const, error: mapped };
      }
    },
    [selectedBundleId, simulateMutation, yamlDraft],
  );

  const isDirty = useMemo(
    () => normalizeYaml(yamlDraft) !== normalizeYaml(originalYaml),
    [originalYaml, yamlDraft],
  );

  return {
    bundles,
    selectedBundleId,
    setSelectedBundleId,
    selectedBundle,
    policy,
    inputRules: policy.rules,
    outputRules: policy.outputRules,
    tenantPolicies,
    updatePolicy,
    setDefaultDecision,
    setInputRules,
    reorderInputRules,
    setOutputPolicy,
    setOutputRules,
    setTenantPolicy,
    toggleOutputRuleEnabled,
    reorderOutputRules,
    yamlDraft,
    setYamlDraft,
    parseIssues,
    isDirty,
    isLoading: bundlesQuery.isLoading || bundleDetailQuery.isLoading,
    loadError,
    isSaving: updateBundleMutation.isPending,
    isSimulating: simulateMutation.isPending,
    save,
    saveError,
    clearSaveError: () => setSaveError(null),
    simulate,
    simulateError,
    clearSimulateError: () => setSimulateError(null),
    refetchBundles: bundlesQuery.refetch,
    refetchSelectedBundle: bundleDetailQuery.refetch,
  };
}

export const __policyStudioGlobalInternal = {
  normalizeYaml,
  mapPolicyStudioError,
  mapLoadError,
  readErrorDetails,
  describeParseIssues,
  moveRule,
  parseTenantPoliciesFromSourceRoot,
};
