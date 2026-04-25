# Cordum Dashboard Design Language

**Version 0.6.0** · February 2025 · cordum.io
> **Implementation traceability:** Current dashboard rollout mapping lives in dashboard/DESIGN_LANGUAGE_MAPPING.md.


---

## Foreword

> *"Every pixel serves a purpose."*

The Cordum Dashboard Design Language is the visual and interaction framework for the Cordum control plane — the governance layer that sits between AI agents and the real world. This document codifies every decision, from the reasoning behind a single accent color to the spatial hierarchy of layered surfaces. It exists so that every contributor, designer, and engineer builds with the same vocabulary, the same intent, and the same standard of craft.

Cordum is not a consumer app. It is a **mission-critical control surface** — the place where operators approve or deny autonomous actions, where policies are written and enforced, where audit trails are reviewed under pressure. The design language reflects this gravity. It borrows from aerospace instrument panels and mission control interfaces, where clarity is not a luxury but a requirement.

---

## 1. Design Philosophy — "Premium Soft Control Surface"

The design philosophy is named **Premium Soft Control Surface**, the pinnacle of our dashboard evolution. It moves beyond utilitarian instruments into a multi-dimensional, high-fidelity experience that communicates authority and precision through depth and motion.

### 1.1 Core Principles

Six principles govern every design decision. They are not aspirational — they are constraints. When a component, layout, or interaction is proposed, it must satisfy at least one of these principles and violate none.

| Principle | Description | Implication |
| :--- | :--- | :--- |
| **Status-First Design** | The most important thing is always what is happening *now*. The UI recedes when healthy and demands attention when not. | Default states are quiet. Alerts and pending states use semantic gradients and pulsing indicators to break the visual field. |
| **Glassmorphic Layering** | Persistent shells (Sidebar, Header) use translucent `glass-sidebar` and `glass-header` primitives. This creates a multi-dimensional feel that separates control from content. | The "Mission Control" shell stays anchored while content flows underneath with a `12px` backdrop blur. |
| **Bento-Grid Logic** | Layouts are organized into non-uniform CSS Grids where card size communicates data priority. | Information is not just listed; it is prioritized by area. High-velocity data (Safety Feed) gets more vertical "runway." |
| **Quiet Confidence** | Restrained color usage gives semantic meaning. When teal appears, it carries real significance. | We use OKLCH-based gradients for data visualizations to make complex patterns immediately scannable without adding visual noise. |
| **Fluid Micro-Interactions** | 250ms `ease-out` transitions. Page entries are staggered. Feeds use `layout` animations for smooth reordering. | The UI feels "alive." Changes in state are seen as transitions, not sudden jumps, reducing cognitive load for operators. |
| **Three Typographic Voices** | Plus Jakarta Sans for authority, Inter for clarity, JetBrains Mono for precision. Each serves a distinct role. | Font weights and tracking are refined at small sizes (e.g., `text-[10px] tracking-widest`) to maintain premium legibility. |

### 1.2 The Governing Metaphor

Think of the Cordum dashboard as a **flight deck instrument panel**. The pilot (operator) needs to:

1. **See system health at a glance** — green lights mean nominal, amber means attention, red means act now.
2. **Read precise data without ambiguity** — monospaced fonts for values, clear units, no rounding errors hidden behind "pretty" formatting.
3. **Take decisive action with confidence** — primary buttons are unmistakable, destructive actions require confirmation, and every click has visible feedback.
4. **Trust the instruments** — the UI never lies. If a metric says 98.2%, the underlying data supports it. Loading states are explicit, not hidden behind stale data.

---

## 2. Color System

The Cordum color system is designed for **dark environments** where operators may spend hours monitoring agent activity. Colors are restrained by default — when the teal accent appears, it carries real significance. The system is built on the OKLCH color space for perceptual uniformity.

### 2.1 Primary Accent — Cordum Teal

The signature teal is the single most important color in the system. It is reserved for:

- **Healthy / approved states** (badges, status dots)
- **Primary actions** (buttons, links)
- **Active navigation** (sidebar highlights, tab indicators)
- **Brand moments** (logo, version badge)

Its restrained usage ensures it always commands attention. If everything is teal, nothing is.

| Token | Hex | OKLCH | Usage |
| :--- | :--- | :--- | :--- |
| `cordum-100` | `#E6FFF6` | — | Tinted backgrounds (very subtle) |
| `cordum-200` | `#B3FFE2` | — | Light accent fills |
| `cordum-300` | `#80FFC4` | — | Hover states on dark surfaces |
| `cordum-400` | `#4DFFAB` | — | Bright highlights, sparklines |
| `cordum-500` | `#00E5A0` | `oklch(0.82 0.18 165)` | **Primary accent** — buttons, active states, healthy indicators |
| `cordum-600` | `#00B880` | — | Hover state for primary buttons |
| `cordum-700` | `#008A60` | — | Pressed state, dark-on-dark accents |
| `cordum-800` | `#005C40` | — | Subtle borders on teal-tinted surfaces |
| `cordum-900` | `#002E20` | — | Deep teal for background tints |

**CSS Variable:**
```css
--color-cordum: oklch(0.82 0.18 165);
--color-cordum-dim: oklch(0.65 0.14 165);
--color-cordum-bright: oklch(0.90 0.20 165);
```

### 2.2 Surface Layers

Layered surfaces create spatial hierarchy without relying on heavy shadows. Each level is slightly lighter than the previous, establishing depth through luminance alone. This is the backbone of the "Layered Depth" principle.

| Token | Hex | OKLCH | Usage |
| :--- | :--- | :--- | :--- |
| `background` | `#111827` | `oklch(0.13 0.01 260)` | Page background — the deepest layer |
| `surface-0` | `#151B28` | `oklch(0.15 0.01 260)` | Sidebar, secondary panels |
| `surface-1` | `#1A2233` | `oklch(0.18 0.01 260)` | Cards, primary containers |
| `surface-2` | `#1F293D` | `oklch(0.21 0.01 260)` | Hover states, elevated cards |
| `surface-3` | `#253248` | `oklch(0.25 0.01 260)` | Active states, dropdowns, popovers |
| `border` | `#2A3548` | `oklch(0.28 0.01 260)` | All borders and dividers |
| `input` | `#253248` | `oklch(0.25 0.01 260)` | Input field backgrounds |

**The luminance progression is intentional:** each step increases lightness by approximately `0.03` in OKLCH, creating a perceptible but subtle lift. The hue angle (`260`) adds a cool blue undertone that prevents the grays from feeling muddy or warm.

```
Background (0.13) → Surface 0 (0.15) → Surface 1 (0.18) → Surface 2 (0.21) → Surface 3 (0.25)
```

### 2.3 Text Hierarchy

Three levels of text emphasis create clear information hierarchy without relying on size alone. The foreground colors are calibrated against the surface layers to maintain WCAG AA contrast ratios.

| Token | OKLCH | Contrast vs Surface 1 | Usage |
| :--- | :--- | :--- | :--- |
| `foreground` | `oklch(0.90 0.005 260)` | ~8.5:1 | Primary text — headings, values, labels |
| `secondary-foreground` | `oklch(0.80 0.005 260)` | ~6.2:1 | Secondary text — descriptions, subtitles |
| `muted-foreground` | `oklch(0.60 0.01 260)` | ~3.5:1 | Tertiary text — timestamps, helper text, captions |

### 2.4 Semantic Status Colors

Status colors map directly to governance states. Each carries a specific, unambiguous meaning in the context of agent control. These colors are never used decoratively — they always communicate state.

| Status | Token | Hex | OKLCH | Governance Meaning |
| :--- | :--- | :--- | :--- | :--- |
| **Success** | `success` | `#10B981` | `oklch(0.75 0.17 155)` | Approved, healthy, passing, nominal |
| **Warning** | `warning` | `#F59E0B` | `oklch(0.80 0.16 75)` | Pending approval, elevated latency, attention needed |
| **Danger** | `danger` / `destructive` | `#EF4444` | `oklch(0.65 0.22 25)` | Denied, error, failed, critical |
| **Info** | `info` | `#3B82F6` | `oklch(0.70 0.15 250)` | Informational, constrained, neutral-active |

**Badge construction pattern:**
```
Background: {status-color} at 15% opacity
Text: {status-color} at 100% (400-weight variant)
Border: {status-color} at 20% opacity
Icon: {status-color} at 100%, 12px
```

### 2.5 Chart Palette

A five-color palette optimized for data visualization on dark backgrounds. The colors are chosen for maximum distinguishability at small sizes (legend dots, pie slices, line charts).

| Token | Color | OKLCH | Assigned Meaning |
| :--- | :--- | :--- | :--- |
| `chart-1` | Cordum Teal | `oklch(0.82 0.18 165)` | Approved / primary metric |
| `chart-2` | Blue | `oklch(0.70 0.15 250)` | Informational / secondary metric |
| `chart-3` | Amber | `oklch(0.80 0.16 75)` | Pending / warning metric |
| `chart-4` | Red | `oklch(0.65 0.22 25)` | Denied / error metric |
| `chart-5` | Green | `oklch(0.75 0.17 155)` | Success / tertiary metric |

---

## 3. Typography

The typographic system uses **three distinct voices**, each serving a specific role. This is not a stylistic choice — it is a functional one. When an operator glances at the screen, the font itself communicates what kind of information they are reading before they process the words.

### 3.1 Font Families

| Voice | Font | Weights | Role | CSS Variable |
| :--- | :--- | :--- | :--- | :--- |
| **Display** | Plus Jakarta Sans | 500, 600, 700, 800 | Page titles, section headings, navigation labels. Geometric, confident, slightly wider than Inter. Conveys authority. | `--font-display` |
| **Body** | Inter | 400, 500, 600 | Body text, UI labels, descriptions, form labels. Clean, readable, versatile. Conveys clarity. | `--font-sans` |
| **Data** | JetBrains Mono | 400, 500, 600 | Metric values, code blocks, job IDs, policy YAML, timestamps. Fixed-width for alignment. Conveys precision. | `--font-mono` |

```css
--font-display: "Plus Jakarta Sans", system-ui, sans-serif;
--font-sans: "Inter", -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
--font-mono: "JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, monospace;
```

**Automatic assignment in CSS:**
```css
body { font-family: var(--font-sans); }
h1, h2, h3, h4, h5, h6 { font-family: var(--font-display); }
code, pre, .font-mono { font-family: var(--font-mono); }
```

### 3.2 Type Scale

A modular scale from 12px to 36px ensures consistent hierarchy across all dashboard views. Each step has a defined font size, line height, and recommended usage.

| Token | Size | Line Height | Recommended Weight | Usage |
| :--- | :--- | :--- | :--- | :--- |
| `text-4xl` | 36px | 40px | 800 (ExtraBold) | Hero titles, page-level headings |
| `text-3xl` | 30px | 36px | 700 (Bold) | Section headings |
| `text-2xl` | 24px | 32px | 600 (SemiBold) | Sub-section headings, card titles |
| `text-xl` | 20px | 28px | 600 (SemiBold) | Widget titles, dialog headings |
| `text-lg` | 18px | 28px | 500 (Medium) | Large body text, feature descriptions |
| `text-base` | 16px | 24px | 400 (Regular) | Default body text, input values |
| `text-sm` | 14px | 20px | 400 (Regular) | Table content, secondary labels, descriptions |
| `text-xs` | 12px | 16px | 500 (Medium) | Captions, timestamps, helper text, uppercase labels |

### 3.3 Typographic Patterns

**Section Header Pattern:**
```
[UPPERCASE LABEL]  ← text-xs, font-mono, cordum teal, tracking-widest
[Heading]          ← text-2xl, font-display, font-semibold, foreground
[Description]      ← text-sm, font-sans, muted-foreground, max-w-xl
```

**Metric Card Pattern:**
```
[UPPERCASE LABEL]  ← text-xs, font-mono, muted-foreground, tracking-widest
[Large Value]      ← text-3xl, font-mono, font-bold, foreground
[Subtext]          ← text-xs, font-sans, muted-foreground
```

**Code Block Pattern:**
```
[Filename Tab]     ← text-xs, font-mono, muted-foreground
[Code Content]     ← text-sm, font-mono, foreground, bg-surface-0
```

---

## 4. Spacing & Layout

### 4.1 Spacing Scale

All spacing is based on a **4px grid**. Every margin, padding, and gap uses a value from this scale. No arbitrary pixel values.

| Token | Value | Pixels | Common Usage |
| :--- | :--- | :--- | :--- |
| `space-1` | 0.25rem | 4px | Tight gaps (icon-to-text in badges) |
| `space-1.5` | 0.375rem | 6px | Badge padding (vertical) |
| `space-2` | 0.5rem | 8px | Compact card padding, small gaps |
| `space-3` | 0.75rem | 12px | Input padding, button padding (vertical) |
| `space-4` | 1rem | 16px | Default gap between elements, mobile container padding |
| `space-5` | 1.25rem | 20px | Card internal spacing |
| `space-6` | 1.5rem | 24px | Card padding, section gaps, tablet container padding |
| `space-8` | 2rem | 32px | Desktop container padding, large section gaps |
| `space-10` | 2.5rem | 40px | Page-level vertical rhythm |
| `space-12` | 3rem | 48px | Major section separators |
| `space-16` | 4rem | 64px | Hero section padding |

### 4.2 Layout Structure

The dashboard uses a **persistent sidebar + scrollable content area** layout. This is the canonical layout for all internal dashboard views.

```
┌─────────────────────────────────────────────────────┐
│  Sidebar (240px, fixed)  │  Content Area (fluid)    │
│                          │                          │
│  ┌──────────────────┐    │  ┌─────────────────────┐ │
│  │  Logo + Title    │    │  │  Top Bar            │ │
│  │                  │    │  │  (search, user, etc) │ │
│  │  Nav Items       │    │  ├─────────────────────┤ │
│  │  · Overview      │    │  │                     │ │
│  │  · Agents        │    │  │  Page Content       │ │
│  │  · Workflows     │    │  │  (scrollable)       │ │
│  │  · Data          │    │  │                     │ │
│  │  · Compliance    │    │  │  ┌───┐ ┌───┐ ┌───┐ │ │
│  │  · Settings      │    │  │  │   │ │   │ │   │ │ │
│  │                  │    │  │  │KPI│ │KPI│ │KPI│ │ │
│  │  ──────────────  │    │  │  │   │ │   │ │   │ │ │
│  │  Profile         │    │  │  └───┘ └───┘ └───┘ │ │
│  └──────────────────┘    │  │                     │ │
│                          │  │  ┌─────────────────┐│ │
│                          │  │  │  Chart / Table   ││ │
│                          │  │  └─────────────────┘│ │
│                          │  └─────────────────────┘ │
└─────────────────────────────────────────────────────┘
```

**Sidebar specifications:**

| Property | Value |
| :--- | :--- |
| Width | 240px (expanded), 64px (collapsed) |
| Background | `surface-0` (`oklch(0.15 0.01 260)`) |
| Border | 1px right border, `border` token |
| Logo area | 64px height, centered |
| Nav item height | 40px |
| Nav item padding | 12px horizontal |
| Active indicator | 2px left border, `cordum` color + `cordum/10` background |
| Collapse trigger | Chevron button at sidebar edge |

**Content area specifications:**

| Property | Value |
| :--- | :--- |
| Max width | 1400px (centered with `mx-auto`) |
| Padding | 16px (mobile), 24px (tablet), 32px (desktop) |
| Top bar height | 56px |
| Top bar background | `background` with `backdrop-blur` |

### 4.3 Grid Patterns

**KPI Row:** 4-column grid on desktop, 2-column on tablet, 1-column on mobile. Each KPI card is an instrument card with a 2px top accent line.

**Chart + Sidebar:** 2/3 + 1/3 split on desktop, stacked on mobile. The larger panel holds the primary chart; the smaller panel holds a secondary visualization (pie chart, legend, or summary).

**Data Table:** Full-width within the content area. Fixed header, scrollable body. Minimum row height of 48px for comfortable click targets.

### 4.4 Border Radius

| Token | Value | Usage |
| :--- | :--- | :--- |
| `radius-sm` | 4px | Badges, small pills, inline tags |
| `radius-md` | 6px | Buttons, inputs, small cards |
| `radius-lg` | 8px | Cards, dialogs, panels |
| `radius-xl` | 12px | Large containers, hero sections, image frames |
| `rounded-full` | 9999px | Avatars, status dots, circular icon buttons |

---

## 5. Components

Every component follows the Control Surface philosophy: instrument-grade clarity with semantic color usage. Components are built on shadcn/ui primitives and extended with Cordum-specific styling.

### 5.1 Buttons

Buttons are the primary action affordance. Variants map to intent, not aesthetics.

| Variant | Background | Text | Border | Usage |
| :--- | :--- | :--- | :--- | :--- |
| **Primary** | `cordum-500` | `surface-0` (dark) | none | Main CTA — "Run Safety Check", "Approve", "Deploy" |
| **Secondary** | `surface-2` | `foreground` | none | Supporting actions — "Cancel", "Back" |
| **Outline** | transparent | `foreground` | `border` | Tertiary actions — "Export", "Filter" |
| **Ghost** | transparent | `muted-foreground` | none | Inline actions — icon buttons, "More" menus |
| **Destructive** | `danger` | white | none | Dangerous actions — "Deny", "Delete", "Revoke" |

**Sizes:**

| Size | Height | Padding (H) | Font Size | Icon Size |
| :--- | :--- | :--- | :--- | :--- |
| `sm` | 32px | 12px | 13px | 14px |
| `default` | 36px | 16px | 14px | 16px |
| `lg` | 40px | 20px | 14px | 16px |
| `icon` | 36×36px | — | — | 16px |

**Interaction states:**
- **Hover:** Primary buttons increase brightness (`brightness-110`). Secondary/outline buttons shift to `surface-3`.
- **Focus:** Double-ring pattern — inner ring `cordum` at 30% opacity, outer ring `cordum` at 15% opacity.
- **Active/Pressed:** Scale to `0.98` with `50ms` transition.
- **Disabled:** 50% opacity, `cursor-not-allowed`.

### 5.2 Instrument Cards

The signature component of the Cordum design system. Instrument cards are containers with a **2px top-edge accent line** that carries semantic meaning. The accent line is the first thing the eye catches, communicating status before the content is read.

```css
.instrument-card {
  background: var(--card);           /* surface-1 */
  border-radius: var(--radius-lg);   /* 8px */
  border: 1px solid var(--border);
  position: relative;
  overflow: hidden;
}

.instrument-card::before {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  height: 2px;
  background: var(--color-cordum);   /* default: healthy/nominal */
}
```

**Status variants:**

| Variant | Accent Color | Meaning |
| :--- | :--- | :--- |
| Default | `cordum` (teal) | Nominal, healthy, approved |
| `.status-warning` | `warning` (amber) | Attention needed, pending |
| `.status-danger` | `danger` (red) | Error, denied, critical |
| `.status-info` | `info` (blue) | Informational, constrained |

**KPI Card anatomy (inside an instrument card):**
```
┌──────────────────────────────────────┐
│ ▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬ │  ← 2px accent line
│                                      │
│  TOTAL JOBS          ↗ icon          │  ← text-xs, mono, muted, uppercase
│  12,847              ▲ 4.5%          │  ← text-3xl, mono, bold + text-xs, cordum
│  Last 30 days                        │  ← text-xs, muted-foreground
│                                      │
└──────────────────────────────────────┘
```

### 5.3 Badges & Status Indicators

Badges are pill-shaped labels that communicate governance state. They combine an icon, a label, and semantic color.

**Status Badge construction:**

| Status | Icon | Background | Text | Border |
| :--- | :--- | :--- | :--- | :--- |
| Approved | `CheckCircle2` | `emerald-500/15` | `emerald-400` | `emerald-500/20` |
| Pending | `Clock` | `amber-500/15` | `amber-400` | `amber-500/20` |
| Denied | `XCircle` | `red-500/15` | `red-400` | `red-500/20` |
| Running | `Activity` | `blue-500/15` | `blue-400` | `blue-500/20` |
| Warning | `AlertTriangle` | `amber-500/15` | `amber-400` | `amber-500/20` |

**Policy Decision Badges** (monospaced, uppercase, no icon):

| Decision | Style |
| :--- | :--- |
| `ALLOW` | `cordum/15` bg, `cordum` text |
| `DENY` | `red-500/15` bg, `red-400` text |
| `REQUIRE_APPROVAL` | `amber-500/15` bg, `amber-400` text |
| `ALLOW_WITH_CONSTRAINTS` | `blue-500/15` bg, `blue-400` text |

**Live Status Indicators:**

Used in the sidebar and system health panels. A pulsing dot + label + optional metric value.

```
● System Healthy          99.9%    ← dot: cordum, pulse animation
● Elevated Latency        340ms    ← dot: amber, pulse animation
● Worker Pool Down        0/3      ← dot: red, pulse animation
```

The pulse animation:
```css
@keyframes status-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.5; }
}
.status-pulse {
  animation: status-pulse 2s ease-in-out infinite;
}
```

### 5.4 Input Fields

Inputs follow the dark surface pattern with clear focus states.

| State | Background | Border | Text | Ring |
| :--- | :--- | :--- | :--- | :--- |
| Default | `input` (surface-3) | `border` | `foreground` | none |
| Focused | `input` | `cordum` | `foreground` | `cordum/30` |
| Error | `input` | `red-500/50` | `foreground` | `red-500/30` |
| Disabled | `surface-0` | `border` (dimmed) | `muted-foreground` | none |

**Search input pattern:** Includes a `Search` icon (lucide) at `left: 12px`, with `padding-left: 36px` on the input to accommodate it.

### 5.5 Toggle Switches

Used for binary settings (enable/disable Safety Kernel, Audit Logging, etc.). Built on Radix UI Switch.

| State | Track | Thumb |
| :--- | :--- | :--- |
| Off | `surface-3` | `muted-foreground` |
| On | `cordum` | `surface-0` (dark) |

### 5.6 Tabs

Tab navigation for switching between related views within a page. The active tab is indicated by a bottom border in `cordum` color.

| Element | Style |
| :--- | :--- |
| Tab list background | `surface-1` with `border` |
| Inactive tab | `muted-foreground` text, transparent background |
| Active tab | `foreground` text, `surface-2` background |
| Active indicator | 2px bottom border, `cordum` color |
| Tab padding | 12px horizontal, 8px vertical |

### 5.7 Data Tables

Tables display structured data with status indicators and row-level actions. They are the workhorse of the dashboard.

| Element | Style |
| :--- | :--- |
| Header row | `surface-0` background, `text-xs`, `font-mono`, `muted-foreground`, uppercase, `tracking-wider` |
| Body row | `surface-1` background, `text-sm`, `foreground` |
| Row hover | `surface-2` background, `250ms` transition (`--duration-soft`) |
| Row border | 1px bottom, `border` token |
| Cell padding | 16px vertical, 24px horizontal |
| Status column | Status badge (see 5.3) |
| Actions column | Ghost icon buttons (`Eye`, `MoreHorizontal`) |
| Pagination | "Showing X–Y of Z results" + Previous/Next buttons |

**Job ID formatting:** Always rendered in `font-mono`, `text-xs`, `muted-foreground`. Example: `job-8a2f3e`.

### 5.8 Progress Bars

Used for approval rates, capacity indicators, and loading states. Built on Radix UI Progress.

| Element | Style |
| :--- | :--- |
| Track | `surface-2`, 6px height, `rounded-full` |
| Fill (default) | `cordum`, `rounded-full` |
| Fill (warning) | `warning` (when value < 50%) |
| Fill (danger) | `danger` (when value < 25%) |

### 5.9 Dialogs & Popovers

Modal dialogs for confirmations, detail views, and forms.

| Element | Style |
| :--- | :--- |
| Overlay | `background` at 80% opacity, `backdrop-blur-sm` |
| Dialog surface | `popover` background, `radius-xl`, `border` |
| Title | `font-display`, `text-lg`, `font-semibold` |
| Description | `text-sm`, `muted-foreground` |
| Actions | Right-aligned, Primary + Secondary buttons |

---

## 6. Data Visualization

Charts are rendered with Recharts on dark backgrounds. The following conventions ensure visual consistency.

### 6.1 General Chart Rules

| Property | Value |
| :--- | :--- |
| Background | Transparent (inherits card background) |
| Grid lines | `border` color at 30% opacity, dashed |
| Axis labels | `text-xs`, `font-mono`, `muted-foreground` |
| Axis lines | Hidden (rely on grid lines) |
| Tooltip background | `surface-3`, `border`, `radius-md` |
| Tooltip text | `text-xs`, `font-mono`, `foreground` |
| Legend | Below chart, `text-xs`, dot indicators |

### 6.2 Area Charts

Used for time-series data (job activity over 24 hours).

- **Fill:** Gradient from `cordum` at 30% opacity (top) to transparent (bottom).
- **Stroke:** `cordum` at 100%, 2px width.
- **Secondary series:** `danger` color for denied/error overlay.
- **Dot:** Hidden by default, shown on hover (6px, `cordum` fill, `surface-1` stroke).

### 6.3 Bar Charts

Used for volume comparisons (weekly job volume, agent throughput).

- **Bar fill:** `cordum` at 100%.
- **Bar radius:** 4px top corners only (`[4, 4, 0, 0]`).
- **Bar hover:** `cordum-bright`, slight scale-up.
- **Bar gap:** 8px between bars.

### 6.4 Pie / Donut Charts

Used for distribution breakdowns (decision distribution, agent allocation).

- **Inner radius:** 60% of outer radius (donut style).
- **Stroke:** `surface-1` at 2px (separates slices).
- **Colors:** Chart palette (see 2.5), mapped to governance states.
- **Label:** Center of donut — large percentage value in `font-mono`.

---

## 7. Interaction & Motion

### 7.1 Transition Defaults

All transitions use a single, consistent timing function. This creates a sense of precision and control — no bouncy, playful animations.

| Property | Value |
| :--- | :--- |
| Duration | `250ms` (micro-interactions, Soft UI; tracked as `--duration-soft`), `300ms` (page transitions), `500ms` (entrance animations) |
| Easing | `ease-out` (CSS) / `{ duration: 0.35 }` (Framer Motion) |
| Stagger | `60ms` between children in lists |

### 7.2 Entrance Animations

Page content enters with a subtle upward fade. This is handled by Framer Motion's `whileInView` with `viewport: { once: true }` to prevent re-triggering on scroll.

```tsx
const stagger = {
  container: { transition: { staggerChildren: 0.06 } },
  item: {
    hidden: { opacity: 0, y: 12 },
    visible: { opacity: 1, y: 0, transition: { duration: 0.35 } },
  },
};
```

### 7.3 Hover & Focus States

| Element | Hover Effect | Focus Effect |
| :--- | :--- | :--- |
| Buttons (primary) | `brightness-110` | Double-ring (`cordum/30` inner, `cordum/15` outer) |
| Cards | `bg-surface-2`, `translateY(-2px)` | `ring-2 ring-cordum/30` |
| Table rows | `bg-surface-2` | — |
| Nav items | `bg-surface-1`, text brightens | `ring-2 ring-cordum/30` |
| Links | `text-cordum`, underline | `ring-2 ring-cordum/30` |

### 7.4 Loading States

- **Skeleton:** `surface-2` background with a shimmer animation (left-to-right gradient sweep).
- **Spinner:** Lucide `Loader2` icon with `animate-spin` class, `cordum` color.
- **Progress:** Linear progress bar (see 5.8) for determinate loading.

---

## 8. Iconography

All icons come from the **Lucide** icon library. Icons are used functionally — never decoratively.

### 8.1 Icon Sizing

| Context | Size | Tailwind Class |
| :--- | :--- | :--- |
| Inline with text-sm | 14px | `w-3.5 h-3.5` |
| Inline with text-base | 16px | `w-4 h-4` |
| Button icons | 16px | `w-4 h-4` |
| Card header icons | 20px | `w-5 h-5` |
| Feature/principle icons | 20px (inside 40px container) | `w-5 h-5` |
| Navigation icons | 18px | `w-[18px] h-[18px]` |

### 8.2 Semantic Icon Mapping

| Concept | Icon | Usage |
| :--- | :--- | :--- |
| Safety / Policy | `Shield` | Safety Kernel, policy evaluation |
| Activity / Metrics | `Activity` | Job activity, system health |
| Workers / Users | `Users` | Worker pool, team management |
| Workflows | `GitBranch` | Workflow engine, pipelines |
| Approved | `CheckCircle2` | Approval badges, success states |
| Pending | `Clock` | Pending badges, waiting states |
| Denied | `XCircle` | Denial badges, error states |
| Warning | `AlertTriangle` | Warning badges, attention states |
| Search | `Search` | Search inputs, global search |
| Notifications | `Bell` | Notification center |
| Settings | `Settings` | Configuration, preferences |
| Refresh | `RefreshCw` | Data refresh, polling |
| Expand / Navigate | `ChevronRight` | Drill-down, navigation |
| External Link | `ArrowUpRight` | Links to external resources |
| Trend Up | `ArrowUpRight` | Positive metric change |
| Trend Down | `ArrowDownRight` | Negative metric change |

---

## 9. Patterns & Recipes

### 9.1 The Approval Queue

The approval queue is the most critical interaction pattern in Cordum. It displays pending human-in-the-loop decisions with enough context for the operator to act confidently.

```
┌──────────────────────────────────────────────────────────────┐
│ ▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬▬ │ ← amber accent
│                                                              │
│  appr-001   HIGH   12m ago                    [Deny] [Approve]│
│  DecisionBot-A — service.restart                             │
│  Production service restart requested after error threshold  │
│  exceeded                                                    │
│                                                              │
└──────────────────────────────────────────────────────────────┘
```

**Elements:**
- **ID:** `font-mono`, `text-sm`, `foreground`
- **Priority badge:** `HIGH` in `red-500/15` bg, `MEDIUM` in `amber-500/15` bg, `LOW` in `blue-500/15` bg
- **Timestamp:** `text-xs`, `muted-foreground`
- **Agent + Action:** `font-mono`, `font-semibold`, `foreground`
- **Description:** `text-sm`, `muted-foreground`
- **Actions:** `Deny` (destructive button) + `Approve` (primary button)

### 9.2 The KPI Row

A row of 3–4 instrument cards at the top of the dashboard, providing at-a-glance system health.

```
┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐
│ TOTAL JOBS  │  │ APPROVAL    │  │ PENDING     │  │ ACTIVE      │
│ 12,847 ▲4.5%│  │ RATE 98.2%  │  │ 3 awaiting  │  │ WORKERS 5/6 │
│ Last 30 days│  │ ████████░░  │  │ Human review│  │ ●●●●●○      │
└─────────────┘  └─────────────┘  └─────────────┘  └─────────────┘
```

Each card follows the instrument card pattern (5.2) with the default teal accent. The "Pending" card may use `status-warning` if the count exceeds a threshold.

### 9.3 The Worker Pool

A grid of worker status indicators showing online/offline state with capacity metrics.

```
Workers Online: 5 / 6

● worker-alpha    ● worker-beta     ● worker-gamma
  CPU: 45%          CPU: 72%          CPU: 38%
  Jobs: 1,240       Jobs: 2,100       Jobs: 980

● worker-delta    ● worker-epsilon  ○ worker-zeta
  CPU: 61%          CPU: 55%          OFFLINE
  Jobs: 1,580       Jobs: 1,340       Reconnecting...
```

- **Online dot:** `cordum`, pulsing
- **Offline dot:** `muted-foreground`, no pulse
- **Worker name:** `font-mono`, `text-sm`
- **Metrics:** `font-mono`, `text-xs`, `muted-foreground`

### 9.4 Code Blocks (Policy YAML)

Policy configuration is displayed in styled code blocks with syntax-appropriate coloring.

```
┌──────────────────────────────────────────────┐
│  ● ● ●   policy.yaml                        │  ← terminal dots (red/amber/green)
├──────────────────────────────────────────────┤
│                                              │
│  name: production-safety                     │
│  version: "1.0"                              │
│  rules:                                      │
│    - action: "service.restart"               │
│      decision: REQUIRE_APPROVAL              │  ← amber text
│      approvers: ["ops-lead", "sre-oncall"]   │
│      timeout: 300s                           │
│                                              │
│    - action: "*.delete"                      │
│      decision: DENY                          │  ← red text
│      reason: "Destructive ops blocked"       │
│                                              │
└──────────────────────────────────────────────┘
```

- **Container:** `surface-0` background, `border`, `radius-lg`
- **Terminal dots:** Three 8px circles — `#EF4444`, `#F59E0B`, `#10B981`
- **Filename tab:** `text-xs`, `font-mono`, `muted-foreground`, `surface-1` background
- **Code:** `text-sm`, `font-mono`, `foreground`
- **Decision keywords:** Colored with semantic status colors

---

## 10. Responsive Behavior

The dashboard is designed desktop-first (operators typically use large screens) but degrades gracefully to tablet and mobile for monitoring on the go.

| Breakpoint | Width | Layout Changes |
| :--- | :--- | :--- |
| Desktop | ≥ 1024px | Full sidebar (240px) + content area. 4-column KPI grid. Side-by-side charts. |
| Tablet | 640px – 1023px | Collapsed sidebar (64px, icons only). 2-column KPI grid. Stacked charts. |
| Mobile | < 640px | Hidden sidebar (hamburger menu). 1-column KPI grid. Full-width charts. Simplified tables. |

**Container widths:**

| Breakpoint | Padding | Max Width |
| :--- | :--- | :--- |
| Mobile | 16px | 100% |
| Tablet | 24px | 100% |
| Desktop | 32px | 1400px |

---

## 11. Accessibility

The design system maintains WCAG 2.1 AA compliance as a baseline.

| Requirement | Implementation |
| :--- | :--- |
| **Color contrast** | All text meets 4.5:1 against its background. Large text (≥18px bold) meets 3:1. |
| **Focus visibility** | All interactive elements have visible focus rings (double-ring pattern in `cordum`). |
| **Keyboard navigation** | Full keyboard support via Radix UI primitives. Tab order follows visual order. |
| **Screen readers** | Semantic HTML (`nav`, `main`, `section`, `table`). ARIA labels on icon-only buttons. |
| **Motion sensitivity** | Respect `prefers-reduced-motion`. Disable entrance animations and pulse effects. |
| **Color independence** | Status is never communicated by color alone — always paired with an icon and/or text label. |

---

## 12. File Structure

The design system is implemented as a React application with the following structure:

```
client/src/
├── index.css                  ← All design tokens (CSS variables, custom properties)
├── App.tsx                    ← Routes, ThemeProvider (dark default)
├── components/
│   ├── Layout.tsx             ← Sidebar + content area shell
│   └── ui/                    ← shadcn/ui primitives (button, card, input, etc.)
├── pages/
│   ├── Home.tsx               ← Design system overview + principles
│   ├── Colors.tsx             ← Full color palette documentation
│   ├── Typography.tsx         ← Type scale + font family showcase
│   ├── Components.tsx         ← Interactive component examples
│   └── DashboardExample.tsx   ← Complete dashboard page demo
└── contexts/
    └── ThemeContext.tsx        ← Theme management (dark default)
```

---

## 13. Design Tokens — Quick Reference

All tokens in one place for fast copy-paste into new components.

```css
/* === COLORS === */
--color-cordum:       oklch(0.82 0.18 165);    /* Primary accent */
--color-cordum-dim:   oklch(0.65 0.14 165);    /* Hover/pressed */
--color-cordum-bright:oklch(0.90 0.20 165);    /* Highlights */

--background:         oklch(0.13 0.01 260);    /* Page background */
--color-surface-0:    oklch(0.15 0.01 260);    /* Sidebar */
--color-surface-1:    oklch(0.18 0.01 260);    /* Cards */
--color-surface-2:    oklch(0.21 0.01 260);    /* Hover */
--color-surface-3:    oklch(0.25 0.01 260);    /* Active/dropdown */

--foreground:         oklch(0.90 0.005 260);   /* Primary text */
--secondary-foreground: oklch(0.80 0.005 260); /* Secondary text */
--muted-foreground:   oklch(0.60 0.01 260);    /* Tertiary text */
--border:             oklch(0.28 0.01 260);    /* Borders */

--color-success:      oklch(0.75 0.17 155);    /* Approved */
--color-warning:      oklch(0.80 0.16 75);     /* Pending */
--color-danger:       oklch(0.65 0.22 25);     /* Denied */
--color-info:         oklch(0.70 0.15 250);    /* Informational */

/* === TYPOGRAPHY === */
--font-display: "Plus Jakarta Sans", system-ui, sans-serif;
--font-sans:    "Inter", -apple-system, BlinkMacSystemFont, sans-serif;
--font-mono:    "JetBrains Mono", ui-monospace, SFMono-Regular, monospace;

/* === RADIUS === */
--radius:    0.5rem;    /* 8px — base */
--radius-sm: 0.25rem;   /* 4px */
--radius-md: 0.375rem;  /* 6px */
--radius-lg: 0.5rem;    /* 8px */
--radius-xl: 0.75rem;   /* 12px */

/* === MOTION === */
/* transition: all 250ms ease-out; (micro, Soft UI; --duration-soft) */
/* transition: all 300ms ease-out; (page) */
/* Framer Motion: { duration: 0.35 } (entrance) */
/* Stagger: 60ms between children */
```

---

## 14. Do's and Don'ts

### Do

- **Do** use the Cordum teal only for healthy states, primary actions, and active indicators.
- **Do** use `font-mono` for all numeric values, job IDs, timestamps, and code.
- **Do** use instrument cards with the 2px top accent line for all metric containers.
- **Do** pair every status color with an icon and a text label (never color alone).
- **Do** maintain the surface layer hierarchy — never place a `surface-0` element on top of a `surface-2` element.
- **Do** use `250ms ease-out` for all micro-interactions (consume the `--duration-soft` token).
- **Do** use uppercase `tracking-widest` labels for section categories.

### Don't

- **Don't** use teal for decoration, backgrounds, or non-semantic purposes.
- **Don't** use more than 3 font families.
- **Don't** use bouncy or spring-based animations.
- **Don't** use heavy box shadows — rely on surface luminance for depth.
- **Don't** use colored backgrounds for cards (cards are always `surface-1`).
- **Don't** use rounded corners larger than `12px` on dashboard components.
- **Don't** use inline styles — all styling flows through design tokens.

---

*This document is the single source of truth for the Cordum dashboard visual language. When in doubt, ask: "Does this choice reinforce or dilute the Control Surface philosophy?"*

*If it doesn't serve the operator, it doesn't belong on the screen.*
