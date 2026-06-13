# tasks-remote

A local-first, end-to-end-encrypted personal task manager CLI with optional
Google Drive sync across your own devices. Tasks live in a local SQLite database
with application-level encryption; sync exchanges encrypted change artifacts, so
Google never sees your task content.

```sh
go install ./cmd/tasks-remote
tasks-remote init
tasks-remote add -due 2026-06-20 "Pay invoice"
tasks-remote list
```

## Highlights

- **Encrypted at rest** — titles, bodies, tags, due/reminder dates are sealed
  with XChaCha20-Poly1305; the key is derived from your Recovery Secret with
  Argon2id. Nothing sensitive lands in SQLite, WAL, temp files, or logs.
- **Multi-device sync** — each device writes its own encrypted change artifact,
  so concurrent pushes never overwrite each other. Restore a new device with
  Google auth + the Recovery Secret.
- **Real conflict handling** — independent field edits auto-merge; concurrent
  content edits and delete/edit collisions become user-resolvable conflicts that
  preserve both sides and converge once resolved.
- **Reminders** — `tasks-remote reminders` lists what's due, with opt-in desktop
  notifications.

## Documentation

- [docs/INSTALL.md](docs/INSTALL.md) — install, requirements, data location.
- [docs/USAGE.md](docs/USAGE.md) — every command, sync, conflicts, reminders.
- [docs/RELEASE-NOTES.md](docs/RELEASE-NOTES.md) — threat model (what v1 protects
  and what it does not), cryptography, known limitations.
- [SPEC.md](SPEC.md), [ROADMAP.md](ROADMAP.md), [docs/adr](docs/adr) — design.

## Security in one line

Your Recovery Secret is the only key to your data. There is no reset: lose it
and encrypted data cannot be recovered. See the threat model in the release
notes for what is and isn't protected.
