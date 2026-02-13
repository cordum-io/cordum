import type { ReactNode } from "react";
import { usePermission } from "../hooks/usePermission";

interface RequireRoleProps {
  roles: string[];
  children: ReactNode;
  fallback?: ReactNode;
}

export function RequireRole({
  roles,
  children,
  fallback = null,
}: RequireRoleProps) {
  const { allowed } = usePermission(roles);
  return allowed ? <>{children}</> : <>{fallback}</>;
}
