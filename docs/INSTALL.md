# Installing tasks-remote

`tasks-remote` is a single self-contained Go binary. It stores tasks in a local
SQLite database with application-level encryption and syncs encrypted change
artifacts through the Google Drive app-data folder.

## Requirements

- Go 1.26 or newer (to build from source).
- An OS keychain for cached unlock material:
  - macOS: Keychain (built in).
  - Windows: Credential Manager (built in).
  - Linux: a Secret Service provider such as GNOME Keyring or KWallet. On
    headless Linux, set `TASKS_REMOTE_SECRET` instead (see Unlocking below).
- For reminder notifications (optional):
  - Linux: `notify-send` (from `libnotify`).
  - macOS: `osascript` (built in).
  - Windows: PowerShell (built in).
- For Google Drive sync: an OAuth desktop client credentials JSON file (see
  USAGE.md, "Google Drive sync").

## Build and install

From the repository root:

```sh
go install ./cmd/tasks-remote
```

This places the `tasks-remote` binary in `$(go env GOBIN)` (or
`$(go env GOPATH)/bin`). Ensure that directory is on your `PATH`.

To build a local binary without installing:

```sh
go build -o tasks-remote ./cmd/tasks-remote
```

## First run

```sh
tasks-remote init        # creates and encrypts the local database
tasks-remote add "Try tasks-remote"
tasks-remote list
```

`init` prompts for a Recovery Secret and derives the database encryption key
from it with Argon2id. **There is no password reset.** If you lose the Recovery
Secret, encrypted local and cloud data cannot be recovered. Store it the way you
would store a password-manager master password.

## Data location

By default the database lives at:

- `$XDG_DATA_HOME/tasks-remote/tasks.db` if `XDG_DATA_HOME` is set, otherwise
- `~/.local/share/tasks-remote/tasks.db`.

Override per command with `-db <path>`:

```sh
tasks-remote -db /secure/vault/tasks.db list
```

## Unlocking

After `init` or `unlock`, the Recovery Secret is cached in the OS keychain,
scoped to the database path, so later commands run without prompting. Use
`tasks-remote lock` to clear the cached entry.

For automation or headless environments, set the secret in the environment
instead of using the keychain:

```sh
export TASKS_REMOTE_SECRET='your recovery secret'
tasks-remote list
```

When `TASKS_REMOTE_SECRET` is set it takes precedence over the keychain.

## Updating

Re-run `go install ./cmd/tasks-remote` against an updated checkout. The local
database schema migrates forward automatically on open; the encrypted sync
format is versioned independently (see ADR 0002).
