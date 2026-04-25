import { describe, expect, it, beforeEach, vi } from "vitest";

const { navigateMock, mockState } = vi.hoisted(() => {
  // React 19 act() environment hint for direct createRoot tests.
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: () => ({
      matches: false,
      media: "",
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  });

  return {
    navigateMock: vi.fn(),
    mockState: {
      canEdit: true,
      rules: [] as unknown[],
      bundles: [] as unknown[],
      workflows: [] as unknown[],
    },
  };
});

vi.mock("@/hooks/usePolicies", () => ({
  usePolicyRules: () => ({
    data: { items: mockState.rules },
    isLoading: false,
  }),
  usePolicyBundles: () => ({
    data: { items: mockState.bundles },
  }),
}));

vi.mock("@/hooks/usePolicyAccess", () => ({
  usePolicyAccess: () => ({
    canEdit: mockState.canEdit,
  }),
}));

vi.mock("@/hooks/useWorkflows", () => ({
  useWorkflows: () => ({
    data: mockState.workflows,
  }),
}));

vi.mock("react-router-dom", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-router-dom")>();
  return {
    ...actual,
    useNavigate: () => navigateMock,
  };
});

import { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import type { PolicyRule } from "@/api/types";
import {
  default as InputRulesPage,
  getInputRuleEditTarget,
  getInputRulesAffordances,
  getInputRulesViewModeState,
  INPUT_RULES_PAGE_SECTIONS,
  mapPolicyRuleToInputEditorRule,
} from "./InputRulesPage";

function makeRule(overrides: Partial<PolicyRule> = {}): PolicyRule {
  return {
    id: "rule-1",
    name: "Standalone Rule",
    match: {},
    decision: "deny",
    priority: 1,
    enabled: true,
    ...overrides,
  };
}

function renderPage() {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => {
    root.render(
      <MemoryRouter initialEntries={["/govern/input-rules"]}>
        <InputRulesPage />
      </MemoryRouter>,
    );
  });
  return {
    container,
    cleanup: () => {
      act(() => root.unmount());
      container.remove();
    },
  };
}

beforeEach(() => {
  navigateMock.mockReset();
  mockState.canEdit = true;
  mockState.rules = [];
  mockState.bundles = [];
  mockState.workflows = [];
});

describe("InputRulesPage extraction contract", () => {
  it("keeps page sections scoped to input-policy concerns", () => {
    expect(INPUT_RULES_PAGE_SECTIONS).toEqual([
      "first-match-banner",
      "default-decision",
      "ordered-rules",
      "yaml-pane",
    ]);
    expect(INPUT_RULES_PAGE_SECTIONS).not.toContain("output-rules");
    expect(INPUT_RULES_PAGE_SECTIONS).not.toContain("tenant-policy");
    expect(INPUT_RULES_PAGE_SECTIONS).not.toContain("simulator");
  });

  it("uses page-scoped view-mode visibility rules", () => {
    expect(getInputRulesViewModeState("visual")).toEqual({ showVisual: true, showYaml: false });
    expect(getInputRulesViewModeState("split")).toEqual({ showVisual: true, showYaml: true });
    expect(getInputRulesViewModeState("yaml")).toEqual({ showVisual: false, showYaml: true });
  });

  it("enforces read-only affordances for viewers and write affordances for editors", () => {
    expect(getInputRulesAffordances(false)).toEqual({
      canAddRule: false,
      canEditRule: false,
      canReorderRule: false,
      canDeleteRule: false,
      yamlEditable: false,
      drawerReadOnly: true,
    });

    expect(getInputRulesAffordances(true)).toEqual({
      canAddRule: true,
      canEditRule: true,
      canReorderRule: true,
      canDeleteRule: true,
      yamlEditable: true,
      drawerReadOnly: false,
    });
  });

  it("routes bundled rules to bundle navigation and standalone rules to the drawer", () => {
    expect(getInputRuleEditTarget({ bundle_id: "bundle-a" })).toBe("bundle");
    expect(getInputRuleEditTarget({})).toBe("drawer");
  });

  it("maps API policy rules into the input-rule drawer shape", () => {
    const mapped = mapPolicyRuleToInputEditorRule({
      id: "rule-1",
      rule_id: "deny-admin-tools",
      name: "Deny admin tools",
      bundle_id: undefined,
      match: {
        tenants: ["default"],
        topics: ["job.admin.*"],
        capabilities: ["tool.exec"],
        risk_tags: ["privileged"],
        requires: ["human-review"],
        pack_ids: ["github"],
        actor_ids: ["user-1"],
        actor_types: ["human"],
        labels: { env: "prod" },
        secrets_present: true,
        mcp: {
          allow_servers: ["mcp-safe"],
          deny_tools: ["dangerous.delete"],
        },
      },
      decision: "allow_with_constraints",
      constraints: {
        budgets: { max_runtime_ms: 1500, max_concurrent_jobs: 2 },
        sandbox: {
          isolated: true,
          network_allowlist: ["api.example.com"],
          fs_read_only: ["/repo"],
          fs_read_write: ["/tmp"],
        },
        toolchain: {
          allowed_tools: ["grep"],
          allowed_commands: ["git status"],
        },
        diff: {
          max_files: 3,
          max_lines: 40,
          deny_path_globs: ["*.pem"],
        },
        redaction_level: "strict",
      },
      priority: 10,
      enabled: true,
      reason: "Keep privileged actions constrained",
      source: { version: 1 },
    });

    expect(mapped).toEqual({
      id: "deny-admin-tools",
      decision: "allow_with_constraints",
      reason: "Keep privileged actions constrained",
      match: {
        tenants: ["default"],
        topics: ["job.admin.*"],
        capabilities: ["tool.exec"],
        riskTags: ["privileged"],
        requires: ["human-review"],
        packIds: ["github"],
        actorIds: ["user-1"],
        actorTypes: ["human"],
        labels: { env: "prod" },
        secretsPresent: true,
        mcp: {
          allowServers: ["mcp-safe"],
          denyServers: [],
          allowTools: [],
          denyTools: ["dangerous.delete"],
          allowResources: [],
          denyResources: [],
          allowActions: [],
          denyActions: [],
        },
      },
      constraints: {
        budgets: {
          maxRuntimeMs: 1500,
          maxRetries: undefined,
          maxArtifactBytes: undefined,
          maxConcurrentJobs: 2,
        },
        sandbox: {
          isolated: true,
          networkAllowlist: ["api.example.com"],
          fsReadOnly: ["/repo"],
          fsReadWrite: ["/tmp"],
        },
        toolchain: {
          allowedTools: ["grep"],
          allowedCommands: ["git status"],
        },
        diff: {
          maxFiles: 3,
          maxLines: 40,
          denyPathGlobs: ["*.pem"],
        },
        redactionLevel: "strict",
      },
      remediations: [],
      source: { version: 1 },
    });
  });
});

describe("InputRulesPage edit interactions", () => {
  it("opens the existing drawer for non-bundle rules", () => {
    mockState.rules = [makeRule()];

    const { container, cleanup } = renderPage();
    const editButton = container.querySelector(
      'button[title="Edit rule"]',
    ) as HTMLButtonElement | null;

    expect(editButton).not.toBeNull();
    act(() => editButton?.click());

    expect(container.textContent).toContain("Edit Rule");
    expect(container.textContent).toContain("Save rule");
    expect(navigateMock).not.toHaveBeenCalled();
    cleanup();
  });

  it("preserves bundle navigation for bundle-backed rules", () => {
    mockState.rules = [
      makeRule({
        id: "rule-2",
        name: "Bundled Rule",
        bundle_id: "bundle/team-a",
      }),
    ];

    const { container, cleanup } = renderPage();
    const editButton = container.querySelector(
      'button[title="Edit in bundle"]',
    ) as HTMLButtonElement | null;

    expect(editButton).not.toBeNull();
    act(() => editButton?.click());

    expect(navigateMock).toHaveBeenCalledWith(
      "/govern/bundles/bundle%2Fteam-a",
    );
    expect(container.textContent).not.toContain("Edit Rule");
    cleanup();
  });
});
