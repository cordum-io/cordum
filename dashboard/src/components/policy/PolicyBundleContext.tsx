import { createContext, useContext, useState, useEffect, type ReactNode } from "react";
import { useSearchParams } from "react-router-dom";
import { usePolicyBundles } from "../../hooks/usePolicies";
import type { PolicyBundle } from "../../api/types";

interface PolicyBundleContextValue {
  bundleId: string;
  setBundleId: (id: string) => void;
  bundles: PolicyBundle[];
  isLoading: boolean;
  isError: boolean;
}

const Ctx = createContext<PolicyBundleContextValue | null>(null);

export function PolicyBundleProvider({ children }: { children: ReactNode }) {
  const { data, isLoading, isError } = usePolicyBundles();
  const bundles = data?.items ?? [];
  const [searchParams, setSearchParams] = useSearchParams();
  const [bundleId, setBundleIdState] = useState(() => searchParams.get("bundle") ?? "");

  // Default to first bundle when loaded
  useEffect(() => {
    if (!bundleId && bundles.length > 0) {
      setBundleIdState(bundles[0].id);
    }
  }, [bundles, bundleId]);

  const setBundleId = (id: string) => {
    setBundleIdState(id);
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      if (id) {
        next.set("bundle", id);
      } else {
        next.delete("bundle");
      }
      return next;
    }, { replace: true });
  };

  return (
    <Ctx.Provider value={{ bundleId, setBundleId, bundles, isLoading, isError }}>
      {children}
    </Ctx.Provider>
  );
}

export function usePolicyBundleContext() {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("usePolicyBundleContext must be used within PolicyBundleProvider");
  return ctx;
}
