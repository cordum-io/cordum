import { useState } from "react";
import { AlertTriangle, Copy, Check, KeyRound } from "lucide-react";
import { Card, CardTitle } from "@/components/ui/Card";
import { SignatureBadge } from "@/components/SignatureBadge";
import { cn } from "@/lib/utils";
import type { PolicyBundle } from "@/api/types";

// BundleSignatureSection mounts on the Bundle Detail page to surface
// the full signature record: badge + key_id + algorithm + SHA-256 +
// signed-bytes. It also calls out the compliance implication of
// unsigned bundles ("strict-mode kernels will reject it") so an
// operator looking at a draft knows exactly what the signing key
// toggle controls.

interface BundleSignatureSectionProps {
  bundle: PolicyBundle;
  className?: string;
}

export function BundleSignatureSection({
  bundle,
  className,
}: BundleSignatureSectionProps) {
  const signed = bundle.signed;
  const sig = bundle.signature;

  return (
    <Card
      className={cn("relative overflow-hidden", className)}
      data-testid="bundle-signature-section"
      data-signed={signed === true ? "true" : signed === false ? "false" : "unknown"}
    >
      <span
        aria-hidden="true"
        className={cn(
          "pointer-events-none absolute inset-x-0 top-0 h-0.5",
          signed === true && "bg-success",
          signed === false && "bg-warning",
          (signed === undefined || signed === null) && "bg-border",
        )}
      />
      <header className="mb-4 flex items-center justify-between gap-4">
        <div className="flex items-center gap-3">
          <span
            className={cn(
              "flex h-8 w-8 items-center justify-center rounded-full border",
              signed === true && "bg-success/10 text-success border-success/30",
              signed === false && "bg-warning/10 text-warning border-warning/30",
              (signed === undefined || signed === null) &&
                "bg-muted text-muted-foreground border-border",
            )}
            aria-hidden="true"
          >
            <KeyRound className="h-4 w-4" />
          </span>
          <div>
            <CardTitle className="text-sm">Signature</CardTitle>
            <p className="text-xs text-muted-foreground">
              Ed25519 signature verified by the safety kernel at load time.
            </p>
          </div>
        </div>
        <SignatureBadge
          signed={signed ?? "unknown"}
          publicKeyId={sig?.key_id}
          size="md"
        />
      </header>

      {signed === true && sig && (
        <dl className="grid grid-cols-1 gap-4 md:grid-cols-2">
          <Field label="Algorithm" value={sig.algorithm || "ed25519"} mono />
          <Field label="Key ID" value={sig.key_id || "—"} mono copy />
          <Field label="SHA-256 hash" value={sig.hash || "—"} mono copy truncate />
          <Field
            label="Signed bytes"
            value={
              sig.signed_bytes
                ? `${sig.signed_bytes.toLocaleString()} bytes`
                : "—"
            }
          />
        </dl>
      )}

      {signed === false && (
        <div className="rounded-2xl border border-warning/30 bg-warning/5 p-4">
          <div className="flex items-start gap-3">
            <AlertTriangle
              className="mt-0.5 h-4 w-4 shrink-0 text-warning"
              aria-hidden="true"
            />
            <div className="min-w-0">
              <p className="text-sm font-semibold text-warning">
                This bundle is not signed.
              </p>
              <p className="mt-1 text-xs text-warning/90">
                In strict mode the safety kernel will reject this bundle at
                load time. Configure a signing key (CORDUM_POLICY_SIGNING_KEY
                or equivalent) so the next save produces a signed artefact.
              </p>
              <p className="mt-3 text-[11px] text-muted-foreground">
                Signing details: see{" "}
                <a
                  className="underline decoration-dotted underline-offset-2 hover:text-ink"
                  href="/docs/deployment/audit-chain"
                  rel="noreferrer"
                >
                  policy signing docs
                </a>
                .
              </p>
            </div>
          </div>
        </div>
      )}

      {(signed === undefined || signed === null) && (
        <div className="rounded-2xl border border-border bg-muted/40 p-4">
          <p className="text-xs text-muted-foreground">
            Signature status is not available for this bundle. The bundle
            detail endpoint has not yet been extended to surface the
            signature record; reload after viewing the bundles index to
            populate this section from the list cache.
          </p>
        </div>
      )}
    </Card>
  );
}

function Field({
  label,
  value,
  mono,
  copy,
  truncate,
}: {
  label: string;
  value: string;
  mono?: boolean;
  copy?: boolean;
  truncate?: boolean;
}) {
  const [copied, setCopied] = useState(false);
  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard API may be unavailable in non-secure contexts; swallow
      // silently so the rest of the UI stays interactive.
    }
  };
  return (
    <div className="min-w-0">
      <dt className="text-[10px] font-semibold uppercase tracking-[0.16em] text-muted-foreground">
        {label}
      </dt>
      <dd
        className={cn(
          "mt-1 flex items-center gap-2",
          mono ? "font-mono text-xs" : "text-sm",
          "text-ink",
        )}
      >
        <span className={cn("min-w-0", truncate && "truncate")} title={truncate ? value : undefined}>
          {truncate && value.length > 24 ? `${value.slice(0, 24)}…` : value}
        </span>
        {copy && value && value !== "—" && (
          <button
            type="button"
            onClick={handleCopy}
            className={cn(
              "inline-flex h-6 w-6 items-center justify-center rounded-full border border-border",
              "text-muted-foreground hover:text-ink hover:bg-muted",
              "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-cordum/40",
            )}
            aria-label={`Copy ${label} to clipboard`}
          >
            {copied ? (
              <Check className="h-3 w-3 text-success" aria-hidden="true" />
            ) : (
              <Copy className="h-3 w-3" aria-hidden="true" />
            )}
          </button>
        )}
      </dd>
    </div>
  );
}
