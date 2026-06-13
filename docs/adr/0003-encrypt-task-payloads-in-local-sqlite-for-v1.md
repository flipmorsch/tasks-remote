# ADR 0003: Encrypt Task Payloads in Local SQLite for v1

## Status

Accepted

## Context

The product needs SQLite for local-first CLI behavior and must protect Sensitive Task Data at rest. Task Search, offline use, sync replay, and conflict resolution all depend on reliable local storage.

Whole-database SQLite encryption is attractive because it can protect tables, indexes, and WAL files behind one database key. SQLCipher is the established SQLite-compatible encryption option, but current Go integration is not straightforward: SQLCipher does not provide an official Go driver, and third-party Go SQLCipher wrappers may be stale, forked, or difficult to verify. Depending on an uncertain driver would put the security boundary at risk.

The v1 implementation needs a conservative security model that can be tested directly.

## Decision

For v1, use ordinary SQLite as the local metadata and durability store, but encrypt Sensitive Task Data in application-managed payloads before writing it to SQLite.

The local database may store non-sensitive operational metadata such as device identifiers, change identifiers, sequence numbers, sync state, conflict identifiers, and timestamps. It must not store plaintext task titles, note bodies, tag names, search terms, or other user-entered Sensitive Task Data.

Persistent Task Search indexes must not store plaintext content in v1. Search may be implemented by decrypting task payloads after unlock and building an in-memory index, or by scanning decrypted payloads for normal personal data sizes.

The storage layer must isolate encryption behind a small internal interface so a later whole-database encryption implementation can replace the v1 payload-encryption approach without changing task or sync domain code.

## Consequences

The v1 security boundary is explicit and testable: grep-style tests can assert that plaintext task content does not appear in local database files, WAL files, temp files, or cloud sync artifacts.

The Go build stays simpler because v1 does not require CGo or SQLCipher packaging.

The local database can still leak some metadata, such as the number of tasks, number of changes, rough edit timing, and sync state. This is acceptable for v1 but must be documented.

Task Search is less powerful than SQLite FTS in v1 because persistent plaintext FTS indexes are forbidden.

A future move to SQLCipher or another whole-database encryption layer should get a separate ADR after a working spike proves encryption, WAL behavior, migration, packaging, and tests across target platforms.

## Alternatives Considered

### Use SQLCipher Immediately

This would provide transparent database-page encryption and better support for encrypted persistent search indexes. It was not chosen for v1 because Go integration and packaging need a dedicated spike before they can be trusted as the main security boundary.

### Store a Plain SQLite Database Inside an Encrypted File Container

This could encrypt the database file while closed, but it is fragile while the app is running and complicates crash recovery, WAL behavior, and partial writes.

### Use Plain SQLite and Rely on Disk Encryption

This is convenient but makes the app's security depend on each machine's OS configuration. It does not satisfy the accepted requirement that the application protect Sensitive Task Data.
