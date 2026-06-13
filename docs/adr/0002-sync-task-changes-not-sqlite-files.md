# ADR 0002: Sync Task Changes, Not SQLite Files

## Status

Accepted

## Context

The product is local-first and uses a local database for fast task commands, offline work, and Task Search. The same Task Collection must be usable from multiple machines through Google Drive sync.

SQLite is a good local command database, but treating the SQLite database file as the cloud sync artifact would make multi-device edits fragile. Two machines can edit while offline, and whole-file replacement cannot safely represent independent Task Changes without risking lost updates, corruption, or opaque conflict handling.

The accepted conflict model requires independent task field edits to merge automatically where possible, while incompatible edits become explicit Sync Conflicts.

## Decision

Cloud sync must exchange encrypted Task Changes and sync metadata, not whole SQLite database files.

SQLite is the local projection of the Task Collection on an Unlocked Device. A device may rebuild or repair its local database by replaying decrypted Task Changes from protected sync data.

## Consequences

Independent edits from different devices can be merged without choosing one entire database file over another.

The application can preserve enough history to diagnose sync state, rebuild local data, and expose Sync Conflicts when user judgment is required.

Local database schema changes do not automatically become cloud sync format changes. The sync format needs explicit versioning and migration rules.

Sync implementation is more complex than file upload/download, and v1 must include tests for replay, idempotency, duplicate changes, interrupted sync, and conflicting offline edits.

## Alternatives Considered

### Sync the SQLite Database File

This is easy to understand and fast to prototype, but it makes concurrent device edits unsafe. Whole-file replacement can lose changes and produces conflicts that are hard for a person to understand.

### Use Google Drive as a Plain Document Store

This could make each task a cloud file, but it would leak product semantics into Drive, complicate encryption and batching, and make local-first behavior depend too heavily on cloud object layout.

### Use a Hosted Backend Instead of Drive Sync

A hosted backend could centralize conflict resolution and account management, but it adds infrastructure, operational cost, and a larger trust boundary. It is unnecessary for the accepted Personal Task Manager v1.
