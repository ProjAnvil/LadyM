package engine

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// MigrateEmbeddings is deliberately independent of Engine.New: recovery must
// open a mismatched store without first admitting queries in the target space.
// keepRollback selects intent for new work; administrative resume honors the
// persisted policy, while automatic runners cannot take over explicit staging.
func MigrateEmbeddings(ctx context.Context, store storage.Store, target schema.EmbeddingIdentity, provider storage.EmbeddingProvider, batchSize int, keepRollback bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := target.Validate(); err != nil {
		return err
	}
	if provider == nil || provider.Dim() != target.Dim {
		return errors.New("migration provider dimension does not match target")
	}
	if identified, ok := provider.(interface {
		Identity() schema.EmbeddingIdentity
	}); ok {
		if identity := identified.Identity(); identity.Validate() == nil && identity.Fingerprint() != target.Fingerprint() {
			return errors.New("migration provider identity does not match target")
		}
	}
	if batchSize < 1 || batchSize > 10000 {
		return errors.New("migration batch size must be 1..10000")
	}
	status, err := store.EmbeddingMigrationStatus()
	if err != nil {
		return err
	}
	if status.ActiveFingerprint == target.Fingerprint() && status.State != "staging" {
		if status.RollbackAvailable && status.AutoFinalize {
			return store.FinalizeAutomaticEmbeddingMigration(status.Generation, target.Fingerprint())
		}
		return nil
	}
	var generation string
	if keepRollback {
		generation, err = store.BeginEmbeddingMigrationGeneration(target)
	} else {
		generation, err = store.BeginAutomaticEmbeddingMigrationGeneration(target)
	}
	if err != nil {
		return err
	}
	status, err = store.EmbeddingMigrationStatus()
	if err != nil {
		return err
	}
	if status.State != "staging" || status.Generation != generation || status.TargetFingerprint != target.Fingerprint() {
		return storage.ErrEmbeddingMigration
	}
	autoFinalize := status.AutoFinalize
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch, err := store.EmbeddingMigrationBatch(generation, batchSize)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		for _, memory := range batch {
			if err := ctx.Err(); err != nil {
				return err
			}
			var vector []float32
			if contextual, ok := provider.(interface {
				EmbedContext(context.Context, string) ([]float32, error)
			}); ok {
				vector, err = contextual.EmbedContext(ctx, memory.Content)
			} else {
				vector, err = provider.Embed(memory.Content)
			}
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := store.StageEmbeddingMigration(memory.ID, vector, target.Fingerprint(), generation); err != nil {
				return err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.ActivateEmbeddingMigration(generation); err != nil {
		return err
	}
	if autoFinalize {
		return store.FinalizeAutomaticEmbeddingMigration(generation, target.Fingerprint())
	}
	return nil
}

// MigrationProvider resolves configured providers for the CLI without Engine
// fingerprint enforcement. Hosts may instead pass their own provider/identity.
func MigrationProvider(cfg *config.Config) (storage.EmbeddingProvider, schema.EmbeddingIdentity, error) {
	if cfg.CJKDictDir != "" {
		storage.SetCJKDictDir(cfg.CJKDictDir)
	}
	if cfg.EmbeddingCJKTokenizer != "" {
		storage.SetCJKTokenizerMode(cfg.EmbeddingCJKTokenizer)
	}
	provider, err := storage.MakeProvider(cfg)
	if err != nil {
		return nil, schema.EmbeddingIdentity{}, err
	}
	if provider.Dim() == 0 {
		if _, err := provider.Embed("dimensionality probe"); err != nil {
			return nil, schema.EmbeddingIdentity{}, err
		}
	}
	identity, err := storage.ProviderEmbeddingIdentity(cfg, provider, false)
	return provider, identity, err
}

func (e *Engine) embeddingIdentity() (schema.EmbeddingIdentity, error) {
	return storage.ProviderEmbeddingIdentity(e.Config, e.Provider, e.routing != nil && e.routing.Embedding != nil)
}

func (e *Engine) enforceEmbeddingFingerprint() error {
	// Keep metadata I/O errors visible through the existing Store facade.
	dim, err := e.Store.GetMeta("embedding_dim")
	if err != nil {
		return err
	}
	identity, identityErr := e.embeddingIdentity()
	status, err := e.Store.EmbeddingMigrationStatus()
	if err != nil {
		return err
	}
	if identityErr != nil {
		all, err := e.Store.IterMemories("", "", "")
		if err != nil {
			return err
		}
		for _, memory := range all {
			vector, err := e.Store.GetEmbedding(memory.ID)
			if err != nil {
				return err
			}
			if vector != nil {
				return fmt.Errorf("%w: unknown injected identity; provide Identity() and explicitly adopt or migrate legacy vectors", identityErr)
			}
		}
		if status.State != "idle" || status.ActiveFingerprint != "" {
			return identityErr
		}
		if err := e.Store.SetMeta("embedding_dim", strconv.Itoa(e.Provider.Dim())); err != nil {
			return err
		}
		return e.Store.SetMeta("embedding_provider", "injected")
	}
	if status.State == "staging" {
		if e.Config.EmbeddingAllowDimChange && status.AutoFinalize && status.TargetFingerprint == identity.Fingerprint() {
			return MigrateEmbeddings(context.Background(), e.Store, identity, e.Provider, 64, false)
		}
		return fmt.Errorf("%w: staging migration %s; use explicit status/resume/rollback", storage.ErrEmbeddingMigration, status.Generation)
	}
	if status.ActiveFingerprint == identity.Fingerprint() {
		if status.State == "rollback_window" && status.AutoFinalize {
			return e.Store.FinalizeAutomaticEmbeddingMigration(status.Generation, identity.Fingerprint())
		}
		legacyTokenizer, err := e.Store.GetMeta("embedding_tokenizer")
		if err != nil {
			return err
		}
		if dim == strconv.Itoa(identity.Dim) && (identity.Provider != "hashing" || legacyTokenizer == identity.Tokenizer) {
			return nil
		}
		if e.Config.EmbeddingAllowDimChange && status.State == "idle" {
			if _, err := e.Store.BeginAutomaticEmbeddingMigrationGeneration(identity); err != nil {
				return err
			}
			return MigrateEmbeddings(context.Background(), e.Store, identity, e.Provider, 64, false)
		}
		return &storage.EmbeddingFingerprintMismatch{Stored: status.ActiveFingerprint, Configured: identity.Fingerprint()}
	}
	if status.ActiveFingerprint == "" {
		all, err := e.Store.IterMemories("", "", "")
		if err != nil {
			return err
		}
		hasVectors := false
		for _, memory := range all {
			vector, err := e.Store.GetEmbedding(memory.ID)
			if err != nil {
				return err
			}
			hasVectors = hasVectors || vector != nil
		}
		if !hasVectors && status.State == "idle" {
			return e.Store.AdoptEmbeddingIdentity(identity)
		}
		if e.Config.EmbeddingAllowDimChange {
			return MigrateEmbeddings(context.Background(), e.Store, identity, e.Provider, 64, false)
		}
		return &storage.EmbeddingFingerprintMismatch{Configured: identity.Fingerprint()}
	}
	if e.Config.EmbeddingAllowDimChange && status.State == "idle" {
		return MigrateEmbeddings(context.Background(), e.Store, identity, e.Provider, 64, false)
	}
	storedDim, _ := strconv.Atoi(dim)
	if storedDim != identity.Dim {
		return storage.AssertDimMatches(storedDim, identity.Dim)
	}
	if strings.EqualFold(identity.Provider, "hashing") && status.ActiveIdentity.Tokenizer != identity.Tokenizer {
		return &storage.EmbeddingTokenizerMismatch{Stored: status.ActiveIdentity.Tokenizer, Configured: identity.Tokenizer}
	}
	return &storage.EmbeddingFingerprintMismatch{Stored: status.ActiveFingerprint, Configured: identity.Fingerprint()}
}
