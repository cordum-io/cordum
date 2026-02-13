import { PolicyAnalytics } from "../components/policy/PolicyAnalytics";
import { usePageTitle } from "../hooks/usePageTitle";

export default function PoliciesAnalyticsPage() {
  usePageTitle("Policies - Analytics");
  return <PolicyAnalytics />;
}
