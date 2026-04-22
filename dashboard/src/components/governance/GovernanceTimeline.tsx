import { ShieldCheck } from "lucide-react";
import { useGovernanceDecisionsFlat, type UseGovernanceDecisionsArgs } from "@/hooks/useGovernanceDecisions";
import type { GovernanceDecision } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { InstrumentCard, InstrumentCardBody, InstrumentCardHeader } from "@/components/ui/InstrumentCard";
import { Skeleton } from "@/components/ui/Skeleton";
import { DecisionNode } from "./DecisionNode";

interface GovernanceTimelineProps extends UseGovernanceDecisionsArgs {
  emptyHint?: string;
  items?: GovernanceDecision[];
  isLoading?: boolean;
  error?: Error | null;
  onRetry?: () => void;
  hasNextPage?: boolean;
  isFetchingNextPage?: boolean;
  onLoadMore?: () => void;
}

export function GovernanceTimeline({
  jobId,
  runId,
  filters,
  limit,
  emptyHint,
  items,
  isLoading: isLoadingOverride,
  error: errorOverride,
  onRetry,
  hasNextPage: hasNextPageOverride,
  isFetchingNextPage: isFetchingNextPageOverride,
  onLoadMore,
}: GovernanceTimelineProps) {
  const query = useGovernanceDecisionsFlat({
    jobId,
    runId,
    filters,
    limit,
  });

  const decisions = items ?? query.items;
  const isLoading =
    isLoadingOverride ?? (query.isLoading && !query.data);
  const error = errorOverride ?? (query.isError ? query.error : null);
  const hasNextPage = hasNextPageOverride ?? Boolean(query.hasNextPage);
  const isFetchingNextPage =
    isFetchingNextPageOverride ?? query.isFetchingNextPage;

  const retry = onRetry ?? (() => void query.refetch());
  const loadMore = onLoadMore ?? (() => void query.fetchNextPage());

  return (
    <InstrumentCard accent="governance" className="max-w-[72ch]">
      <InstrumentCardHeader
        title="Governance Timeline"
        subtitle={
          emptyHint ??
          "Chronological policy decisions for this execution. Each entry shows the matched rule, verdict, reason, and constraint snapshot."
        }
        icon={<ShieldCheck className="h-4 w-4" />}
      />
      <InstrumentCardBody>
        {isLoading ? (
          <div aria-busy="true" className="space-y-3">
            {Array.from({ length: 3 }).map((_, index) => (
              <div
                key={index}
                className="flex gap-4"
              >
                <Skeleton className="h-10 w-10 shrink-0 rounded-full" />
                <div className="flex-1 space-y-2">
                  <Skeleton className="h-5 w-1/3 rounded-full" />
                  <Skeleton className="h-20 w-full rounded-3xl" />
                </div>
              </div>
            ))}
          </div>
        ) : error ? (
          <ErrorBanner
            title="Unable to load governance decisions"
            message={error.message}
            onRetry={retry}
          />
        ) : decisions.length === 0 ? (
          <EmptyState
            icon={<ShieldCheck className="h-5 w-5" />}
            title="No governance decisions yet"
            description={
              emptyHint ??
              "This run hasn\u2019t triggered any policy evaluations. Decisions appear here as safety rules evaluate."
            }
          />
        ) : (
          <div className="space-y-4">
            <ul role="list" aria-label="Governance decisions" className="space-y-1">
              {decisions.map((decision, index) => (
                <DecisionNode
                  key={`${decision.timestamp}-${decision.matchedRule}-${index}`}
                  decision={decision}
                  index={index}
                  isLast={index === decisions.length - 1}
                />
              ))}
            </ul>
            {hasNextPage && (
              <div className="flex justify-center">
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={loadMore}
                  disabled={isFetchingNextPage}
                >
                  {isFetchingNextPage ? "Loading\u2026" : "Load more"}
                </Button>
              </div>
            )}
          </div>
        )}
      </InstrumentCardBody>
    </InstrumentCard>
  );
}
