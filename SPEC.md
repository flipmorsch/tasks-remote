# Tasks Remote v1 Specification

## Goal

Build a Go CLI Personal Task Manager with a local-first SQLite database and encrypted Google Drive sync. The app must protect Sensitive Task Data from Google Drive, accidental cloud exposure, and normal multi-machine conflict hazards.

## Non-Goals

- Team workspaces, shared task lists, delegated tasks, or organization ownership.
- Hosted backend infrastructure.
- Google Drive plaintext inspection or Google Drive search over task content.
- Regulated compliance claims such as HIPAA or PCI.
- Mobile, desktop GUI, or web UI in v1.

## Success Targets

- Local command latency: p50 under 50ms, p95 under 200ms, p99 under 500ms for normal personal databases.
- Sync latency: p95 under 10s when online and Google Drive is healthy.
- Local operation SLO: 99.5% successful local operations.
- Recovery Window: RPO at most 5 minutes with network access; RTO at most 15 minutes for normal personal data sizes.
- Sync failures must be visible and retryable.
- Silent data loss is a release blocker.

## Product Model

The user owns one Task Collection. A task can include:

- Title.
- Body or note text.
- Completion state.
- Priority.
- Due date.
- Reminder date.
- Tags.
- List or section.
- Creation, update, completion, and deletion timestamps.

Task Search is local-only and available only on an Unlocked Device.

## CLI Commands

Use `tasks` as the placeholder binary name until the project chooses a final name.

Required v1 commands:

```text
tasks init
tasks unlock
tasks lock
tasks login google
tasks logout google
tasks add <title>
tasks edit <task-id>
tasks done <task-id>
tasks reopen <task-id>
tasks delete <task-id>
tasks list
tasks show <task-id>
tasks search <query>
tasks tag add <task-id> <tag>
tasks tag remove <task-id> <tag>
tasks sync
tasks sync status
tasks conflicts
tasks conflicts resolve <conflict-id>
tasks export
```

Command behavior:

- Local task mutations must write durable local state before attempting sync.
- Commands that read or mutate Sensitive Task Data require an Unlocked Device.
- Google login alone must not unlock task content.
- `tasks sync status` must work when locked, but it must not print Sensitive Task Data.
- `tasks export` must default to an explicit plaintext warning and require confirmation.

Current implementation note:

- `TASKS_REMOTE_SECRET` is supported for non-interactive automation.
- Interactive unlock caches recovery material in the OS keychain.
- `tasks lock` clears the cached OS keychain entry for the selected database.
- Keychain entries are scoped by local database path.
- Task tags are treated as Sensitive Task Data and stored inside encrypted task and change payloads.
- Due dates and reminder dates are treated as Sensitive Task Data and stored inside encrypted task and change payloads.
- `tasks export -out <path> --confirm-plaintext` writes active tasks to a new plaintext JSON file and refuses to overwrite an existing path.

## Storage Architecture

SQLite is the local projection of the Task Collection. Google Drive sync stores encrypted Task Changes and sync metadata, not a SQLite database file.

Local persistent storage must not leak Sensitive Task Data. This includes task bodies, titles, tags, FTS indexes, WAL files, temporary files, logs, crash reports, and exported data.

V1 local-at-rest design:

- Store Sensitive Task Data as application-encrypted payload columns before writing to SQLite.
- Store non-sensitive operational metadata separately only when it cannot reveal task content.
- Do not persist plaintext FTS indexes.
- Rebuild any in-memory search index after unlock.
- Treat WAL files, temporary files, logs, and crash output as part of the plaintext-leakage test surface.

A future whole-database encrypted SQLite implementation, such as SQLCipher, requires a separate spike and ADR before it replaces v1 payload encryption.

## Local Tables

Suggested logical schema:

```text
devices
  device_id
  display_name
  created_at
  last_seen_at

tasks
  task_id
  encrypted_payload
  payload_nonce
  payload_key_id
  status_metadata
  created_at
  updated_at
  deleted_at
  last_change_id

tags
  tag_id
  encrypted_name
  name_nonce
  name_key_id

task_tags
  task_id
  tag_id

task_changes
  change_id
  device_id
  sequence
  task_id
  change_type
  field_mask
  encrypted_payload
  payload_nonce
  payload_key_id
  created_at
  parent_change_ids
  sync_state

sync_state
  key
  value

sync_conflicts
  conflict_id
  task_id
  field
  local_change_id
  remote_change_id
  encrypted_base_value
  encrypted_local_value
  encrypted_remote_value
  created_at
  resolved_at
```

Implementation notes:

- `change_id` must be globally unique and idempotent.
- `(device_id, sequence)` must be unique.
- Applying the same Task Change twice must be harmless.
- Deletion should be a tombstone in v1, not physical removal from change history.
- Metadata fields must be reviewed before storage; if a field can reveal user-entered Sensitive Task Data, it belongs inside an encrypted payload.
- A later compaction feature may snapshot old changes, but compaction must preserve restore and conflict semantics.

## Sync Format

Drive should contain a small set of application files in the app-specific Drive area:

```text
manifest.json
changes-v1.json.enc
devices/<device-id>/changes-<range>.jsonl.enc
snapshots/<snapshot-id>.bin.enc
```

V1 can start without snapshots if replay stays fast enough. Add snapshots only when restore time approaches the RTO target.

Current implementation note:

- `manifest.json` is plaintext but must contain only non-sensitive collection metadata such as format version and KDF salt.
- Task content and Task Changes are stored in encrypted authenticated artifacts.
- Each device writes only its own changes to `devices/<device-id>/changes-v1.json.enc`, so concurrent pushes from different devices cannot overwrite each other. Pull merges every device artifact.
- Each device artifact is bound to its own path through authenticated associated data, so a swapped or tampered file fails to open instead of corrupting the merge.
- `sync push -dir`, `sync pull -dir`, and `sync restore -dir` use a local directory as a fake Drive app-data folder for deterministic tests.
- `sync push -google -credentials <file>`, `sync pull -google -credentials <file>`, and `sync restore -google -credentials <file>` use Google Drive app data storage after `login google`.

Each encrypted change file should contain newline-delimited canonical JSON records before encryption:

```json
{
  "version": 1,
  "change_id": "uuid-or-ulid",
  "device_id": "device-id",
  "sequence": 42,
  "task_id": "task-id",
  "change_type": "task.updated",
  "field_mask": ["title", "due_at"],
  "payload": {
    "title": "Pay invoice",
    "due_at": "2026-06-20T12:00:00Z"
  },
  "created_at": "2026-06-13T12:00:00Z",
  "parent_change_ids": ["previous-change-id"]
}
```

Rules:

- Encrypt each sync artifact before upload.
- Authenticate metadata needed to prevent tampering, rollback confusion, and cross-device mixups.
- Include format versions in plaintext only when they do not reveal task content.
- Never rely on Google Drive filenames for security.
- Treat Drive file contents as attacker-controlled until authenticated and decrypted.

## Conflict Rules

Auto-merge:

- Different fields on the same task.
- Tag additions/removals that do not contradict each other.
- Completion state if one change clearly happens after the other in the causal chain.

Create Sync Conflict:

- Two offline edits change the same long text body.
- A delete collides with a meaningful edit.
- Causal history is missing or inconsistent.
- Authenticated sync metadata indicates duplicate sequence numbers for the same device.

Conflict resolution must preserve both versions until the user chooses the result.

Current implementation note:

- Each change records the per-task change it was authored against (`parent_change_id`). Two changes that share a parent are a fork: two devices edited the same version of a task offline.
- Concurrent content edits become a `concurrent_edit` Sync Conflict and delete/edit collisions become a `delete_edit` Sync Conflict. Both sides are preserved in the change log.
- Lower-stakes forks (completion status, tags) fall through to temporal last-writer-wins replay rather than blocking the user.
- `tasks conflicts` lists open conflicts with both decrypted sides; `tasks conflicts resolve <conflict-id> --use local|remote` records the choice as a new `task.resolved` change that wins replay and converges to other devices when it syncs in.
- Known v1 limitation: detection compares the changes at the fork point. If a device makes several edits to a task before its first sync with a divergent device, resolution applies the fork-point version of the chosen side rather than that side's latest edit.

## Crypto Requirements

The Recovery Secret protects a root key. The root key protects local database access and cloud sync encryption keys.

Recommended primitives:

- Password KDF: Argon2id with per-user random salt and memory-hard parameters calibrated at setup.
- AEAD: XChaCha20-Poly1305 or AES-256-GCM with unique nonces and authenticated associated data.
- Randomness: OS CSPRNG only.
- Key storage: OS keychain for cached unlock material and OAuth refresh tokens.

Rules:

- Do not invent custom cryptography.
- Do not derive encryption keys directly from Google OAuth tokens.
- Do not log secrets, plaintext tasks, decrypted payloads, OAuth tokens, or key material.
- Do not continue after authentication failure of encrypted sync data.
- Provide a `lock` command that clears cached unlock material where the OS allows it.
- Provide a documented recovery warning during `init`.

## Google Drive Integration

Use Google OAuth for installed applications. The app must use the system browser authorization flow, not an embedded browser.

Preferred scope:

```text
https://www.googleapis.com/auth/drive.appdata
```

Drive behavior:

- Store protected sync artifacts in the Google Drive app data folder.
- Do not request full Drive access.
- Do not create user-visible task files in My Drive for v1 sync.
- OAuth tokens grant cloud storage access only; they do not unlock task content.
- OAuth tokens are stored in the OS keychain.

References:

- Google Drive application data folder: https://developers.google.com/workspace/drive/api/guides/appdata
- Google Drive scopes: https://developers.google.com/workspace/drive/api/guides/api-specific-auth
- OAuth for desktop apps: https://developers.google.com/identity/protocols/oauth2/native-app

## Threat Model

Threats v1 must address:

- Google account compromise without the Recovery Secret.
- Google Drive storage exposure.
- OAuth refresh token theft.
- Lost or stolen laptop.
- Malicious or corrupted Drive sync artifact.
- Interrupted sync upload or download.
- Duplicate, replayed, reordered, or partially uploaded Task Changes.
- Offline edits on multiple devices.
- Plaintext leakage through logs, FTS indexes, temp files, exports, or crash output.

Threats v1 does not fully address:

- Malware running as the user on an Unlocked Device.
- Compromised OS keychain.
- Screen capture or terminal scrollback after plaintext display.
- User intentionally exporting plaintext data to an unsafe location.
- Google account denial, quota exhaustion, or API outage beyond visible degraded sync.

## Testing Requirements

Unit tests:

- Task Change creation and replay.
- Idempotent replay.
- Duplicate change rejection.
- Conflict detection for same-field edits.
- Delete/edit conflict preservation.
- Lock/unlock state transitions.
- Redaction of Sensitive Task Data in errors and logs.

Integration tests:

- SQLite migrations.
- Encrypted database open/close behavior.
- Search index availability after unlock.
- Sync upload/download against a fake Drive client.
- Interrupted upload recovery.
- Restore a new device from encrypted sync artifacts.
- OAuth token storage abstraction with fake keychain.

Security tests:

- No plaintext task titles or bodies in cloud artifacts.
- No plaintext task titles or bodies in local unencrypted files.
- Tampered sync artifact fails authentication.
- Wrong Recovery Secret cannot decrypt data.
- `sync status` does not print Sensitive Task Data while locked.

Manual acceptance:

- Create tasks offline, sync later, restore on a second machine.
- Edit different fields offline on two machines and verify auto-merge.
- Edit the same note body offline on two machines and verify Sync Conflict.
- Lose Google auth and verify local use continues.
- Lock the device and verify task reads fail until unlock.

## Open Decisions

- Final binary name.
- Whether v1 supports reminders as local notifications or only stored reminder dates.
- Exact supported keychain platform matrix.
- Whether a later whole-database encrypted SQLite spike should target SQLCipher or another maintained option.
