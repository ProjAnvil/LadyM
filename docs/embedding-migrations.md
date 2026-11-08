# Embedding migration and recovery

An embedding space is identified by provider, model/version, dimension and
tokenizer/version, hashed as canonical JSON with SHA-256. Equal dimensions do
**not** imply compatible spaces. Ordinary Engine startup fails on unidentified
legacy vectors or a different fingerprint. Matching startup does not re-embed
stored content.

Injected providers should implement:

```go
Identity() schema.EmbeddingIdentity
```

All three strings must meaningfully name the provider and model/tokenizer
versions, and `Dim` must match the provider. Anonymous injection remains usable
for a fresh database; reopening stored vectors requires an identified provider
and explicit adoption or migration. Adoption is a caller attestation, not proof
that vectors came from a model. It never calls a provider or changes vectors.
An anonymous provider cannot subsequently claim equivalence to an adopted space.

## Local CLI

These commands open storage directly, bypassing normal Engine fingerprint
refusal. Status, rollback, finalize and adoption do not load a provider or need
its credentials. They affect the whole store, not just one workspace.

```sh
ladym --config target.toml migrate embeddings --db memory.db --status
ladym --config target.toml migrate embeddings --db memory.db --resume --batch-size 64
ladym --config target.toml migrate embeddings --db memory.db --rollback
ladym --config target.toml migrate embeddings --db memory.db --finalize
ladym migrate embeddings --db legacy.db --adopt \
  --provider host --model model-v1 --dim 768 --tokenizer tokenizer-v1
```

Select exactly one control. Resume uses the configured embedding provider/model;
`--provider`, `--model` and `--dim` can override configuration. Hashing's model
identity is `blake2b-feature-v1` and its tokenizer is the actual local CJK
tokenizer version. Hosted models use `provider-managed-v1`; hosts requiring
more precise tokenizer/version attribution should inject an identified provider.
`--tokenizer` supplies the explicit adoption identity, not a tokenizer override.
The existing flat TOML key `embedding_dim` configures hashing dimensions.
Do not adopt a different known fingerprint to reinterpret existing vectors;
perform a migration instead.

## State and durability

`idle → staging → rollback_window → idle`

- Begin snapshots old vectors and identity metadata into durable backup storage.
  Each attempt has a random generation. Same-target resume preserves it; another
  target conflicts until staging is explicitly aborted with rollback.
- Staging uses bytewise ordered ID keyset batches, never offsets. Each vector,
  generation/target fence and cursor update commits atomically. A failed provider
  call or cancelled operation leaves committed rows resumable.
- Activation validates **every eligible ID** and rejects missing, orphan,
  ineligible or incorrectly fenced rows. It switches vectors, dimension,
  index and identity metadata in one transaction. SQLite reloads its local index
  from the committed vector generation; PostgreSQL rebuilds pgvector/HNSW in the
  same transaction, with read locks protecting coherent pre-rewrite snapshots.
  Failed activation leaves the old space and backup intact.
- Explicit migration retains rollback. Rollback restores vectors and their
  identity, not memory bodies/access counters. Finalize deletes staging/backup
  vectors and unfreezes writes. It is intentionally irreversible.
- During staging and the rollback window, database gates reject memory creation,
  content/vector writes and structural or trust/scope metadata edits, including
  raw SQL. Touch/access updates remain allowed. Explicit Store deletion remains
  allowed: it atomically cancels staging, recursively invalidates descendants and
  removes deleted sources from backups. Rollback never recreates a deleted row.
  Automatic cutoff-based cleanup cannot use this explicit-delete exception.
- Existing Engine handles are fingerprint-fenced too: after an external space
  switch, their vector reads/writes fail rather than mix same-dimensional models.
  Normal Engine startup refuses staging; compatible active reads remain possible
  in the rollback window.

Path-identity resources are excluded from embedding/provider calls and remain
structural records with no active vector. Other records, including records that
previously lacked vectors, are eligible. No LLM, file import or external source
read participates in the migration.

Storage capacity temporarily includes active, staged and backup vectors; plan
for roughly three copies during staging. Backup persists until explicit
rollback/finalize. Database-administrator DDL or editing migration control tables
is outside the write-gate boundary.

## Embedded Go control

Open with `storage.OpenStore(cfg, dim)` rather than `engine.New(cfg)`, then use:

```go
err := engine.MigrateEmbeddings(ctx, store, target, provider, 64, true)
```

The final boolean retains rollback. The legacy `allow_dim_change` automatic
startup path uses the same durable migration but finalizes after success to
preserve existing write compatibility. Explicit API/CLI migration defaults to
retaining the rollback window.

Low-level runners must acquire a generation atomically with
`BeginEmbeddingMigrationGeneration(target)`, fetch
`EmbeddingMigrationBatch(generation, limit)`, and call
`StageEmbeddingMigration(id, vector, targetFingerprint, generation)` followed by
`ActivateEmbeddingMigration(generation)`. A separate begin/status pair is not a
safe way to acquire a fence. Late work cannot publish after abort/restart, even
when the new target has the same fingerprint.

Cancellation is checked before batches, before/after provider calls and before
activation. Providers with `EmbedContext` receive the context; legacy providers
run synchronously, so cancellation cannot interrupt an in-flight legacy call
but prevents its result from being staged. No fake-cancellation goroutines are
created. General contextual storage/provider work belongs to the later worker
task, not this migration feature.
