import { motion } from "framer-motion";
import { Activity, ArrowRight, Cpu, ShieldCheck, UserCheck } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { Button } from "@/components/ui/Button";
import { SkeletonCard } from "@/components/ui/Skeleton";
import { StatTile } from "@/components/ui/StatTile";
import { cn } from "@/lib/utils";
import type { Approval, Job, Worker } from "@/api/types";

interface HomeKpiCardsProps {
  jobs: Job[];
  workers: Worker[];
  pendingApprovals: Approval[];
  isLoading: boolean;
}

interface SafetyCounts {
  allowed: number;
  denied: number;
  approval: number;
  allowRate: number;
}

function summarizeSafety(jobs: Job[]): SafetyCounts {
  const allowed = jobs.filter((j) => j.safetyDecision?.type === "allow").length;
  const denied = jobs.filter((j) => j.safetyDecision?.type === "deny").length;
  const approval = jobs.filter(
    (j) => j.safetyDecision?.type === "require_approval",
  ).length;
  const constrained = jobs.filter(
    (j) => j.safetyDecision?.type === "allow_with_constraints",
  ).length;
  const throttled = jobs.filter(
    (j) => j.safetyDecision?.type === "throttle",
  ).length;
  const total = allowed + denied + approval + constrained + throttled;
  return {
    allowed,
    denied,
    approval,
    allowRate: total > 0 ? Math.round((allowed / total) * 100) : 0,
  };
}

const fadeUp = { hidden: { opacity: 0, y: 10 }, visible: { opacity: 1, y: 0 } };

export function HomeKpiCards({
  jobs,
  workers,
  pendingApprovals,
  isLoading,
}: HomeKpiCardsProps) {
  const navigate = useNavigate();
  const safety = summarizeSafety(jobs);
  const activeWorkers = workers.filter(
    (w) => w.status === "idle" || w.status === "busy",
  );
  const runningJobs = jobs.filter((j) => j.status === "running").length;
  const completedJobs = jobs.filter((j) => j.status === "succeeded").length;
  const failedJobs = jobs.filter((j) => j.status === "failed").length;

  return (
    <motion.div
      initial="hidden"
      animate="visible"
      variants={{ visible: { transition: { staggerChildren: 0.05 } } }}
      className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4"
    >
      {isLoading ? (
        Array.from({ length: 4 }).map((_, i) => <SkeletonCard key={i} />)
      ) : (
        <>
          <motion.div variants={fadeUp}>
            <StatTile
              label="Recent Jobs"
              value={jobs.length.toLocaleString()}
              icon={<Activity className="w-4 h-4" />}
            >
              <div className="flex gap-3 mt-3 text-xs font-mono text-muted-foreground">
                <span>{runningJobs} running</span>
                <span className="text-[var(--color-success)]">
                  {completedJobs} done
                </span>
                <span className="text-destructive">{failedJobs} failed</span>
              </div>
            </StatTile>
          </motion.div>

          <motion.div variants={fadeUp}>
            <StatTile
              label="Active Agents"
              value={activeWorkers.length}
              icon={<Cpu className="w-4 h-4" />}
            >
              <div className="flex gap-1 mt-3.5">
                {workers.slice(0, 20).map((w) => (
                  <div
                    key={w.id}
                    className={cn(
                      "w-2 h-2 rounded-sm",
                      w.status === "idle"
                        ? "bg-[var(--color-success)]"
                        : w.status === "busy"
                          ? "bg-cordum"
                          : "bg-muted-foreground",
                    )}
                  />
                ))}
              </div>
            </StatTile>
          </motion.div>

          <motion.div variants={fadeUp}>
            <StatTile
              label="Safety Decisions"
              value={`${safety.allowRate}%`}
              icon={<ShieldCheck className="w-4 h-4" />}
            >
              <div className="flex gap-3 mt-3 text-xs font-mono">
                <span className="text-[var(--color-success)]">
                  {safety.allowed} allow
                </span>
                <span className="text-[var(--color-governance)]">
                  {safety.denied} deny
                </span>
                <span className="text-[var(--color-warning)]">
                  {safety.approval} review
                </span>
              </div>
            </StatTile>
          </motion.div>

          <motion.div variants={fadeUp}>
            <StatTile
              label="Pending Approvals"
              value={pendingApprovals.length}
              accent={pendingApprovals.length > 0 ? "warning" : "cordum"}
              icon={
                <UserCheck
                  className={cn(
                    "w-4 h-4",
                    pendingApprovals.length > 0
                      ? "text-[var(--color-warning)]"
                      : "text-cordum",
                  )}
                />
              }
            >
              {pendingApprovals.length > 0 && (
                <Button
                  variant="ghost"
                  size="sm"
                  className="mt-2.5 text-[var(--color-warning)] hover:text-[var(--color-warning)] p-0 h-auto font-mono text-xs uppercase tracking-widest"
                  onClick={() => navigate("/approvals")}
                >
                  Review now <ArrowRight className="w-3 h-3 ml-1" />
                </Button>
              )}
            </StatTile>
          </motion.div>
        </>
      )}
    </motion.div>
  );
}
