import { useState, useCallback } from "react";
import { Copy, Check, ChevronDown } from "lucide-react";
import { toast } from "sonner";
import { cn } from "@/lib/utils";

const MAX_DISPLAY_BYTES = 100 * 1024; // 100KB — truncate beyond this

export interface CodeBlockProps {
  children?: string | null;
  title?: string;
  language?: string;
  maxHeight?: number;
  collapsible?: boolean;
  defaultExpanded?: boolean;
  copyable?: boolean;
  className?: string;
  /**
   * Compact inline variant — renders as a single mono-text button (or span)
   * rather than the full Mac-chrome code block. Used for hash chips, short
   * IDs, and other places where a one-line copyable token is appropriate.
   *
   * When `inline` + `copyable`, clicking the button writes the FULL `children`
   * value to the clipboard (not the truncated display preview).
   */
  inline?: boolean;
  /**
   * Inline-only: max characters to display before truncating. The full value
   * is still what gets copied. Defaults to 8 (the audit-hash chip contract).
   */
  inlineMaxLength?: number;
  /**
   * Inline-only: a11y label for the button. Pass the full hash so screen
   * readers can announce what gets copied.
   */
  ariaLabel?: string;
  /**
   * Inline-only: title attribute (hover tooltip). Defaults to a copy hint.
   */
  inlineTitle?: string;
}

export function CodeBlock({
  children,
  title,
  language,
  maxHeight = 400,
  collapsible = false,
  defaultExpanded = true,
  copyable = true,
  className,
  inline = false,
  inlineMaxLength = 8,
  ariaLabel,
  inlineTitle,
}: CodeBlockProps) {
  const [expanded, setExpanded] = useState(defaultExpanded);
  const [copied, setCopied] = useState(false);
  const [showFull, setShowFull] = useState(false);

  const raw = children ?? "";
  const isTruncated = !showFull && raw.length > MAX_DISPLAY_BYTES;
  const displayContent = isTruncated ? raw.slice(0, MAX_DISPLAY_BYTES) : raw;
  const isEmpty = !raw.trim();

  const handleCopy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(raw);
      setCopied(true);
      toast.success("Copied to clipboard");
      setTimeout(() => setCopied(false), 2000);
    } catch {
      toast.error("Copy failed");
    }
  }, [raw]);

  // Inline (compact chip) variant — used for hash chips and other short
  // copyable tokens. Skips the chrome and renders as a single mono button.
  if (inline) {
    if (isEmpty) {
      return (
        <span
          aria-label={ariaLabel}
          className={cn(
            "inline-flex h-4 items-center rounded font-mono text-[10px] tracking-tight px-1 text-muted-foreground/60 bg-surface-2/40",
            className,
          )}
        >
          —
        </span>
      );
    }
    const previewLength = Math.max(1, inlineMaxLength);
    const truncated = raw.length > previewLength ? raw.slice(0, previewLength) : raw;
    if (!copyable) {
      return (
        <span
          aria-label={ariaLabel}
          title={inlineTitle ?? raw}
          className={cn(
            "inline-flex h-4 items-center rounded font-mono text-[10px] tracking-tight px-1 text-foreground bg-surface-2",
            className,
          )}
        >
          {truncated}
        </span>
      );
    }
    return (
      <button
        type="button"
        onClick={(event) => {
          event.stopPropagation();
          void handleCopy();
        }}
        aria-label={ariaLabel ?? `Copy ${raw}`}
        title={inlineTitle ?? `${raw} — click to copy`}
        className={cn(
          "inline-flex h-4 items-center rounded font-mono text-[10px] tracking-tight px-1 text-foreground bg-surface-2 hover:bg-surface-3 focus:outline-none focus:ring-1 focus:ring-cordum",
          className,
        )}
        data-copied={copied || undefined}
      >
        {copied ? (
          <Check className="w-3 h-3" aria-hidden="true" />
        ) : (
          truncated
        )}
      </button>
    );
  }

  return (
    <div
      role="region"
      aria-label={title ?? "Code block"}
      className={cn("rounded-2xl border border-border/50 overflow-hidden", className)}
    >
      {/* Title bar — Mac-style chrome */}
      <div className="flex items-center gap-3 px-4 py-2.5 bg-[#161b1e] border-b border-white/5">
        {/* Traffic lights (decorative) */}
        <div className="flex gap-1.5 shrink-0" aria-hidden="true">
          <span className="w-2.5 h-2.5 rounded-full bg-[#ff5f57]" />
          <span className="w-2.5 h-2.5 rounded-full bg-[#febc2e]" />
          <span className="w-2.5 h-2.5 rounded-full bg-[#28c840]" />
        </div>

        {/* Title */}
        {title && (
          <span className="flex-1 text-center text-xs font-mono text-white/50 truncate">
            {title}
          </span>
        )}
        {!title && <span className="flex-1" />}

        {/* Right side: language badge + copy + collapse toggle */}
        <div className="flex items-center gap-2 shrink-0">
          {language && (
            <span className="text-[10px] font-mono uppercase tracking-wider text-white/30 px-1.5 py-0.5 rounded bg-white/5">
              {language}
            </span>
          )}
          {copyable && !isEmpty && (
            <button
              type="button"
              onClick={handleCopy}
              aria-label="Copy code"
              className="p-1 rounded text-white/30 hover:text-white/70 transition-colors"
            >
              {copied ? <Check className="w-3.5 h-3.5" /> : <Copy className="w-3.5 h-3.5" />}
            </button>
          )}
          {collapsible && (
            <button
              type="button"
              onClick={() => setExpanded(!expanded)}
              aria-label={expanded ? "Collapse code" : "Expand code"}
              className="p-1 rounded text-white/30 hover:text-white/70 transition-colors"
            >
              <ChevronDown
                className={cn(
                  "w-3.5 h-3.5 transition-transform duration-150",
                  expanded && "rotate-180",
                )}
              />
            </button>
          )}
        </div>
      </div>

      {/* Content */}
      <div
        className={cn(
          "transition-all duration-200 overflow-hidden",
          !expanded && collapsible && "max-h-0",
        )}
      >
        {isEmpty ? (
          <div className="px-4 py-6 bg-[#0f1416] text-center text-xs text-white/20 font-mono">
            No content
          </div>
        ) : (
          <div
            className="bg-[#0f1416] overflow-y-auto"
            style={{ maxHeight: expanded ? maxHeight : 0 }}
          >
            <pre className="px-4 py-3 font-mono text-xs leading-relaxed text-white/85 whitespace-pre overflow-x-auto">
              {displayContent}
              {isTruncated && (
                <>
                  {"\n"}
                  <span className="text-white/30">
                    ... (truncated, {Math.round(raw.length / 1024)}KB total){" "}
                    <button
                      type="button"
                      onClick={() => setShowFull(true)}
                      className="underline text-white/40 hover:text-white/60"
                    >
                      Show all
                    </button>
                  </span>
                </>
              )}
            </pre>
          </div>
        )}
      </div>
    </div>
  );
}
