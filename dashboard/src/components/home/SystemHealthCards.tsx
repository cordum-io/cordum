import { ArrowRight } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { Button } from "@/components/ui/Button";
import { CollapsibleSection } from "@/components/ui/CollapsibleSection";
import { InstrumentCard } from "@/components/ui/InstrumentCard";
import { cn } from "@/lib/utils";
import type { Worker } from "@/api/types";

export interface ServiceHealth {
  name: string;
  status: "healthy" | "degraded" | "down";
  latency: string;
}

interface SystemHealthCardsProps {
  workers: Worker[];
  workersLoading: boolean;
  services: ServiceHealth[];
  statusLoading: boolean;
}

export function SystemHealthCards({
  workers,
  workersLoading,
  services,
  statusLoading,
}: SystemHealthCardsProps) {
  const navigate = useNavigate();

  return (
    <>
      <CollapsibleSection title="Worker Pool Health" defaultOpen={false}>
        <div className="flex items-center justify-between mb-4">
          <p className="text-xs text-muted-foreground">Real-time agent status</p>
          <Button variant="ghost" size="sm" onClick={() => navigate("/agents")}>
            View fleet <ArrowRight className="w-3 h-3 ml-1" />
          </Button>
        </div>
        <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-6 gap-3">
          {workers.slice(0, 12).map((w) => {
            const isOnline = w.status === "idle" || w.status === "busy";
            return (
              <InstrumentCard
                key={w.id}
                onClick={() => navigate(`/agents/${w.id}`)}
                hoverable
                accent={isOnline ? "healthy" : "muted"}
                className="p-3"
              >
                <div className="flex items-center gap-2 mb-2">
                  <div
                    className={cn(
                      "w-2 h-2 rounded-full",
                      isOnline
                        ? "bg-[var(--color-success)] animate-pulse"
                        : "bg-muted-foreground",
                    )}
                  />
                  <span className="font-mono text-xs text-foreground truncate">
                    {w.name || w.id.slice(0, 10)}
                  </span>
                </div>
                <div className="space-y-1.5">
                  <div className="flex justify-between text-xs uppercase tracking-wider font-mono">
                    <span className="text-muted-foreground">CPU</span>
                    <span className="text-foreground">{w.cpuLoad ?? 0}%</span>
                  </div>
                  <div className="w-full h-1 rounded-full bg-surface-2 overflow-hidden">
                    <div
                      className="h-full rounded-full bg-cordum transition-all"
                      style={{ width: `${w.cpuLoad ?? 0}%` }}
                    />
                  </div>
                  <div className="flex justify-between text-xs uppercase tracking-wider font-mono">
                    <span className="text-muted-foreground">MEM</span>
                    <span className="text-foreground">{w.memoryLoad ?? 0}%</span>
                  </div>
                  <div className="w-full h-1 rounded-full bg-surface-2 overflow-hidden">
                    <div
                      className="h-full rounded-full bg-[var(--color-info)] transition-all"
                      style={{ width: `${w.memoryLoad ?? 0}%` }}
                    />
                  </div>
                </div>
                <div className="mt-2 pt-1.5 border-t border-border/40 text-xs font-mono text-muted-foreground">
                  Jobs: {w.activeJobs ?? 0} / {w.capacity ?? 0}
                </div>
              </InstrumentCard>
            );
          })}
          {workers.length === 0 && !workersLoading && (
            <div className="col-span-full flex flex-col items-center gap-2 py-8">
              <p className="text-sm text-muted-foreground">
                No agents connected — start an agent with your API key
              </p>
              <Button variant="outline" size="sm" onClick={() => navigate("/agents")}>
                Agent setup
              </Button>
            </div>
          )}
        </div>
      </CollapsibleSection>

      <CollapsibleSection title="Service Health" defaultOpen={false}>
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-5 gap-3">
          {statusLoading ? (
            Array.from({ length: 5 }).map((_, i) => (
              <div
                key={i}
                className="flex items-center gap-3 rounded-2xl border border-border bg-surface-0 p-3 animate-pulse"
              >
                <div className="w-2 h-2 rounded-full shrink-0 bg-surface-2" />
                <div className="flex-1 min-w-0 space-y-1">
                  <div className="h-3 bg-surface-2 rounded w-20" />
                  <div className="h-2.5 bg-surface-2 rounded w-10" />
                </div>
              </div>
            ))
          ) : services.length > 0 ? (
            services.map((svc) => (
              <InstrumentCard
                key={svc.name}
                accent={
                  svc.status === "healthy"
                    ? "healthy"
                    : svc.status === "degraded"
                      ? "warning"
                      : "danger"
                }
                className="p-3"
              >
                <div className="flex items-center gap-3">
                  <div
                    className={cn(
                      "w-2 h-2 rounded-full shrink-0",
                      svc.status === "healthy"
                        ? "bg-[var(--color-success)]"
                        : svc.status === "degraded"
                          ? "bg-[var(--color-warning)]"
                          : "bg-destructive",
                    )}
                  />
                  <div className="flex-1 min-w-0">
                    <p className="text-xs text-foreground font-semibold truncate">
                      {svc.name}
                    </p>
                    <p className="text-xs text-muted-foreground font-mono">
                      {svc.latency || "—"}
                    </p>
                  </div>
                </div>
              </InstrumentCard>
            ))
          ) : (
            <div className="col-span-full text-center py-4 text-sm text-muted-foreground">
              Health data unavailable
            </div>
          )}
        </div>
      </CollapsibleSection>
    </>
  );
}
