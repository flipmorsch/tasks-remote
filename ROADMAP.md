# Roadmap

## Phase 1: Local Secure CLI Core

Deliver a usable offline task manager with encrypted local task payloads.

Scope:

- Initialize Go module and CLI structure.
- Implement `tasks init`, `unlock`, `lock`, `add`, `list`, `show`, `edit`, `done`, `reopen`, `delete`, and `search`.
- Create SQLite migrations.
- Encrypt all Sensitive Task Data before SQLite writes.
- Store unlock material through an OS keychain abstraction.
- Keep search local to an Unlocked Device without persistent plaintext FTS.

Acceptance:

- A user can manage tasks offline.
- Locked commands cannot reveal Sensitive Task Data.
- Wrong Recovery Secret cannot decrypt data.
- Plaintext task titles and bodies do not appear in local database, WAL, temp files, logs, or test sync artifacts.

Current status:

- Basic offline task commands are implemented.
- Encrypted task tags are implemented.
- Encrypted due dates and reminder dates are implemented.
- Guarded plaintext JSON export is implemented.
- OS keychain-backed `unlock` and `lock` are implemented.
- `TASKS_REMOTE_SECRET` remains available for automation.
- Local notification delivery and conflict resolution are not implemented yet.

## Phase 2: Task Change Log and Replay

Make local data rebuildable from Task Changes before adding Google Drive.

Scope:

- Implement globally unique Task Change IDs.
- Record append-only local changes for all task mutations.
- Replay Task Changes into the local projection.
- Add idempotency and duplicate detection.
- Add tombstone behavior for deletion.
- Add conflict detection primitives.

Acceptance:

- Replaying the same change twice is harmless.
- A fresh local database can rebuild from the local change log.
- Same-field offline conflicts are preserved rather than overwritten.
- Delete/edit collisions create explicit conflicts.

## Phase 3: Google Auth and Protected Sync Storage

Add Google Drive sync without trusting Google Drive with plaintext task content.

Scope:

- Implement `tasks login google` and `logout google`.
- Use system browser OAuth for installed applications.
- Store OAuth refresh tokens in the OS keychain.
- Use the Drive app data folder with the narrow app data scope.
- Upload and download encrypted sync artifacts through a Drive client interface.
- Build a fake Drive client for deterministic tests.

Acceptance:

- Google login alone does not unlock task content.
- Cloud artifacts do not contain plaintext Sensitive Task Data.
- Tampered sync artifacts fail authentication.
- Interrupted uploads are retryable.
- `tasks sync status` works while locked without revealing task content.

Current status:

- Local fake-Drive sync artifacts are implemented through `sync push -dir`, `sync pull -dir`, and `sync restore -dir`.
- Protected change artifacts are encrypted and authenticated.
- Sync uses per-device artifacts (`devices/<device-id>/changes-v1.json.enc`) so concurrent multi-device pushes cannot overwrite each other.
- Google OAuth and the Drive app-data client are implemented behind `login google`, `logout google`, and `sync * -google -credentials <file>`.
- Live Google verification still requires real OAuth desktop credentials and a Google account.

## Phase 4: Multi-Device Restore and Conflict UX

Make the product useful across machines.

Scope:

- Restore a new device from encrypted Drive artifacts.
- Require Google authorization plus Recovery Secret for restore.
- Merge independent field changes.
- Expose `tasks conflicts` and `tasks conflicts resolve`.
- Preserve both sides of conflicting body edits.
- Add visible Sync Health states.

Acceptance:

- A second machine can restore the Task Collection.
- Different offline field edits merge.
- Same-note offline body edits create a user-resolvable Sync Conflict.
- Sync failures are visible and retryable.
- Recovery target is measured against the accepted Recovery Window.

Current status:

- Restore from encrypted per-device artifacts works through `sync restore`.
- Independent field edits (e.g. completion vs. content) auto-merge.
- Concurrent content edits and delete/edit collisions become user-resolvable Sync Conflicts that preserve both sides.
- `tasks conflicts resolve <conflict-id> --use local|remote` records a resolution that converges across devices.
- Visible Sync Health states beyond `sync status` counts are not built yet.

## Phase 5: Hardening and Release Readiness

Prepare for real personal use.

Scope:

- Add end-to-end restore tests.
- Add log redaction tests.
- Add crash/interruption tests around local writes and sync.
- Add platform checks for keychain behavior.
- Add backup/export warning UX.
- Add install and update documentation.

Acceptance:

- Test suite covers the threat model in `SPEC.md`.
- No silent data loss cases are known.
- Security-sensitive failure modes produce clear user-facing errors.
- Release notes document what v1 protects and what it does not protect.

## Deferred

- Team collaboration.
- Shared lists.
- Mobile apps.
- Hosted backend.
- Persistent encrypted FTS through whole-database SQLite encryption.
- SQLCipher or other whole-database encryption until a dedicated spike proves packaging and behavior.
