# Dashboard Page Rules

These rules override the IAPStack master design system for the operations dashboard.

## Information architecture

1. Persistent project navigation and control-plane identity.
2. Project context, freshness, refresh, and sign-out actions.
3. Store → Verify → Access → Deliver operational pipeline.
4. Four compact metrics: applications, customers, products, and pending jobs.
5. Application readiness as the primary work surface.
6. Catalog and durable queue state as supporting operational surfaces.
7. Recent verification, customer, and delivery tables.

Do not use a marketing hero, trial CTA, decorative chart, or repeated summary content.

## Layout

- Maximum workspace width: 1600px.
- Desktop shell: 248px sidebar plus fluid workspace.
- Workspace: 12-column grid with 12–16px gaps.
- Application panel spans 8 columns; catalog/queue stack spans 4 columns.
- Activity tables occupy full width, then split 7/5 where content allows.
- At 1024px use one content column and move navigation above the workspace.
- At 768px metrics become 2×2; at 480px all major elements become one column.

## Component behavior

- Pipeline nodes show a textual value, connected state, and warning marker.
- Metric cards emphasize the value first, then a short operational interpretation.
- Application cards expose configuration state and one clear “Manage connections” action.
- Queue cards show Pending / Completed / Failed with tabular figures and non-color labels.
- Empty states explain the next action instead of only stating absence.
- Dialogs use a strong scrim, sticky context header where needed, and 44px close controls.

## Motion and feedback

- Use a single subtle content entrance after authentication; never hide usable data for animation.
- Button and navigation state changes use 180ms transitions.
- Loading actions disable the initiating button, expose `aria-busy`, and retain a nearby live message.
- Reduced motion removes entrance transforms, spinners, and smooth scrolling.
