# IAPStack Design System

This file is the source of truth for the self-hosted IAPStack control plane. Page-specific rules in `pages/` override this document.

## Product direction

- Product type: developer operations tool and real-time purchase monitor.
- Audience: engineers and operators configuring store authority, inspecting purchase verification, and recovering delivery failures.
- Visual direction: dark, data-dense, technical, and calm. Combine a real-time terminal monitor with Swiss-style hierarchy.
- Signature element: the Store → Verify → Access → Deliver pipeline. It must communicate readiness and failure with text and shape, not color alone.
- Avoid: marketing hero layouts, decorative glass blur, sci-fi HUD ornament, neon glow, oversized typography, and chart decoration without operational value.

## Foundations

### Color tokens

| Role | Value | Purpose |
|---|---:|---|
| Background | `#080D17` | Application canvas |
| Sidebar | `#0B1220` | Primary navigation |
| Surface | `#111A2B` | Panels and dialogs |
| Surface raised | `#162238` | Interactive and emphasized surfaces |
| Border | `#26344D` | Component boundaries |
| Border strong | `#3B4D6C` | Hover and selected boundaries |
| Foreground | `#F4F7FB` | Primary text |
| Muted foreground | `#A8B4C8` | Secondary text; maintain 4.5:1 contrast |
| Primary | `#6EA8FE` | Focus, active navigation, links |
| Accent | `#35D07F` | Primary action and healthy state |
| Warning | `#F6B94A` | Degraded or pending state |
| Destructive | `#FF6B7D` | Terminal failure and destructive state |
| Focus ring | `#8AB8FF` | Keyboard focus indicator |

All component colors must reference semantic CSS variables. State meaning must include a label or symbol in addition to color.

### Typography

- Body: `IBM Plex Sans`, `Inter`, `Segoe UI`, system sans-serif.
- Technical headings and data: `JetBrains Mono`, `SFMono-Regular`, `Consolas`, monospace.
- Scale: 12 / 14 / 16 / 20 / 24 / 32px. Body text is at least 16px on small screens.
- Use tabular numbers for metrics and table data.
- Use sentence case for actions and headings; uppercase is limited to compact operational labels.

### Spacing and shape

- 4px base rhythm with 8 / 12 / 16 / 24 / 32px layout tiers.
- Dense dashboard cards use 12–16px internal padding; major sections use 20–24px.
- Radius: 8px controls, 12px panels, 16px dialogs. Avoid pill shapes except status chips.
- Shadows are reserved for dialogs and floating mobile navigation; use borders for panel hierarchy.

## Interaction rules

- Web controls are at least 44×44px and separated by at least 8px.
- Hover, pressed, focus, disabled, loading, success, and error states are all distinct.
- Use 120ms press feedback and 180–220ms state transitions without changing layout bounds.
- Animate only opacity and transform. Respect `prefers-reduced-motion`.
- Keep forms progressively disclosed. Complex provider and webhook configuration belongs in focused dialogs.
- Keep visible labels, helper text for irreversible secret behavior, inline status feedback, and semantic native controls.
- Use inline SVG icons with a consistent 1.75px outline style. Decorative icons are `aria-hidden`.

## Accessibility and responsive rules

- Normal text contrast is at least 4.5:1; focus and component boundaries are at least 3:1.
- Preserve the skip link, sequential heading hierarchy, keyboard navigation, native dialog escape behavior, and visible focus.
- Desktop uses a persistent sidebar. Below 1024px it becomes a top navigation region without hiding core actions.
- Tables use a bounded horizontal wrapper on small screens; the page itself must never scroll horizontally.
- Validate at 375, 768, 1024, and 1440px, including landscape and reduced motion.
