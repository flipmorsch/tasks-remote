# Using tasks-remote

A local-first, encrypted personal task manager with optional encrypted Google
Drive sync across your own devices.

All commands take an optional global `-db <path>` flag before the command name:

```sh
tasks-remote -db /path/to/tasks.db list
```

> Flag ordering: command flags (e.g. `-body`, `-due`) must come **before**
> positional arguments, matching Go's flag parser. For example:
> `tasks-remote add -body "notes" "My title"`, not
> `tasks-remote add "My title" -body "notes"`. (`conflicts resolve` accepts the
> id before or after `--use`.)

## Lifecycle

| Command | Description |
| --- | --- |
| `init` | Create and encrypt a new local database; prompts for the Recovery Secret. |
| `unlock` | Re-enter the Recovery Secret and cache it in the OS keychain. |
| `lock` | Clear the cached unlock material for this database. |

Commands that read or change task content require an unlocked device. Only
`sync status` works while locked, and it never prints task content.

## Managing tasks

```sh
tasks-remote add "Pay invoice"
tasks-remote add -body "vendor: ACME" -due 2026-06-20 -remind 2026-06-19 "Pay invoice"
tasks-remote list
tasks-remote show <task-id>
tasks-remote edit -body "updated note" <task-id> "Pay invoice"
tasks-remote done <task-id>
tasks-remote reopen <task-id>
tasks-remote delete <task-id>
tasks-remote search invoice
```

Dates accept `YYYY-MM-DD` or full RFC3339 (`2026-06-20T12:00:00Z`).

Tags:

```sh
tasks-remote tag add <task-id> finance
tasks-remote tag remove <task-id> finance
```

Titles, bodies, tags, due dates, and reminder dates are all treated as
Sensitive Task Data and stored encrypted at rest. Search runs locally over
decrypted content on an unlocked device; nothing is indexed in plaintext.

## Reminders

```sh
tasks-remote reminders                 # due + upcoming within 24h
tasks-remote reminders -within 72h     # widen the upcoming window
tasks-remote reminders -notify         # also send desktop notifications
```

`reminders` lists open tasks whose reminder is due (`DUE`) or upcoming
(`SOON`), soonest first. `-notify` sends a best-effort desktop notification for
each due reminder (title only). There is no background daemon: schedule the
command yourself for delivery, e.g. a cron entry or systemd timer:

```cron
0 9 * * *  TASKS_REMOTE_SECRET='...' tasks-remote reminders -notify
```

Reminders re-notify on each run until the task is completed, so pick a cadence
that matches how often you want to be nudged.

## Plaintext export

```sh
tasks-remote export -out tasks.json --confirm-plaintext
```

Export writes active tasks as plaintext JSON. It refuses to run without
`--confirm-plaintext` and refuses to overwrite an existing file. The output
contains Sensitive Task Data with no protection — treat it like a password
export.

## Google Drive sync

Sync exchanges encrypted change artifacts (never a plaintext database) through
the Drive app-data folder. Google never sees task content; a Google account
compromise alone cannot read your tasks without the Recovery Secret.

One-time setup:

1. Create an OAuth **desktop app** client in Google Cloud and download its
   credentials JSON.
2. Log in (opens your system browser):

   ```sh
   tasks-remote login google -credentials /path/to/credentials.json
   ```

Push and pull:

```sh
tasks-remote sync push -google -credentials /path/to/credentials.json
tasks-remote sync pull -google -credentials /path/to/credentials.json
tasks-remote sync status
tasks-remote logout google
```

For local testing without Google, point sync at a directory that stands in for
the Drive app-data folder:

```sh
tasks-remote sync push -dir /tmp/fake-drive
tasks-remote sync pull -dir /tmp/fake-drive
```

### Restoring a second device

On a new machine, restore from the encrypted artifacts. You need both Google
authorization and the Recovery Secret:

```sh
tasks-remote login google -credentials /path/to/credentials.json
tasks-remote sync restore -google -credentials /path/to/credentials.json
```

Each device writes only its own change artifact
(`devices/<device-id>/changes-v1.json.enc`), so two devices syncing at the same
time never overwrite each other.

## Conflicts

Independent edits to different fields auto-merge. When two devices edit the same
task's content offline, or a delete collides with an edit, a Sync Conflict is
recorded and both versions are preserved.

```sh
tasks-remote conflicts
```

```text
conflict_ab12...  concurrent_edit  task=task_9f...
  local:  [device device_eb5efd7b1] Plan trip — drive to coast
  remote: [device device_0ebbe8c45] Plan trip — fly to Lisbon
  resolve: tasks-remote conflicts resolve conflict_ab12... --use local|remote
```

Pick the side to keep:

```sh
tasks-remote conflicts resolve <conflict-id> --use remote
```

Resolution is recorded as a new change that wins replay and converges to your
other devices on their next pull. A `duplicate_device_sequence` conflict (a
rejected duplicate) is dismissed with `conflicts resolve <conflict-id>` and no
`--use`.

## Automation

Set `TASKS_REMOTE_SECRET` to run non-interactively (CI, cron, headless Linux).
It overrides the OS keychain and avoids the unlock prompt. Never commit it or
pass it on a shared command line where it lands in shell history.
