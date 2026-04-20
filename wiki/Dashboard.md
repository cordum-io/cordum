# Dashboard Design System & Parity

The Cordum dashboard follows the **Control Surface** design language: a dark-first,
compact, data-dense interface built for infrastructure operators. Every surface,
color, and typographic choice is driven by CSS custom properties so themes can be
swapped without touching component code.

## Design System Usage

### CSS Variables (Theming)

All colors and surfaces are defined as CSS custom properties in `dashboard/src/styles/index.css`:

| Token | Purpose |
|-------|---------|
| `--surface-glass` | Primary card/panel background (frosted glass) |
| `--ink` | Default text color |
| `--muted` | Secondary/dimmed text |
| `--accent` | Interactive highlights, links, focus rings |
| `--success`, `--warning`, `--danger` | Semantic status colors |
| `--font-sans`, `--font-mono` | Typography stacks |

### TailwindCSS Utilities

The Tailwind config maps CSS variables to utility classes:

- `bg-surface1`, `bg-surface2`, `bg-surface3` -- layered surface backgrounds
- `text-ink`, `text-muted`, `text-accent` -- semantic text colors
- `font-mono` -- monospace for IDs, hashes, code
- `border-white/5`, `border-white/10` -- subtle dividers

### Class Merging

Use the `cn()` utility (`dashboard/src/lib/utils.ts`) to merge Tailwind classes
safely, avoiding duplicate or conflicting classes:

```tsx
import { cn } from '@/lib/utils';
<div className={cn('rounded-lg bg-surface1', isActive && 'ring-1 ring-accent')} />
```

### Component Patterns

| Component | File | Usage |
|-----------|------|-------|
| `Card` | `components/ui/Card.tsx` | Container with glass background |
| `Badge` | `components/ui/Badge.tsx` | Status labels, counts |
| `MetricCard` | `components/ui/MetricCard.tsx` | KPI display with trend |
| `StatusBadge` | `components/ui/StatusBadge.tsx` | Job/run state indicator |

## Parity Validation

Run the full validation sequence after any styling or theming changes:

```bash
cd dashboard
node ./node_modules/typescript/bin/tsc --noEmit
npx vitest run src/styles/design-parity.test.ts src/styles/theme-tokens.test.ts
npx vitest run
npm run build
```

Guard tests in `src/styles/` verify that CSS variable tokens, Tailwind mappings,
and component classes stay aligned with the spec.

## Intentional Deviations

No major deviations from the Control Surface spec at this time. Minor
implementation details (e.g., exact border-radius values, transition durations)
are documented inline where they differ.

## Cross-Platform Notes

- **Windows/MSYS**: use forward slashes in all paths.
- TypeScript check: always use `node ./node_modules/typescript/bin/tsc --noEmit`
  (not `npx tsc`, which can resolve the wrong binary on Windows).
- Vitest and Vite work normally under MSYS bash.

## Further Reading

- [`dashboard/DESIGN_PARITY_CHECKLIST.md`](../dashboard/DESIGN_PARITY_CHECKLIST.md) -- detailed token-by-token evidence
- [`dashboard/DESIGN_LANGUAGE_MAPPING.md`](../dashboard/DESIGN_LANGUAGE_MAPPING.md) -- spec-to-code mapping
- [`cordum-dashboard-design-language.md`](../cordum-dashboard-design-language.md) -- full design language spec (v0.6.0)
