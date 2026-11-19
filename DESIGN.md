---
name: IAPStack Operations Dashboard
description: Dense light and dark control plane for a self-hosted in-app purchase verification server.
colors:
  background: "#09090b"
  sidebar: "#0c0c0e"
  surface: "#111113"
  surface-raised: "#17171a"
  surface-hover: "#1c1c20"
  surface-inset: "#0d0d0f"
  border: "rgba(255, 255, 255, 0.075)"
  border-strong: "rgba(255, 255, 255, 0.13)"
  highlight: "rgba(255, 255, 255, 0.045)"
  foreground: "#ededef"
  muted: "#a1a1aa"
  subtle: "#8b8b94"
  primary: "#7b7ff6"
  primary-strong: "#9a9dff"
  primary-soft: "rgba(123, 127, 246, 0.14)"
  focus: "#8b8ef8"
  accent: "#34d399"
  accent-soft: "rgba(52, 211, 153, 0.12)"
  warning: "#fbbf24"
  warning-soft: "rgba(251, 191, 36, 0.12)"
  danger: "#fb7185"
  danger-soft: "rgba(251, 113, 133, 0.12)"
  danger-action: "#e11d48"
  danger-action-hover: "#be123c"
  chart-allowed: "#6f73f2"
  chart-denied: "#f43f5e"
  chart-unresolved: "#f59e0b"
  scrim: "rgba(3, 3, 5, 0.72)"
colors-light:
  background: "#f4f4f5"
  sidebar: "#fafafa"
  surface: "#ffffff"
  surface-raised: "#f4f4f5"
  surface-hover: "#e9e9ec"
  surface-inset: "#fafafa"
  border: "rgba(9, 9, 11, 0.1)"
  border-strong: "rgba(9, 9, 11, 0.18)"
  highlight: "rgba(255, 255, 255, 0.55)"
  foreground: "#18181b"
  muted: "#52525b"
  subtle: "#666670"
  primary: "#5b5def"
  primary-strong: "#4447d6"
  primary-soft: "rgba(91, 93, 239, 0.12)"
  focus: "#4f46e5"
  accent: "#047857"
  accent-soft: "rgba(4, 120, 87, 0.1)"
  warning: "#b45309"
  warning-soft: "rgba(180, 83, 9, 0.1)"
  danger: "#e11d48"
  danger-soft: "rgba(225, 29, 72, 0.08)"
  danger-action: "#e11d48"
  danger-action-hover: "#be123c"
  chart-allowed: "#5b5def"
  chart-denied: "#f43f5e"
  chart-unresolved: "#d97706"
  scrim: "rgba(24, 24, 27, 0.44)"
typography:
  display:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"SF Pro Text\", \"Segoe UI Variable Text\", \"Segoe UI\", system-ui, sans-serif"
    fontSize: "34px"
    fontWeight: 600
    lineHeight: 1.1
    letterSpacing: "-0.035em"
    fontFeature: "\"tnum\""
  headline:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"SF Pro Text\", \"Segoe UI Variable Text\", \"Segoe UI\", system-ui, sans-serif"
    fontSize: "30px"
    fontWeight: 600
    lineHeight: 1.15
    letterSpacing: "-0.03em"
    fontFeature: "\"tnum\""
  page-title:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"SF Pro Text\", \"Segoe UI Variable Text\", \"Segoe UI\", system-ui, sans-serif"
    fontSize: "24px"
    fontWeight: 600
    lineHeight: 1.25
    letterSpacing: "-0.025em"
  title:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"SF Pro Text\", \"Segoe UI Variable Text\", \"Segoe UI\", system-ui, sans-serif"
    fontSize: "14px"
    fontWeight: 600
    lineHeight: 1.5
    letterSpacing: "-0.005em"
  body:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"SF Pro Text\", \"Segoe UI Variable Text\", \"Segoe UI\", system-ui, sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.5
    fontFeature: "\"cv11\", \"ss01\", \"ss03\""
  label:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"SF Pro Text\", \"Segoe UI Variable Text\", \"Segoe UI\", system-ui, sans-serif"
    fontSize: "13px"
    fontWeight: 500
    lineHeight: 1.5
  caption:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"SF Pro Text\", \"Segoe UI Variable Text\", \"Segoe UI\", system-ui, sans-serif"
    fontSize: "12px"
    fontWeight: 500
    lineHeight: 1.5
  data:
    fontFamily: "ui-monospace, \"SF Mono\", \"JetBrains Mono\", Menlo, Consolas, monospace"
    fontSize: "11.5px"
    fontWeight: 400
    lineHeight: 1.5
rounded:
  badge: "6px"
  control: "8px"
  panel: "12px"
  dialog: "16px"
  pill: "999px"
spacing:
  xxs: "4px"
  xs: "8px"
  sm: "12px"
  md: "16px"
  lg: "20px"
  xl: "24px"
  xxl: "32px"
components:
  button-primary:
    backgroundColor: "{colors.foreground}"
    textColor: "{colors.background}"
    typography: "{typography.label}"
    rounded: "{rounded.control}"
    padding: "0 12px"
    height: "34px"
  button-primary-hover:
    backgroundColor: "#ffffff"
  button-secondary:
    backgroundColor: "{colors.surface-raised}"
    textColor: "{colors.foreground}"
    typography: "{typography.label}"
    rounded: "{rounded.control}"
    padding: "0 12px"
    height: "34px"
  button-secondary-hover:
    backgroundColor: "{colors.surface-hover}"
  button-danger:
    backgroundColor: "{colors.danger-action}"
    textColor: "#ffffff"
    typography: "{typography.label}"
    rounded: "{rounded.control}"
    padding: "0 12px"
    height: "34px"
  button-danger-hover:
    backgroundColor: "{colors.danger-action-hover}"
  button-text:
    backgroundColor: "transparent"
    textColor: "{colors.muted}"
    rounded: "{rounded.control}"
    padding: "0 12px"
    height: "34px"
  button-icon:
    backgroundColor: "transparent"
    textColor: "{colors.muted}"
    rounded: "{rounded.control}"
    size: "34px"
  input:
    backgroundColor: "{colors.surface-inset}"
    textColor: "{colors.foreground}"
    typography: "{typography.body}"
    rounded: "{rounded.control}"
    padding: "8px 12px"
    height: "38px"
  panel:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.panel}"
    padding: "10px 20px"
  nav-project:
    backgroundColor: "transparent"
    textColor: "{colors.muted}"
    rounded: "{rounded.control}"
    padding: "8px 10px"
  nav-project-active:
    backgroundColor: "rgba(255, 255, 255, 0.06)"
    textColor: "{colors.foreground}"
  status-badge:
    backgroundColor: "rgba(255, 255, 255, 0.05)"
    textColor: "{colors.muted}"
    typography: "{typography.caption}"
    rounded: "{rounded.pill}"
    padding: "0 9px 0 8px"
  status-badge-healthy:
    backgroundColor: "{colors.accent-soft}"
    textColor: "{colors.accent}"
  status-badge-attention:
    backgroundColor: "{colors.warning-soft}"
    textColor: "{colors.warning}"
  status-badge-failure:
    backgroundColor: "{colors.danger-soft}"
    textColor: "{colors.danger}"
  meta-badge:
    backgroundColor: "rgba(255, 255, 255, 0.05)"
    textColor: "{colors.muted}"
    rounded: "{rounded.badge}"
    padding: "1px 7px"
  dialog:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.foreground}"
    rounded: "{rounded.dialog}"
    padding: "24px"
    width: "720px"
---

# Design System: IAPStack Operations Dashboard

## Overview

**Creative North Star: "The Quiet Control Plane"**

The dashboard is the category-standard developer console, built to Linear and Vercel craft rather than dressed as a terminal, in matching dark and light themes. In dark a near-black ground holds a few raised panels; in light the same panels sit white on a pale zinc ground. Both separate surfaces with hairline alpha borders lit by a 1px inner top highlight. One thing leads: the purchase route Store → Verify → Access → Deliver. Everything else is quiet, aligned and dense, so the operator sees route health first, then volume and revenue, then the evidence tables.

Colour is rationed. One indigo marks focus, selection and the active project. The primary action is the inverted button (light on dark, dark on light). Green, amber and rose appear only where they carry state, and each always has a text label next to it (and on the route, a change of shape). The type is the platform's own sans with tabular figures. Monospace is reserved for identifiers, keys and code.

Motion is limited to one load-in fade, a dialog rise and a slow pulse travelling along the route track. Reduced motion turns all of it off.

**Key Characteristics:**
- Tonal neutral ground in both schemes (near-black in dark, pale zinc in light) with panel layering, hairline alpha borders and a 1px top highlight
- One indigo accent for focus, selection and the active state, never as decoration
- Inverted primary button, so the main action reads without colour in either theme
- State colour only with a text label, and with a shape change where it matters
- Joined panels (metric row, application tiles, commissioning steps) divided by 1px lines instead of floating cards
- System sans with tabular figures; monospace only for identifiers

## Colors

A neutral zinc palette with one cool indigo and three semantic state hues. Every token carries a light and a dark value (`light-dark()`); the `colors` list above is the dark set and `colors-light` its pair. The theme follows the operating system by default (System), and the toggle on the sign-in screen and in the top bar cycles System → Light → Dark. The explicit choice is kept in a first-party cookie scoped to `/dashboard/`; nothing theme-related is written to browser storage.

### Primary
- **Signal Indigo** (`primary`): focus ring family, the active-project indicator bar, the route pulse, field focus borders, info messages and the soft selection wash. Its stronger tint (`primary-strong`) is used for links, the caret and the route pulse highlight. `primary-soft` is the tinted wash behind selected or info content and entitlement tags.
- **Focus Indigo** (`focus`): the 3px `:focus-visible` outline on every interactive element, offset 2px.

### Secondary (state)
- **Healthy Green** (`accent`, `accent-soft`): healthy route nodes, active/allowed/delivered badges, completed commissioning steps, success messages, imported-reports badge.
- **Attention Amber** (`warning`, `warning-soft`): a route stage that needs attention, pending/grace/on-hold/unresolved badges, unconfigured credentials, reports-required badge.
- **Failure Rose** (`danger`, `danger-soft`): denied/failed/refunded/revoked badges, form errors, the global error banner.
- **Destructive Crimson** (`danger-action`, `danger-action-hover`): the solid fill of destructive buttons only (revoke, delete). It is deeper than Failure Rose so white text passes contrast.

### Tertiary (chart)
- **Allowed Indigo** (`chart-allowed`), **Denied Rose** (`chart-denied`), **Unresolved Amber** (`chart-unresolved`): stacked daily verification bars. Always paired with a legend that names each series.

### Neutral
- **Void** (`background`): page ground.
- **Sidebar Black** (`sidebar`): the fixed project rail, one step above the ground.
- **Panel** (`surface`): every panel, dialog, and the route strip.
- **Raised** (`surface-raised`) / **Raised Hover** (`surface-hover`): secondary buttons and file-picker buttons.
- **Inset** (`surface-inset`): inputs, textareas, commissioning step cells.
- **Hairline** (`border`) / **Hairline Strong** (`border-strong`): panel edges and dividers / control borders and the route track.
- **Top Light** (`highlight`): the 1px inset top highlight on panels, secondary buttons and the active project.
- **Ink** (`foreground`), **Muted** (`muted`), **Subtle** (`subtle`): primary text, labels and secondary values, captions and timestamps.
- **Scrim** (`scrim`): dialog backdrop, with a 3px blur.

### Named Rules
**The One Indigo Rule.** Indigo means focus, selection or "this one is active". It is never a fill for buttons, headings or decoration. The page's only ambient use is a faint 7% radial glow at the top of the workspace.

**The Labelled State Rule.** Green, amber and rose never appear alone. Every state colour carries a text label, and the route's attention stage also changes its node from a circle to a diamond.

**The Honest Number Rule.** Sandbox value sits below a dashed divider in its own block, in neutral ink, and is never styled like revenue. With no store reports, the revenue figure is replaced by the state line. The subscriptions panel shows counts only, never a money figure, and a provider without signed trial evidence says so instead of showing zero.

## Typography

**Display Font:** System UI sans (-apple-system, SF Pro Text, Segoe UI Variable, system-ui)
**Body Font:** The same system sans
**Label/Mono Font:** ui-monospace (SF Mono, JetBrains Mono, Menlo, Consolas)

**Character:** One native sans at tight negative tracking for numbers and titles, and plain tracking for everything else. It reads as the operator's own OS, not a brand costume. Stylistic sets `cv11`, `ss01` and `ss03` are on globally.

### Hierarchy
- **Display** (600, 34px, 1.1, -0.035em, tabular): the revenue figure only.
- **Headline** (600, 30px, 1.15, -0.03em, tabular): the four overview metric values. The no-reports revenue state line takes 26px at the same weight.
- **Page Title** (600, 24px, 1.25, -0.025em): the project name in the top bar, single line with ellipsis.
- **Title** (600, 14px): panel headings (`h2`) and application names. Route stage values use 600 at 16px, tabular.
- **Body** (400, 14px, 1.5): default text. Table cells are 13px in Muted with tabular figures.
- **Label** (500, 13px): buttons, form labels, metric names, route stage names, secondary panel text.
- **Caption** (500 or 400, 12–12.5px, Subtle): table headers, timestamps, counts, sidebar headings, helper copy.
- **Data** (mono, 11.5px at 0.86em of context): identifiers, bundle IDs, keys, entitlement tags, and textareas that hold JSON or key material (12.5px).

### Named Rules
**The Tabular Rule.** Every number that can change (metrics, counts, timestamps, table cells, route values) uses `font-variant-numeric: tabular-nums`, so refreshes do not shift columns.

**The Mono Is Evidence Rule.** Monospace marks something the operator might copy: an ID, a key, a code value. Headings, labels and numbers stay in the sans.

**The Sentence Case Rule.** No uppercase labels and no wide tracking. Hierarchy comes from size, weight and the Ink/Muted/Subtle steps.

## Layout

The app is a two-column grid: a 248px sticky sidebar and a fluid workspace capped at 1440px, padded 22px top, 32px sides and 64px bottom. Workspace sections stack with a 16px gap in this order: top bar, purchase route strip, four-up metric row, revenue and activity panel, subscriptions panel, applications panel, then a split row of side panels (1.25fr / 1fr) and the evidence tables.

The spacing rhythm is 4/8 based. Panels pad 20px horizontally, panel headings are at least 52px tall, table cells pad 11px × 16px (20px at the outer edges), and dialogs pad 24px.

Responsive behaviour:
- **≤1240px:** the revenue column and the subscription churn column narrow to 320px and application tiles reflow to two rows.
- **≤1023px:** the sidebar becomes a top bar, the project list becomes a horizontal scroller, the active indicator moves to a bottom bar, the footer hides, and the revenue card and the subscription churn list stack under their charts.
- **≤767px / ≤479px:** grids collapse to one column, the subscription vitals become two columns and the trial cells stack, the primary top-bar action goes full width, and dialogs become bottom sheets with square bottom corners.
- **Coarse pointers:** buttons, inputs and project items grow to a 44px minimum; icon buttons become 44px square.

## Elevation & Depth

Depth is tonal first. Each step up (ground → sidebar → panel → raised) is a few points lighter, edged with a hairline alpha border and lit with a 1px inset top highlight. Shadows are soft, dark and low, and they ground panels rather than float them.

### Shadow Vocabulary
- **Panel** (`box-shadow: inset 0 1px 0 rgba(255,255,255,0.045), 0 1px 2px rgba(0,0,0,0.4), 0 12px 32px -18px rgba(0,0,0,0.7)`): every panel, the route strip, the metric row and the revenue/activity panel.
- **Dialog** (`box-shadow: inset 0 1px 0 rgba(255,255,255,0.045), 0 24px 64px -12px rgba(0,0,0,0.7), 0 2px 8px rgba(0,0,0,0.4)`): modal dialogs over the blurred scrim.
- **Primary button** (`box-shadow: inset 0 1px 0 rgba(255,255,255,0.5), 0 1px 2px rgba(0,0,0,0.4)`): the light action, plus the brand mark.
- **Field focus** (`box-shadow: 0 0 0 3px rgba(123,127,246,0.14)`): input focus wash under the indigo border.

### Named Rules
**The Top Light Rule.** Every raised surface carries a 1px inner top highlight. A panel without it looks pasted on.

**The Joined Panel Rule.** Related figures share one panel divided by 1px hairlines (metric row, application tiles, commissioning steps). They are never a grid of separate floating cards.

## Shapes

Corners are gently rounded and step up with container size: 6px badges, 8px controls and inputs, 12px panels, 16px dialogs, and full pills for status badges. Borders are always 1px hairlines, with one exception: the dashed divider that sets sandbox value apart from revenue. Status is carried by small 6px dots before labels, and the route uses 18px ringed nodes. A healthy node is a circle and an attention node is a 4px-radius diamond.

## Components

### Buttons
Compact and quiet. One light action per area, everything else recedes.
- **Shape:** gently rounded (8px), 34px tall, 12px side padding, 13px/500 label, 15px stroke icons at 1.8 width.
- **Primary:** Ink fill with near-black 600 text, a top highlight and a tiny drop shadow. Hover goes to pure white.
- **Secondary:** Raised fill, strong hairline border and top highlight. Hover moves to Raised Hover and a brighter border.
- **Danger:** Destructive Crimson fill with white 600 text. Hover deepens.
- **Text / Icon:** transparent with Muted text. Hover adds a 5% white wash and Ink text. Icon buttons are 34px square with a hairline border.
- **Press:** primary, secondary and danger buttons shift down 1px. Busy submit buttons show a 12px spinner, and disabled buttons drop to 50% opacity.

### Inputs / Fields
- **Style:** Inset fill, strong hairline border, 8px radius, 38px tall. Labels sit above in 13px/500 Muted.
- **Focus:** the border turns Signal Indigo with a 3px indigo wash, plus the global 3px focus outline when focused from the keyboard.
- **Read-only:** Muted text. Textareas use mono at 12.5px for JSON and key material. Selects use a drawn chevron.

### Navigation (project rail)
- **Visibility:** the rail collapses to a 60px icon rail from the panel icon beside the brand lockup (or Ctrl+B / Cmd+B): the brand mark, project initials (still clickable, titled with the full project ID and counts), the new-catalog button and the health dot remain. The toggle never moves or disappears, so the way back is always where the operator left it — the rail is collapsed, never fully hidden. The choice is kept in a cookie scoped to `/dashboard/`, like the theme, and never changes the selected project. On narrow layouts, where the rail is a top bar, collapsed means a single compact row.
- **Style:** project items have a 13.5px/500 name and a 12px Subtle count, 8px × 10px padding and an 8px radius.
- **Hover:** 4% white wash and Ink text.
- **Active:** 6% white wash, hairline border, top highlight, and a 2px Signal Indigo bar on the left edge (a bottom bar on mobile).
- **Mobile:** a horizontal scroll row under the brand lockup.

### Cards / Containers (panels)
- **Corner Style:** 12px.
- **Background:** Panel, with the Panel shadow and top light.
- **Border:** 1px Hairline. Internal rows are divided with Hairline rules, never gaps.
- **Internal Padding:** 20px sides. Headings are at least 52px tall and hold a 14px/600 title, a tabular count and compact 30px actions.

### Status Badges
- **Lifecycle pill:** 999px pill with a 6px dot, 12px/500 capitalised label, and a soft state fill with a 22% tinted border (green/amber/rose). Expired and canceled are neutral Subtle.
- **Access label:** in the transactions table, access is the same dot and colour without the pill fill, so it reads as quieter than lifecycle.
- **Meta badges** (provider, kind, revision): 6px radius, 5% white fill, 11.5px Muted.

### Tables
Sticky 12px Subtle headers on a near-ground fill. Rows are 13px Muted, tabular, with hairline dividers and a 2.5% white hover. The first column's `strong` is Ink, with a mono identifier under it. Long tables scroll inside the panel (max 560px) and fade at the bottom edge.

### Purchase Route Strip (signature)
Four equal stages on a 1px Hairline Strong track. A 22%-wide indigo gradient pulse travels the track every 4.8s. Each stage has an 18px ringed node (green circle when healthy, amber diamond when it needs attention), a 13px Muted stage name and a 16px/600 tabular value. When a stage needs attention, its value line turns amber. That line already states the counts behind the problem (for example "12 delivered · 1 failed"). The pulse is removed under reduced motion.

### Charts
Both bar charts accept pointer inspection: hovering (or touching) a day snaps a guide line to that day, restores its bars to full opacity while the rest dim, and shows a tooltip naming the date and the same labelled values as the legend. Hover targets span the full day slot so thin bars stay inspectable. The tooltip is a pointer enhancement only; the chart's `aria-label` summary and per-bar titles remain the accessible source.

### Revenue Card
Sits right of the activity chart (360px), separated by a hairline, with a faint top gradient. It has a 13px Muted heading, a state pill (reports required / imported) and a 34px revenue figure, or the 26px state line when no reports exist. Sandbox test value follows below a dashed divider in its own neutral block.

### Subscriptions Panel
Sits under Revenue & activity on the shared panel surface. The heading row holds the 14px title, a Subtle subtitle, the environment select and a segmented window control (7, 30, 90 days). A joined four-up vitals row (Active now, New, Renewals, Churned) uses 26px tabular values with a Subtle note under each; the churn note states the rate and its denominator. Below, a single-series daily bar chart of active subscriptions (Allowed Indigo, with its legend) sits left of a 360px churn-reason list. Plan changes and pauses sit under a dashed divider as "Not counted as churn". Free trials follow in a joined strip, one cell per provider divided by hairlines; a provider without signed trial evidence reads "Not available for" that provider in Muted, never zero. A product table and a one-paragraph definition close the panel. It shows no money figure.

### Segmented Control
Inset fill, strong hairline, 8px radius and 2px inner padding around 26px options (12.5px/500 Muted). The pressed option takes the 6% white wash, a hairline border and the top light, and carries `aria-pressed`. It never fills with indigo. On coarse pointers options grow to 44px.

### Dialogs
720px max, 16px radius, Panel fill with a strong hairline, Dialog shadow, and a 3px-blurred scrim. They enter by rising 8px and scaling from 0.985 over 260ms. On phones they become bottom sheets.

## Do's and Don'ts

### Do:
- **Do** separate every raised surface with a 1px white-alpha hairline and a 1px inset top highlight.
- **Do** keep Signal Indigo for focus, selection and the active project; make the primary action the light Ink button.
- **Do** pair every green, amber or rose with a text label, and change shape where state is critical (route diamond).
- **Do** use tabular figures for every live number and monospace only for copyable identifiers, keys and code.
- **Do** join related figures into one panel divided by hairlines.
- **Do** keep sandbox value visually separate from revenue (dashed divider, neutral ink).
- **Do** use the ease-out curve `cubic-bezier(0.16, 1, 0.3, 1)` at 120ms for state changes and 200ms for larger moves; disable all animation under reduced motion.
- **Do** grow touch targets to 44px on coarse pointers.

### Don't:
- **Don't** return to the navy-plus-monospace "terminal" look: no navy ground and no monospace headings or labels.
- **Don't** build a hero-metric grid of identical floating cards.
- **Don't** fill buttons, headings or large areas with indigo.
- **Don't** communicate state by colour alone.
- **Don't** use uppercase labels, wide letter-spacing or kicker lines above headings.
- **Don't** add motion beyond the load-in fade, dialog rise and route pulse.
- **Don't** load third-party fonts, scripts or styles; the dashboard runs under a same-origin CSP.
