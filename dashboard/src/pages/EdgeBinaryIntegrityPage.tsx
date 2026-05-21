/*
 * EDGE-151-DASHBOARD — Binary integrity dashboard page.
 *
 * Lists recent `binary-verify-{ok,fail}` outcomes captured from the
 * install scripts (docs/security/binary-signing.md §8). Operators can
 * filter by event class, sig_scheme, and endpoint label; failed events
 * pin a warning row with a deep-link to the operator runbook.
 */

import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import {
  ShieldAlert,
  ShieldCheck,
  Filter,
  ExternalLink,
  AlertTriangle,
} from "lucide-react";
import { Button } from "@/components/ui/Button";
import { EmptyState } from "@/components/ui/EmptyState";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { Skeleton } from "@/components/ui/Skeleton";
import { StatusBadge, type BadgeVariant } from "@/components/ui/StatusBadge";
import {
  useBinaryIntegrityEvents,
  type BinaryIntegrityFilters,
  type BinaryVerifyListItem,
  type BinaryVerifySigScheme,
} from "@/hooks/useBinaryIntegrityEvents";
import { cn, formatRelativeTime } from "@/lib/utils";

const SIG_SCHEMES: readonly BinaryVerifySigScheme[] = [
  "gpg",
  "codesign",
  "authenticode",
  "dev",
] as const;

const RUNBOOK_HREF = "/docs/security/binary-signing.md#9-operator-runbook";

function shortHash(value: string): string {
  if (!value) return "—";
  return value.length > 12 ? `${value.slice(0, 12)}…` : value;
}

function shortFingerprint(value: string): string {
  if (!value) return "—";
  // Standard 40-char gpg fingerprint → show last 16 chars (long-id form).
  if (value.length >= 16) return value.slice(-16);
  return value;
}

function eventVariant(item: BinaryVerifyListItem): BadgeVariant {
  return item.event === "binary-verify-ok" ? "healthy" : "danger";
}

export default function EdgeBinaryIntegrityPage() {
  const [filters, setFilters] = useState<BinaryIntegrityFilters>({});
  const query = useBinaryIntegrityEvents(filters);

  const failedCount = useMemo(
    () => query.items.filter((it) => it.event === "binary-verify-fail").length,
    [query.items],
  );

  return (
    <div className="space-y-6 p-6">
      <header className="space-y-2">
        <p className="text-xs font-medium uppercase tracking-[0.18em] text-cordum">
          Edge / Binary integrity
        </p>
        <h1 className="text-2xl font-semibold text-foreground">
          Install-time binary verification
        </h1>
        <p className="max-w-2xl text-sm text-muted-foreground">
          Structured outcomes from the pre-activation integrity gate in{" "}
          <code className="rounded bg-surface-2 px-1 py-0.5 font-mono text-xs">
            tools/scripts/install.{`{sh,ps1}`}
          </code>
          . Failed events ship with a pinned runbook link — see{" "}
          <Link
            to={RUNBOOK_HREF}
            className="text-cordum underline-offset-2 hover:underline"
          >
            §9 Operator runbook
          </Link>
          .
        </p>
      </header>

      <FilterRow filters={filters} setFilters={setFilters} />

      {query.userMessage ? (
        <ErrorBanner
          title="Binary-integrity panel unavailable"
          message={query.userMessage}
          onRetry={() => {
            void query.refetch();
          }}
        />
      ) : null}

      {failedCount > 0 ? (
        <div
          className="flex items-start gap-3 rounded-2xl border border-amber-300/60 bg-amber-50/50 p-3 text-sm text-amber-900 dark:border-amber-400/40 dark:bg-amber-950/30 dark:text-amber-100"
          data-testid="binary-integrity-fail-summary"
          role="status"
        >
          <ShieldAlert className="mt-0.5 h-4 w-4" aria-hidden="true" />
          <div>
            <div className="font-medium">
              {failedCount} verify failure{failedCount === 1 ? "" : "s"} in the
              current page
            </div>
            <div className="text-xs">
              Open the affected rows for the operator action; treat each
              endpoint as suspect until the failure is investigated per the
              runbook.
            </div>
          </div>
        </div>
      ) : null}

      {query.isPending ? (
        <Skeleton className="h-64 w-full" />
      ) : query.items.length === 0 ? (
        <EmptyState
          title="No binary-verify events recorded"
          description="Once operators upload install-script stderr via the documented ingest endpoint, outcomes will appear here. See §8 Operator ingest workflow for the curl recipe."
        />
      ) : (
        <EventTable items={query.items} />
      )}
    </div>
  );
}

function FilterRow({
  filters,
  setFilters,
}: {
  filters: BinaryIntegrityFilters;
  setFilters: (next: BinaryIntegrityFilters) => void;
}) {
  return (
    <section
      className="grid gap-3 rounded-2xl border border-border bg-surface-1/70 p-3 shadow-soft sm:grid-cols-2 lg:grid-cols-4"
      data-testid="binary-integrity-filters"
    >
      <FilterSelect
        label="Event"
        value={filters.event ?? ""}
        onChange={(value) =>
          setFilters({
            ...filters,
            event: value === "" ? undefined : (value as "ok" | "fail"),
          })
        }
        testid="binary-integrity-filter-event"
        options={[
          { value: "", label: "All events" },
          { value: "ok", label: "Verified (ok)" },
          { value: "fail", label: "Failed" },
        ]}
        icon={<Filter className="h-3 w-3" />}
      />
      <FilterSelect
        label="Signature scheme"
        value={filters.sigScheme ?? ""}
        onChange={(value) =>
          setFilters({
            ...filters,
            sigScheme:
              value === "" ? undefined : (value as BinaryVerifySigScheme),
          })
        }
        testid="binary-integrity-filter-sig-scheme"
        options={[
          { value: "", label: "All schemes" },
          ...SIG_SCHEMES.map((s) => ({ value: s, label: s })),
        ]}
        icon={<Filter className="h-3 w-3" />}
      />
      <label className="flex flex-col gap-1 text-xs text-muted-foreground">
        <span className="flex items-center gap-1 uppercase tracking-[0.12em]">
          <Filter className="h-3 w-3" /> Endpoint
        </span>
        <input
          type="text"
          value={filters.endpoint ?? ""}
          onChange={(e) =>
            setFilters({
              ...filters,
              endpoint: e.target.value === "" ? undefined : e.target.value,
            })
          }
          placeholder="hostname or asset tag"
          data-testid="binary-integrity-filter-endpoint"
          className="rounded-xl border border-border bg-background px-2 py-1.5 text-sm text-foreground shadow-soft focus:outline-none focus:ring-2 focus:ring-cordum/30"
        />
      </label>
      <div className="flex items-end">
        <Button
          variant="outline"
          size="sm"
          onClick={() => setFilters({})}
          data-testid="binary-integrity-reset"
          aria-label="Reset filters"
        >
          Reset
        </Button>
      </div>
    </section>
  );
}

interface FilterOption {
  value: string;
  label: string;
}

function FilterSelect({
  label,
  value,
  onChange,
  options,
  icon,
  testid,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  options: readonly FilterOption[];
  icon?: React.ReactNode;
  testid: string;
}) {
  return (
    <label className="flex flex-col gap-1 text-xs text-muted-foreground">
      <span className="flex items-center gap-1 uppercase tracking-[0.12em]">
        {icon}
        {label}
      </span>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        data-testid={testid}
        className="rounded-xl border border-border bg-background px-2 py-1.5 text-sm text-foreground shadow-soft focus:outline-none focus:ring-2 focus:ring-cordum/30"
      >
        {options.map((opt) => (
          <option key={opt.value} value={opt.value}>
            {opt.label}
          </option>
        ))}
      </select>
    </label>
  );
}

function EventTable({ items }: { items: BinaryVerifyListItem[] }) {
  return (
    <section
      className="overflow-hidden rounded-2xl border border-border bg-surface-1/70 shadow-soft"
      data-testid="binary-integrity-table"
    >
      <div className="overflow-x-auto">
        <table className="min-w-full text-sm">
          <thead>
            <tr className="border-b border-border bg-surface-2/50 text-[10px] uppercase tracking-[0.14em] text-muted-foreground">
              <th className="px-3 py-2 text-left font-medium">When</th>
              <th className="px-3 py-2 text-left font-medium">Event</th>
              <th className="px-3 py-2 text-left font-medium">Endpoint</th>
              <th className="px-3 py-2 text-left font-medium">Binary</th>
              <th className="px-3 py-2 text-left font-medium">Hash</th>
              <th className="px-3 py-2 text-left font-medium">Sig scheme</th>
              <th className="px-3 py-2 text-left font-medium">Fingerprint</th>
              <th className="px-3 py-2 text-left font-medium">Reason</th>
            </tr>
          </thead>
          <tbody>
            {items.map((item, idx) => (
              <EventRow
                key={`${item.timestamp}-${item.hash}-${idx}`}
                item={item}
              />
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function EventRow({ item }: { item: BinaryVerifyListItem }) {
  const isFailure = item.event === "binary-verify-fail";
  const Icon = isFailure ? ShieldAlert : ShieldCheck;
  return (
    <tr
      data-testid={`binary-integrity-row-${item.event}`}
      data-event={item.event}
      className={cn(
        "border-b border-border last:border-0",
        isFailure
          ? "bg-amber-50/30 dark:bg-amber-950/20"
          : "hover:bg-surface-2/40",
      )}
    >
      <td className="whitespace-nowrap px-3 py-2 text-xs text-muted-foreground">
        {formatRelativeTime(item.timestamp)}
      </td>
      <td className="px-3 py-2">
        <span className="inline-flex items-center gap-1.5">
          <Icon
            className={cn(
              "h-3.5 w-3.5",
              isFailure ? "text-amber-600 dark:text-amber-300" : "text-emerald-600 dark:text-emerald-300",
            )}
            aria-hidden="true"
          />
          <StatusBadge variant={eventVariant(item)}>
            {isFailure ? "fail" : "ok"}
          </StatusBadge>
        </span>
      </td>
      <td className="whitespace-nowrap px-3 py-2 font-mono text-xs text-foreground">
        {item.endpoint || "—"}
      </td>
      <td className="whitespace-nowrap px-3 py-2 font-mono text-xs text-foreground">
        {item.path}
      </td>
      <td className="px-3 py-2 font-mono text-[11px] text-muted-foreground" title={item.hash}>
        {shortHash(item.hash)}
      </td>
      <td className="whitespace-nowrap px-3 py-2 text-xs text-foreground">
        {item.sig_scheme}
      </td>
      <td
        className="px-3 py-2 font-mono text-[11px] text-muted-foreground"
        title={item.fingerprint}
      >
        {shortFingerprint(item.fingerprint)}
      </td>
      <td className="px-3 py-2 text-xs">
        {isFailure ? (
          <span className="inline-flex flex-wrap items-center gap-1 text-amber-800 dark:text-amber-200">
            <AlertTriangle className="h-3 w-3" aria-hidden="true" />
            {item.reason || "no reason recorded"}
            <Link
              to={RUNBOOK_HREF}
              data-testid="binary-integrity-runbook-link"
              className="ml-1 inline-flex items-center gap-0.5 text-cordum underline-offset-2 hover:underline"
            >
              View runbook
              <ExternalLink className="h-3 w-3" aria-hidden="true" />
            </Link>
          </span>
        ) : (
          <span className="text-muted-foreground">—</span>
        )}
      </td>
    </tr>
  );
}
