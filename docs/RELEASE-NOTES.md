# tasks-remote v1 — Release Notes

`tasks-remote` is a local-first, single-person task manager. Tasks live in a
local SQLite database with application-level encryption, and sync as encrypted
change artifacts through the Google Drive app-data folder. This document states
what v1 protects, what it does not, and the known limitations to be aware of.

## What v1 delivers

- Offline task management: add, edit, complete, reopen, delete, tag, list,
  show, and search — all on an unlocked device.
- Encryption at rest: titles, bodies, tags, due dates, and reminder dates are
  stored as authenticated, encrypted payloads. They do not appear in the SQLite
  database, WAL/temp files, or sync artifacts.
- Append-only change log with replay: the local projection can be rebuilt from
  the change history; applying a change twice is harmless.
- Multi-device sync: each device writes its own encrypted change artifact, so
  concurrent pushes cannot overwrite one another. A new device restores with
  Google authorization plus the Recovery Secret.
- Conflict handling: independent field edits auto-merge; concurrent content
  edits and delete/edit collisions become user-resolvable Sync Conflicts that
  preserve both sides. `conflicts resolve` records a decision that converges to
  other devices.
- Reminders: `reminders` lists due and upcoming reminders, with opt-in
  best-effort desktop notifications.
- Guarded plaintext export with an explicit confirmation flag.

## Cryptography

- Key derivation: Argon2id from the Recovery Secret with a per-database random
  salt (time=3, memory=64 MiB, threads=1).
- Encryption: XChaCha20-Poly1305 (AEAD) with a unique random nonce per
  payload and authenticated associated data binding each ciphertext to its
  task, change, or artifact name.
- Randomness: the OS CSPRNG.
- Key/secret storage: the OS keychain holds cached unlock material and the
  Google OAuth token. Encryption keys are never derived from Google tokens.

## What v1 protects against

- **Google account compromise without the Recovery Secret** — cloud artifacts
  are encrypted; Google and anyone with Drive access sees only ciphertext.
- **Google Drive storage exposure** — task content and change history are
  encrypted and authenticated before upload.
- **OAuth refresh token theft** — a stolen token grants Drive app-data storage
  access only; it does not unlock task content.
- **Lost or stolen device while locked** — without the Recovery Secret (and
  with no cached keychain entry, e.g. after `lock`), local data is unreadable.
- **Malicious or corrupted sync artifact** — artifacts are authenticated;
  tampering or swapping a device's file fails to open, and sync stops rather
  than importing unverified data.
- **Interrupted sync** — local artifact writes are atomic (temp file +
  rename), so an interrupted push never leaves a half-written artifact;
  unpushed changes stay pending and the push is retryable.
- **Duplicate / replayed / reordered changes** — changes are idempotent by id,
  and duplicate `(device, sequence)` pairs are flagged as conflicts.
- **Plaintext leakage** — no plaintext FTS index; errors and logs do not
  contain Sensitive Task Data (covered by tests).

## What v1 does NOT protect against

- **Malware running as you on an unlocked device** — it can read decrypted
  content like any local program.
- **A compromised OS keychain** — cached unlock material and the OAuth token
  live there.
- **Screen capture or terminal scrollback** after content is displayed,
  including the title shown in a `-notify` desktop notification.
- **Intentional plaintext export** to an unsafe location (`export`).
- **Google denial, quota exhaustion, or API outage** beyond surfacing a clear,
  retryable error and continuing to work locally.
- **Regulated-compliance guarantees** (HIPAA, PCI, etc.) — out of scope.

## Recovery

The Recovery Secret is the only key to your data. There is no reset and no
backdoor. If it is lost, encrypted local and cloud data cannot be recovered.
Keep a durable copy the way you would a password-manager master password.

## Keychain platform matrix

Cached unlock uses `github.com/zalando/go-keyring`:

| Platform | Backend | Notes |
| --- | --- | --- |
| macOS | Keychain | Built in. |
| Windows | Credential Manager | Built in. |
| Linux | Secret Service (D-Bus) | Needs GNOME Keyring, KWallet, or similar. |
| Headless / no Secret Service | — | Use `TASKS_REMOTE_SECRET`; cached unlock is unavailable. |

## Known limitations

- **Conflict fork point**: conflict detection compares the changes at the point
  two devices diverged. If a device makes several edits to a task before its
  first sync with a divergent device, resolving in its favor applies the
  fork-point version rather than that device's latest edit. The common
  single-edit-each case is exact.
- **Tag/status concurrency**: concurrent tag-set or completion changes from the
  same base are auto-merged by most-recent-edit (wall clock) rather than raised
  as conflicts; only content edits and delete/edit collisions block for review.
- **Clock dependence**: replay orders changes by their recorded timestamps, so
  significant clock skew between your devices can affect which concurrent edit
  is shown before resolution. Conflicts still preserve both sides.
- **Reminders re-notify**: `reminders -notify` notifies for every currently-due
  reminder on each run; cadence is controlled by how you schedule the command.
- **No background sync daemon**: sync and reminders run on demand or on a
  schedule you set; there is no resident process.

## Not in v1 (deferred)

Team collaboration, shared lists, mobile/desktop GUI, a hosted backend,
persistent encrypted full-text search, and whole-database encryption
(e.g. SQLCipher) are deferred. See `ROADMAP.md`.

## Live Google verification

The Google OAuth desktop flow and the Drive app-data client are implemented and
exercised against a fake HTTP server in tests. End-to-end verification against a
real Google account and real desktop OAuth credentials should be performed as a
release acceptance step before publishing binaries.
