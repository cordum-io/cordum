# Design Language Mapping

Maps concepts from the "Control Surface" design language spec (`cordum-dashboard-design-language.md` v0.6.0) to their concrete implementations in the dashboard codebase. Use this as a reference when building or modifying components to ensure design parity.

For validation status of each route, see [DESIGN_PARITY_CHECKLIST.md](./DESIGN_PARITY_CHECKLIST.md).

---

## Surface Hierarchy

The design uses layered surfaces to create depth without drop shadows. All surface tokens are CSS custom properties defined in the theme and consumed via Tailwind utilities.

| Token | Value (Dark) | Usage | Codebase Location |
|-------|-------------|-------|--------------------|
| `--surface-glass` | `rgba(255,255,255,0.03)` | AppShell sidebar, header bar | `AppShell.tsx` — sidebar/header background |
| `--surface1` | `rgba(255,255,255,0.06)` | Card backgrounds, table rows, panel bodies | Used by instrument cards, table components |
| `--surface2` | `rgba(255,255,255,0.09)` | Nested elements, hover states, inset panels | Table row hover, expandable detail areas |
| `--surface3` | `rgba(255,255,255,0.12)` | Active states, selected items | Active sidebar item, selected table row |

Surfaces stack without opacity compounding — each level is an absolute value, not additive.

---

## Color Tokens

### Core Palette

| Token | Purpose | CSS Variable |
|-------|---------|-------------|
| `ink` | Primary text, headings | `--color-ink` |
| `muted` | Secondary text, labels, timestamps | `--color-muted` |
| `accent` | Cordum teal, primary actions, active indicators | `--color-accent` |
| `accent-hover` | Hover state for accent elements | `--color-accent-hover` |

### Semantic Colors

| Token | Purpose | Used For |
|-------|---------|----------|
| `danger` | Destructive states, errors | FAILED, DENIED, TIMEOUT statuses, error badges |
| `warning` | Caution states, pending attention | PENDING, APPROVAL_REQUIRED, warning indicators |
| `success` | Positive states, completion | SUCCEEDED, approved, healthy indicators |
| `info` | Informational, neutral highlights | RUNNING, DISPATCHED, in-progress states |

All semantic colors have matching `-muted` variants (e.g., `--color-danger-muted`) for badge backgrounds and subtle indicators.

---

## Typography

Three font families serve distinct roles. Loaded via `font-display: swap` to prevent invisible text during load.

| Token | Font | Weight Range | Usage |
|-------|------|-------------|-------|
| `font-display` | Plus Jakarta Sans | 600-700 | Page titles, section headings, metric values |
| `font-sans` | Inter | 400-500 | Body text, labels, descriptions, table cells |
| `font-mono` | JetBrains Mono | 400 | Code snippets, IDs, hashes, JSON payloads |

Base size: 14px. Scale follows a compact ratio — headings are restrained to maintain density.

---

## Component Patterns

### Instrument Cards
Primary data display unit on the Security Overview page.
- 2px `accent` top border (the "instrument" signature)
- `surface1` background
- Metric value in `font-display` 600 weight
- Label in `font-sans` `muted` color
- Implementation: metric card components in `src/components/`

### Status Badges
Consistent across Jobs, Runs, and Approvals pages.
- Pill shape, semantic background (`danger-muted`, `success-muted`, etc.)
- Text in corresponding semantic color
- `font-mono` for status text (uppercase)
- Always paired with text — color is never the sole indicator

### Table Rows
Used on Jobs, Approvals, and data-dense pages.
- 48px row height (fixed rhythm)
- `surface1` default, `surface2` on hover
- Sort indicators use `muted` → `ink` transition on active column
- Pagination controls at table footer

### Metric Cards
Used on Security Overview and dashboard summary areas.
- Large numeric value in `font-display`
- Trend indicator (up/down arrow) with semantic color
- Compact footprint on 4px grid

---

## Spacing System

Built on a strict 4px base grid.

| Increment | Value | Common Use |
|-----------|-------|-----------|
| 1x | 4px | Inline spacing, icon gaps |
| 2x | 8px | Component internal padding |
| 3x | 12px | Card padding, form field spacing |
| 4x | 16px | Section gaps, card margins |
| 6x | 24px | Page section separation |
| 8x | 32px | Major layout gaps |
| 12x | 48px | Table row height, sidebar item rhythm |

The 48px row rhythm is a key density signature — it keeps tables scannable while fitting maximum data on screen.

---

## Theme Implementation

- CSS variables defined in `src/index.css` (root scope)
- Dark theme is the default; light theme overrides via `.light` class
- Tailwind config extends theme with CSS variable references
- `cn()` utility (from `src/lib/utils.ts`) merges conditional classes cleanly
- No inline styles — all visual properties flow through tokens

---

## Quick Reference: Token to Code

```
Design Spec          →  CSS Variable         →  Tailwind Class
surface-glass        →  --surface-glass       →  bg-surface-glass
surface1             →  --surface1            →  bg-surface1
accent               →  --color-accent        →  text-accent / bg-accent
ink                  →  --color-ink           →  text-ink
muted                →  --color-muted         →  text-muted
danger               →  --color-danger        →  text-danger / bg-danger
font-display         →  font-family           →  font-display
font-mono            →  font-family           →  font-mono
```

For parity validation status across all routes, see [DESIGN_PARITY_CHECKLIST.md](./DESIGN_PARITY_CHECKLIST.md).
