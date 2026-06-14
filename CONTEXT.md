# Context

## Glossary

### Personal Task Manager

A task management product for one person managing their own notes, reminders, and task lists across devices.

Use "Personal Task Manager" when discussing the product boundary, ownership model, and expected collaboration behavior.
Do not use "Personal Task Manager" for team workspaces, shared task lists, delegated assignments, or organization-owned data.

Related terms: Task Collection

### Recovery Window

The acceptable amount of recent work loss and restore time after a device is lost, damaged, or replaced.

Use "Recovery Window" when discussing how quickly a person should recover their Task Collection after failure.
Do not use "Recovery Window" for ordinary sync delay between healthy devices.

Related terms: Task Collection, Sensitive Task Data

### Task Change

A user action that creates, edits, completes, restores, moves, or deletes part of the Task Collection.

Use "Task Change" when discussing how work from multiple devices is preserved and reconciled.
Do not use "Task Change" for a full copy of the user's stored data.

Related terms: Task Collection

### Sync Conflict

A situation where changes from two devices cannot be combined without user judgment.

Use "Sync Conflict" when two offline edits affect the same meaning in incompatible ways.
Do not use "Sync Conflict" for normal edits to different fields that can be merged automatically.

Related terms: Task Change

### Task Collection

The complete set of tasks, notes, lists, metadata, and history owned by one person in the product.

Use "Task Collection" when discussing the user's private body of task data as a whole.
Do not use "Task Collection" for a single task, a shared workspace, or a storage file.

Related terms: Personal Task Manager

### Task Collection Setup

The first-use path where a person either creates a new Task Collection or restores an existing Task Collection onto the device.

Use "Task Collection Setup" when discussing the choices available before the device has local access to a Task Collection.
Do not use "Task Collection Setup" for unlocking an existing local Task Collection, ordinary sync, or routine task work.

Related terms: Task Collection, Recovery Secret, Protected Task Copy, Restore Confirmation

### Sensitive Task Data

Personal task content that may reveal private identity, plans, relationships, locations, health, work, legal, financial, or security information.

Use "Sensitive Task Data" when discussing privacy, protection level, retention, recovery, export, and sync safety.
Do not use "Sensitive Task Data" to imply regulated medical, payment-card, or enterprise compliance requirements unless those become explicit product goals.

Related terms: Task Collection

### Recovery Secret

A user-controlled secret that allows a person to unlock their protected Task Collection on a new or restored device.

Use "Recovery Secret" when discussing whether the person can regain access to Sensitive Task Data after changing devices.
Do not use "Recovery Secret" for the Google account password, device login password, or a cloud access token.

Related terms: Sensitive Task Data, Task Collection

### Protected Task Copy

A copy of the Task Collection that is safe to store outside the user's device because its Sensitive Task Data cannot be read without the Recovery Secret.

Use "Protected Task Copy" when discussing backup, restore, sync, and cloud storage safety.
Do not use "Protected Task Copy" for plaintext exports or unlocked local views of task content.

Related terms: Task Collection, Sensitive Task Data, Recovery Secret

### Unlocked Device

A device that currently has access to the user's Task Collection after the person has provided or restored valid unlock material.

Use "Unlocked Device" when discussing whether the app can show, edit, export, or sync task content without prompting again.
Do not use "Unlocked Device" for a device that only has Google authorization.

Related terms: Recovery Secret, Protected Task Copy

### Locked Device

A device that may have local files or cloud authorization but cannot read Sensitive Task Data until the person unlocks it.

Use "Locked Device" when discussing startup, logout, background sync, and loss or theft scenarios.
Do not use "Locked Device" for an unauthenticated Google account state alone.

Related terms: Unlocked Device, Sensitive Task Data

### Sync Health

The user's current confidence that recent Task Changes have been protected and can be restored on another device.

Use "Sync Health" when discussing whether sync is not configured, locked, pending local changes, caught up, retrying, blocked, or waiting for conflict resolution.
Do not use "Sync Health" for whether local task commands can be used, or to imply that ordinary task work must stop while a Sync Conflict is open.

Related terms: Task Change, Recovery Window, Sync Conflict

### Sync Now

A deliberate user action that exchanges Task Changes with the configured Protected Task Copy and then refreshes the local view of the Task Collection.

Use "Sync Now" when discussing the primary manual sync action in the Interactive Task Surface.
Do not use "Sync Now" for background sync, restore, export, setup-only actions, or individual low-level upload and download controls.

Related terms: Sync Health, Task Change, Protected Task Copy, Interactive Task Surface

### Interactive Task Surface

The default person-facing workspace for reviewing current work, creating and changing tasks, organizing tasks, searching the Task Collection, resolving Sync Conflicts, checking Sync Health, and starting recovery actions without memorizing commands.

Use "Interactive Task Surface" when discussing the product experience shown when a person opens the Personal Task Manager directly in an interactive terminal.
Do not use "Interactive Task Surface" for replacing command syntax, storage files, background automation, non-interactive shell use, or cloud-side task access.

Related terms: Personal Task Manager, Task Collection, Sync Health, Unlocked Device, Locked Device

### Working View

A task-focused view that helps the person act on overdue, due, upcoming, reminded, and open work before browsing the whole Task Collection.

Use "Working View" when discussing the primary unlocked view of the Interactive Task Surface.
Do not use "Working View" for reading full task notes, viewing task identifiers, completed-task browsing, the full task history, cloud sync state, or recovery flow.

Related terms: Interactive Task Surface, Task Collection, Task Search

### Local Sync Setup

Device-local information that helps the person start manual sync actions without re-entering non-secret setup details each time, including the chosen sync location for the current Task Collection.

Use "Local Sync Setup" when discussing saved sync convenience state on a device.
Do not use "Local Sync Setup" for OAuth tokens, the Recovery Secret, Sensitive Task Data, Protected Task Copies, or setup shared across unrelated Task Collections.

Related terms: Sync Health, Recovery Secret, Protected Task Copy, Locked Device

### Destructive Task Action

A user action that removes task content from the normal working view and may require recovery or sync history to revisit later.

Use "Destructive Task Action" when discussing actions that should require deliberate confirmation before changing the Task Collection.
Do not use "Destructive Task Action" for completion, tag changes, ordinary edits, or manual sync actions.

Related terms: Task Change, Task Collection, Restore Confirmation

### Task Form

A deliberate create or edit flow where the person reviews task fields before saving a Task Change.

Use "Task Form" when discussing task creation or editing that can be saved or canceled before it changes the Task Collection.
Do not use "Task Form" for quick actions such as completion, reopening, confirmed deletion, search, or sync.

Related terms: Task Change, Interactive Task Surface, Working View

### Restore Confirmation

A deliberate user approval before replacing or rebuilding local access to a Task Collection from a Protected Task Copy on a device that may already hold local task data.

Use "Restore Confirmation" when a recovery action could change the local view of the Task Collection or overwrite local recovery state.
Do not use "Restore Confirmation" for ordinary sync between healthy devices or for viewing Sync Health.

Related terms: Protected Task Copy, Recovery Secret, Task Collection, Sync Health

### Task Search

The ability to find tasks and notes by matching text inside the user's Task Collection.

Use "Task Search" when discussing how the person finds their own task content on an Unlocked Device.
Do not use "Task Search" for cloud-side indexing, Google Drive search, searching another person's data, or exposing full task notes in compact result lists.

Related terms: Task Collection, Unlocked Device, Sensitive Task Data
