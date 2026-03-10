/**
 * Build a deep-link URL to the Simulator page with prefilled context.
 *
 * Query parameter contract:
 *   - bundle:       selected bundle ID
 *   - topic:        job topic
 *   - tenant:       tenant ID
 *   - workflow:     workflow ID
 *   - capabilities: comma-separated capability list
 *   - risk_tags:    comma-separated risk tag list
 */

export interface SimulatorDeepLinkParams {
  bundleId?: string;
  topic?: string;
  tenant?: string;
  workflowId?: string;
  capabilities?: string[];
  riskTags?: string[];
}

export function buildSimulatorUrl(params: SimulatorDeepLinkParams): string {
  const sp = new URLSearchParams();
  if (params.bundleId?.trim()) sp.set("bundle", params.bundleId.trim());
  if (params.topic?.trim()) sp.set("topic", params.topic.trim());
  if (params.tenant?.trim()) sp.set("tenant", params.tenant.trim());
  if (params.workflowId?.trim()) sp.set("workflow", params.workflowId.trim());
  if (params.capabilities?.length) sp.set("capabilities", params.capabilities.join(","));
  if (params.riskTags?.length) sp.set("risk_tags", params.riskTags.join(","));

  const qs = sp.toString();
  return qs ? `/govern/simulator?${qs}` : "/govern/simulator";
}
