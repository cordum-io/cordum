export type OutputPolicyFailMode = "open" | "closed";
export type OutputPolicyFailureAction = "allow" | "deny";

export interface TopicOverride {
  topicPattern: string;
  enabled: boolean;
  failMode: OutputPolicyFailMode;
  scanners: string[];
}

export interface ScannerOverride {
  id: string;
  enabled?: boolean;
  action?: string;
  confidence?: number;
  enabledTypes?: string[];
}

export interface CustomPatternConfig {
  id: string;
  name: string;
  regex: string;
  category: string;
  action: string;
  enabled: boolean;
}

export interface OutputPolicyConfig {
  enabled: boolean;
  failMode: OutputPolicyFailMode;
  scanTimeoutMs: number;
  maxPayloadKb: number;
  failureAction: OutputPolicyFailureAction;
  topicOverrides: TopicOverride[];
  scannerOverrides: ScannerOverride[];
  customPatterns: CustomPatternConfig[];
}

export interface OutputPolicyStats {
  totalChecks24h: number;
  quarantined24h: number;
  avgLatencyMs: number;
  lastCheckAt?: string;
}

export const DEFAULT_OUTPUT_POLICY_CONFIG: OutputPolicyConfig = {
  enabled: false,
  failMode: "open",
  scanTimeoutMs: 5000,
  maxPayloadKb: 512,
  failureAction: "allow",
  topicOverrides: [],
  scannerOverrides: [],
  customPatterns: [],
};
