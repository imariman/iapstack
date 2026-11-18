# IAPStack Design System

The visual system for the self-hosted IAPStack control plane now lives in [`DESIGN.md`](../../DESIGN.md) at the repository root. It replaced the earlier navy and monospace look in the November 2026 dashboard redesign. Colour, type, spacing, component and motion tokens are defined there and mirror `dashboard/app.css`.

## Product direction (unchanged)

- Product type: developer operations tool and real-time purchase monitor.
- Audience: engineers and operators configuring store authority, inspecting purchase verification, and recovering delivery failures.
- The dashboard is an operations console. It is not a marketing page, a paywall editor, or an experiment platform.
- Signature element: the Store → Verify → Access → Deliver purchase route. It communicates readiness and failure with text and shape, never colour alone.
- Numbers stay honest: sandbox value is never shown as revenue, and revenue appears only from imported official store reports.
