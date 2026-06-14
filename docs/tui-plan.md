# TUI Implementation Plan

## Goal

Add an Interactive Task Surface that opens when a person runs `tasks-remote` in
an interactive terminal, while preserving the existing command interface for
automation and direct CLI use.

## Entry Points

- `tasks-remote` opens the TUI only when stdin and stdout are interactive.
- `tasks-remote tui` opens the TUI explicitly.
- Non-interactive no-command usage prints usage or a clear non-interactive
  message instead of trying to render a terminal UI.
- Existing commands and flag behavior remain unchanged.

## States

The TUI must handle these startup states:

- Missing database: show Task Collection Setup with `Create New Task Collection`
  and `Restore Existing Task Collection`.
- Locked database: show unlock, restore, Sync Health, Google login/setup, and
  quit actions without revealing Sensitive Task Data.
- Unlocked database: start in the Working View.

Creating a new Task Collection may initialize the default database path only
after the user explicitly chooses creation and confirms the Recovery Secret.

## Working View

The unlocked default view shows overdue, due, upcoming, reminded, and open work.
It should show title, status, tags, due date, and reminder date. Full task notes
and task identifiers belong in detail/edit or technical views, not the compact
Working View.

Completed tasks are available through `All` or `Done` filters, not shown in the
default Working View. The first version does not include bulk actions.

## Task Actions

The first implementation must use real storage-backed operations against the
selected database. It is not a mock or demo UI.

Required task actions:

- List and filter tasks.
- Create tasks with title, body, due date, reminder date, and tags.
- Edit title, body, due date, reminder date, and tags.
- Complete and reopen tasks.
- Delete tasks after confirmation.
- Search titles and bodies on an Unlocked Device, with compact result lists.
- View and resolve Sync Conflicts.

Create and edit use Task Forms with explicit save/cancel. Quick actions such as
complete, reopen, and confirmed delete write immediately.

Plaintext export remains CLI-only for the first TUI version.

## Sync

Sync is manual-first. The primary action is `Sync Now`; advanced `Push` and
`Pull` actions may also be available.

Sync Health must distinguish at least:

- Not configured.
- Locked.
- Pending local changes.
- Caught up.
- Retrying or blocked.
- Waiting for conflict resolution.

`Sync Now` opens Local Sync Setup when sync is not configured. Local Sync Setup
is stored per database path and may target Google Drive or a local directory.
The local directory option is advanced/testing-oriented but should remain
available for parity with the CLI.

After successful sync, refresh task data and Sync Health. Failed sync, login,
and restore errors stay visible until dismissed.

Manual pull warns when pending local changes exist, but may proceed. Open Sync
Conflicts do not block ordinary task work.

Google login reuses the existing browser OAuth flow and shows a clear state
telling the user to return after browser authorization. OAuth tokens remain in
the OS keychain. The credentials JSON path may be remembered as Local Sync
Setup because it is not task content or the Recovery Secret.

## Restore

Restore supports Google Drive and local directory sources. Restore first
establishes access to the Protected Task Copy, then asks for the Recovery
Secret.

Restore requires Restore Confirmation when the target database already exists.
If the existing database has pending local changes, require stronger
confirmation before proceeding.

The UI must not describe restore as downloading or replacing `tasks.db`; it
restores a Task Collection from Protected Task Copies.

## Terminal Behavior

- Keyboard-first for v1.
- No mouse support in v1.
- Include an in-app help screen with keybindings.
- Adapt to narrow terminals with a compact usable layout.
- Remain readable without color and respect `NO_COLOR` where practical.
- Opening the TUI does not send desktop notifications. Notification delivery
  remains explicit CLI/scheduled behavior.
- Do not show `TASKS_REMOTE_SECRET` automation guidance inside the TUI.

## Implementation Shape

- Put TUI code in a new package such as `internal/tui`.
- Keep `cmd/tasks-remote/main.go` as a thin integration layer.
- Use Bubble Tea, Bubbles, and Lip Gloss unless implementation discovery finds
  a concrete blocker.
- Reuse storage and sync packages directly where practical. Avoid a broad
  service-layer refactor before the first working TUI.

## Tests

Prioritize tests for:

- No-command behavior in interactive versus non-interactive contexts.
- Startup states: missing database, locked database, unlocked database.
- Model/update transitions for task list, forms, help, error display, sync
  setup, and restore confirmation.
- Storage effects for create, edit, complete, reopen, delete, tags, search, and
  conflict resolution.
- Sync command dispatch and Sync Health state changes with fake/local clients.
- `NO_COLOR` and narrow-layout behavior at a lightweight level.

Avoid brittle full-screen rendering snapshots except where they catch a specific
layout regression.
