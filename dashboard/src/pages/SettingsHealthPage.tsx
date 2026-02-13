import { SystemHealthTab } from "../components/settings/SystemHealthTab";
import { usePageTitle } from "../hooks/usePageTitle";

export default function SettingsHealthPage() {
  usePageTitle("Settings - Health");
  return <SystemHealthTab />;
}
