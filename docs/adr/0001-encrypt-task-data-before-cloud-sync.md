# ADR 0001: Encrypt Task Data Before Cloud Sync

## Status

Accepted

## Context

The product is a Personal Task Manager for one person's Task Collection across multiple machines. Task content is Sensitive Task Data and may include private identity, location, work, legal, financial, health, or security information.

The application will use the user's Google account for cloud sync, but Google Drive must not become a trusted plaintext database for task content. A Google account compromise, Drive export, Drive-side retention, OAuth token compromise, or accidental cloud exposure should not reveal the user's task content.

The v1 recovery target is local-first sync with a Recovery Window of at most about five minutes of recent edit loss, assuming network access, and about fifteen minutes to restore normal personal data sizes on a new machine.

## Decision

Cloud sync must store only Protected Task Copies. Sensitive Task Data must be encrypted before it leaves the user's device.

The user must control a Recovery Secret that is separate from their Google account. A new device needs both Google authorization and the Recovery Secret to decrypt synced task content.

Google Drive authorization is used only to store and retrieve the application's protected sync data. The application should request the narrowest Drive scope that supports this boundary.

## Consequences

Google Drive does not need to be trusted with plaintext task content.

Compromise of the Google account or Drive storage alone should not reveal Sensitive Task Data.

OAuth tokens are cloud access credentials, not data-decryption credentials, and must not be treated as a substitute for the Recovery Secret.

If the user loses the Recovery Secret and has no unlocked local device or plaintext export, synced task content cannot be recovered.

Sync, backup, migration, and export features must preserve the distinction between Protected Task Copies and unlocked task content.

## Alternatives Considered

### Store Plain SQLite Backups in Google Drive

This is simpler to implement and easier for users to inspect manually, but it makes Google Drive a plaintext database for Sensitive Task Data. It also makes Google account compromise or accidental Drive exposure much more damaging.

### Rely Only on Google Account Security

This avoids a separate Recovery Secret and reduces account setup friction, but it gives the cloud account enough authority to expose the user's task content. That does not match the accepted sensitivity tier.

### Use Google Drive Visibility Controls Without Client-Side Encryption

Storing data in an app-specific Drive area reduces accidental user-facing clutter and avoids broad Drive access, but Drive visibility controls are not a substitute for protecting Sensitive Task Data from cloud-side access or account compromise.
