# Notification bell for Overview messages

## Intent

The always-on auth-failure panel clutters the graph and cannot be dismissed.
Replace it with a notification bell next to the Overview edit toggle: a red dot
means there are messages, clicking opens a list, and the list can be cleared.
Model messages generically so future sources can reuse the same bell.

## Goals

- Put a bell button immediately next to the `编辑` toggle in the Overview
  toolbar; it is visible in both monitor and edit modes.
- Show a red dot while at least one notification exists.
- Open a list on click: newest first, with title, detail and time.
- Clear the list from a button inside the panel. Clearing also empties the
  server-side in-memory auth-failure tracker so a reload cannot resurrect them.
- Keep the graph free of persistent overlays.
- Feed the bell through a generic `AppNotification` shape; auth failures are
  one adapter today.

## Non-goals

- Persisting notifications or read/unread state across restarts.
- Showing a history page, filtering, or per-notification dismissal.
- Notifying about anything except gateway-key auth failures in this version.
- Writing 401s to SQLite or changing request/usage statistics.

## Constraints

- Clearing is admin-only (`DELETE /api/auth-failures`) and affects the shared
  in-memory tracker; credentials are never stored or returned.
- Keep the existing bounded tracker (50 entries) and the additive
  `/api/events` payload.
- The Overview WebSocket must stay connected in edit mode so the bell keeps
  receiving while the monitor is paused.
- UI source strings are Chinese and translated through `en-US.ts`.

## Approach

1. Add `Tracker.Clear()` and an authenticated `DELETE /api/auth-failures` that
   empties the tracker and returns 204.
2. Add `web/src/notifications.ts` with `AppNotification`,
   `authFailureNotifications(snapshot)` and the reason labels. Future sources
   append to the same list.
3. Add `NotificationBell` (Badge dot + Popover) and a testable
   `NotificationList` (header, clear button, empty state, entries).
4. Render the bell beside the edit toggle in `OverviewToolbar`; remove the
   `AuthFailureAlert` Panel and the `authFailures` prop from `Topology`.
5. Keep `authFailures` in `App`, derive notifications, clear through the admin
   API, and enable the Overview WebSocket in both monitor and edit modes.

## Alternatives considered

- **Client-side clear only.** Rejected: the tracker re-sends its 50 entries on
  the next snapshot or reload, so the clear would appear to fail.
- **Keep the overlay and add a close button.** Rejected: still covers the
  graph, and each new source would need its own overlay.
- **Persist notifications in SQLite.** Rejected for this version: retention,
  migrations and an unauthenticated write path are not worth it for a live
  debugging aid.
