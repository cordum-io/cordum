import { Button } from "@/components/ui/Button";

interface PolicyEmptyConfigCardProps {
  title: string;
  description: string;
  ctaLabel?: string;
  onCtaClick?: () => void;
}

function invokeEmptyConfigCta(onCtaClick?: () => void): void {
  onCtaClick?.();
}

export function PolicyEmptyConfigCard({
  title,
  description,
  ctaLabel,
  onCtaClick,
}: PolicyEmptyConfigCardProps) {
  return (
    <div className="rounded-xl border border-border bg-surface-0 p-4 text-center">
      <h4 className="text-xs font-semibold text-foreground">{title}</h4>
      <p className="mt-1 text-xs text-muted-foreground">{description}</p>
      {ctaLabel && onCtaClick && (
        <Button
          size="sm"
          variant="outline"
          className="mt-3"
          onClick={() => invokeEmptyConfigCta(onCtaClick)}
        >
          {ctaLabel}
        </Button>
      )}
    </div>
  );
}

export const __policyEmptyConfigCardInternal = {
  invokeEmptyConfigCta,
};

