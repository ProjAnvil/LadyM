package storage

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/ProjAnvil/LadyM/schema"
)

var ErrEmbeddingMigration = errors.New("embedding migration conflict or write freeze")

type EmbeddingFingerprintMismatch struct {
	Stored, Configured string
}

func (e *EmbeddingFingerprintMismatch) Error() string {
	return "embedding fingerprint mismatch; use migrate embeddings --resume, or --adopt for unidentified legacy vectors"
}

type migrationRecord struct {
	schema.MigrationStatus
	OldMeta map[string]string `json:"old_meta,omitempty"`
}

type migrationVector struct {
	ID     string
	Vector []float32
}

type migrationTransaction interface {
	memoryTransaction
	exec(string, ...any) error
	scan(string, []any, ...any) error
	vectors(string) ([]migrationVector, error)
	vectorForID(string) ([]float32, error)
	memoriesAfter(string, int) ([]*schema.Memory, error)
	switchVectors(string, int) error
	ensureDimension(int) error
	checkIdentity() error
}

// Backend adapters own transactions and vector encoding. The state machine is
// shared so generation fencing, completeness and rollback cannot drift.
type embeddingMigrations struct {
	begin       func() (migrationTransaction, error)
	bound       bool
	fingerprint string
}

// BindEmbeddingIdentity fences an Engine's store handle to its provider space.
// Binding happens once, before the handle is shared with layers or goroutines.
func BindEmbeddingIdentity(store Store, fingerprint string) error {
	binder, ok := store.(interface{ bindEmbeddingIdentity(string) })
	if !ok {
		return errors.New("store does not support embedding identity binding")
	}
	binder.bindEmbeddingIdentity(fingerprint)
	return nil
}

func (m *embeddingMigrations) bindEmbeddingIdentity(fingerprint string) {
	m.bound, m.fingerprint = true, fingerprint
}

func migrationCheckIdentity(tx migrationTransaction, binding *embeddingMigrations) error {
	if binding == nil || !binding.bound {
		return nil
	}
	var fingerprint string
	if err := tx.scan("SELECT COALESCE((SELECT value FROM meta WHERE key='embedding_fingerprint'),'')", nil, &fingerprint); err != nil {
		return err
	}
	if fingerprint != binding.fingerprint {
		return &EmbeddingFingerprintMismatch{Stored: fingerprint, Configured: binding.fingerprint}
	}
	return nil
}

var embeddingMetaKeys = []string{"embedding_identity", "embedding_fingerprint", "embedding_dim", "embedding_provider", "embedding_tokenizer"}

func migrationRead(tx migrationTransaction) (*migrationRecord, error) {
	var raw string
	if err := tx.scan("SELECT data FROM embedding_migration WHERE id=1", nil, &raw); err != nil {
		return nil, err
	}
	var state migrationRecord
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return nil, err
	}
	var fp, identity string
	if err := tx.scan("SELECT COALESCE((SELECT value FROM meta WHERE key='embedding_fingerprint'),'')", nil, &fp); err != nil {
		return nil, err
	}
	if err := tx.scan("SELECT COALESCE((SELECT value FROM meta WHERE key='embedding_identity'),'')", nil, &identity); err != nil {
		return nil, err
	}
	state.ActiveFingerprint = fp
	if identity != "" {
		if err := json.Unmarshal([]byte(identity), &state.ActiveIdentity); err != nil {
			return nil, err
		}
	} else {
		state.ActiveIdentity = schema.EmbeddingIdentity{}
		var dim string
		if err := tx.scan("SELECT COALESCE((SELECT value FROM meta WHERE key='embedding_dim'),'0')", nil, &dim); err != nil {
			return nil, err
		}
		state.ActiveIdentity.Dim, _ = strconv.Atoi(dim)
	}
	return &state, nil
}

func migrationSave(tx migrationTransaction, state *migrationRecord) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return tx.exec("UPDATE embedding_migration SET data=?, internal=0 WHERE id=1", string(raw))
}

func migrationInternal(tx migrationTransaction) error {
	return tx.exec("UPDATE embedding_migration SET internal=1 WHERE id=1")
}

func migrationSetIdentity(tx migrationTransaction, identity schema.EmbeddingIdentity) error {
	raw, err := json.Marshal(identity)
	if err != nil {
		return err
	}
	values := map[string]string{
		"embedding_identity": string(raw), "embedding_fingerprint": identity.Fingerprint(),
		"embedding_dim": strconv.Itoa(identity.Dim), "embedding_provider": identity.Provider,
	}
	// Preserve the legacy tokenizer key's hashing-only contract.
	if identity.Provider == "hashing" {
		values["embedding_tokenizer"] = identity.Tokenizer
	} else {
		values["embedding_tokenizer"] = ""
	}
	for _, key := range embeddingMetaKeys {
		if err := tx.exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, values[key]); err != nil {
			return err
		}
	}
	return nil
}

func migrationEligible(m *schema.Memory) bool {
	return m.Type != schema.TypeResource || m.MetaString("resource_identity") != "path"
}

func migrationCandidates(tx migrationTransaction, cursor string, limit int) ([]*schema.Memory, error) {
	// Backend SQL uses a bytewise collation and an ID keyset, never offsets.
	all, err := tx.memoriesAfter(cursor, limit)
	return all, err
}

func migrationFence(state *migrationRecord, generation string) error {
	if state.State != "staging" || generation == "" || generation != state.Generation {
		return ErrEmbeddingMigration
	}
	return nil
}

func (m *embeddingMigrations) EmbeddingMigrationStatus() (*schema.MigrationStatus, error) {
	tx, err := m.begin()
	if err != nil {
		return nil, err
	}
	defer tx.rollback()
	state, err := migrationRead(tx)
	if err != nil {
		return nil, err
	}
	return &state.MigrationStatus, tx.commit()
}

func (m *embeddingMigrations) AdoptEmbeddingIdentity(identity schema.EmbeddingIdentity) error {
	if err := identity.Validate(); err != nil {
		return err
	}
	tx, err := m.begin()
	if err != nil {
		return err
	}
	defer tx.rollback()
	state, err := migrationRead(tx)
	if err != nil {
		return err
	}
	if state.State != "idle" || state.ActiveFingerprint != "" && state.ActiveFingerprint != identity.Fingerprint() {
		return ErrEmbeddingMigration
	}
	vectors, err := tx.vectors("memories")
	if err != nil {
		return err
	}
	for _, row := range vectors {
		if row.Vector != nil {
			if err := validateVector(row.Vector, identity.Dim); err != nil {
				return err
			}
		}
	}
	if err := tx.ensureDimension(identity.Dim); err != nil {
		return err
	}
	if err := migrationSetIdentity(tx, identity); err != nil {
		return err
	}
	return tx.commit()
}

func (m *embeddingMigrations) BeginEmbeddingMigration(target schema.EmbeddingIdentity) error {
	return m.beginEmbeddingMigration(target, nil, false)
}

// BeginEmbeddingMigrationGeneration acquires the fence in the same transaction
// as begin/resume; a separate status read could inherit an intervening restart.
func (m *embeddingMigrations) BeginEmbeddingMigrationGeneration(target schema.EmbeddingIdentity) (string, error) {
	var generation string
	err := m.beginEmbeddingMigration(target, &generation, false)
	return generation, err
}

// BeginAutomaticEmbeddingMigrationGeneration persists intent before any vector
// work. Resuming cannot change the rollback policy selected by the first runner.
func (m *embeddingMigrations) BeginAutomaticEmbeddingMigrationGeneration(target schema.EmbeddingIdentity) (string, error) {
	var generation string
	err := m.beginEmbeddingMigration(target, &generation, true)
	return generation, err
}

func (m *embeddingMigrations) beginEmbeddingMigration(target schema.EmbeddingIdentity, acquired *string, autoFinalize bool) error {
	if err := target.Validate(); err != nil {
		return err
	}
	tx, err := m.begin()
	if err != nil {
		return err
	}
	defer tx.rollback()
	state, err := migrationRead(tx)
	if err != nil {
		return err
	}
	fp := target.Fingerprint()
	if state.State == "staging" && state.TargetFingerprint == fp {
		if state.AutoFinalize != autoFinalize {
			return ErrEmbeddingMigration
		}
		if acquired != nil {
			*acquired = state.Generation
		}
		return tx.commit()
	}
	if state.State != "idle" {
		return ErrEmbeddingMigration
	}
	var generation [16]byte
	if _, err := rand.Read(generation[:]); err != nil {
		return err
	}
	state.State, state.TargetIdentity, state.TargetFingerprint = "staging", target, fp
	state.AutoFinalize = autoFinalize
	state.Generation, state.Cursor, state.Processed = hex.EncodeToString(generation[:]), "", 0
	state.OldMeta = map[string]string{}
	for _, key := range embeddingMetaKeys {
		var value string
		if err := tx.scan("SELECT COALESCE((SELECT value FROM meta WHERE key=?),'')", []any{key}, &value); err != nil {
			return err
		}
		state.OldMeta[key] = value
	}
	if err := tx.exec("DELETE FROM embedding_staging"); err != nil {
		return err
	}
	if err := tx.exec("DELETE FROM embedding_backup"); err != nil {
		return err
	}
	vectors, err := tx.vectors("memories")
	if err != nil {
		return err
	}
	for _, row := range vectors {
		raw, err := json.Marshal(row.Vector)
		if err != nil {
			return err
		}
		if err := tx.exec("INSERT INTO embedding_backup(id,vector) VALUES(?,?)", row.ID, string(raw)); err != nil {
			return err
		}
		if state.ActiveIdentity.Dim == 0 && row.Vector != nil {
			state.ActiveIdentity.Dim = len(row.Vector)
		}
	}
	if state.ActiveIdentity.Dim == 0 {
		// An empty legacy space has nothing to restore; use the target dimension.
		state.ActiveIdentity.Dim = target.Dim
	}
	if err := migrationSave(tx, state); err != nil {
		return err
	}
	if acquired != nil {
		*acquired = state.Generation
	}
	return tx.commit()
}

func (m *embeddingMigrations) EmbeddingMigrationBatch(generation string, limit int) ([]*schema.Memory, error) {
	if limit < 1 || limit > 10000 {
		return nil, errors.New("migration batch size must be 1..10000")
	}
	tx, err := m.begin()
	if err != nil {
		return nil, err
	}
	defer tx.rollback()
	state, err := migrationRead(tx)
	if err != nil {
		return nil, err
	}
	if err := migrationFence(state, generation); err != nil {
		return nil, err
	}
	batch, err := migrationCandidates(tx, state.Cursor, limit)
	if err != nil {
		return nil, err
	}
	return batch, tx.commit()
}

func (m *embeddingMigrations) StageEmbeddingMigration(id string, vector []float32, fingerprint, generation string) error {
	tx, err := m.begin()
	if err != nil {
		return err
	}
	defer tx.rollback()
	state, err := migrationRead(tx)
	if err != nil {
		return err
	}
	if err := migrationFence(state, generation); err != nil {
		return err
	}
	if state.TargetFingerprint != fingerprint {
		return ErrEmbeddingMigration
	}
	if err := validateVector(vector, state.TargetIdentity.Dim); err != nil {
		return err
	}
	next, err := migrationCandidates(tx, state.Cursor, 1)
	if err != nil {
		return err
	}
	if len(next) != 1 || next[0].ID != id {
		return fmt.Errorf("%w: stage must advance the next eligible ID", ErrEmbeddingMigration)
	}
	raw, err := json.Marshal(vector)
	if err != nil {
		return err
	}
	if err := tx.exec("INSERT INTO embedding_staging(id,vector,generation,fingerprint) VALUES(?,?,?,?)", id, string(raw), generation, fingerprint); err != nil {
		return err
	}
	state.Cursor, state.Processed = id, state.Processed+1
	if err := migrationSave(tx, state); err != nil {
		return err
	}
	return tx.commit()
}

func (m *embeddingMigrations) ActivateEmbeddingMigration(generation string) error {
	tx, err := m.begin()
	if err != nil {
		return err
	}
	defer tx.rollback()
	state, err := migrationRead(tx)
	if err != nil {
		return err
	}
	if err := migrationFence(state, generation); err != nil {
		return err
	}
	all, err := tx.memories("")
	if err != nil {
		return err
	}
	staged, err := tx.vectors("embedding_staging")
	if err != nil {
		return err
	}
	byID := map[string][]float32{}
	for _, row := range staged {
		if err := validateVector(row.Vector, state.TargetIdentity.Dim); err != nil {
			return err
		}
		var gen, fp string
		if err := tx.scan("SELECT generation,fingerprint FROM embedding_staging WHERE id=?", []any{row.ID}, &gen, &fp); err != nil {
			return err
		}
		if gen != generation || fp != state.TargetFingerprint {
			return ErrEmbeddingMigration
		}
		byID[row.ID] = row.Vector
	}
	// Check membership in both directions, not just equal counts.
	for _, memory := range all {
		_, exists := byID[memory.ID]
		if migrationEligible(memory) != exists {
			return fmt.Errorf("%w: incomplete or ineligible staged ID %s", ErrEmbeddingMigration, memory.ID)
		}
		delete(byID, memory.ID)
	}
	if len(byID) != 0 {
		return fmt.Errorf("%w: orphan staged IDs", ErrEmbeddingMigration)
	}
	if err := migrationInternal(tx); err != nil {
		return err
	}
	if err := tx.switchVectors("embedding_staging", state.TargetIdentity.Dim); err != nil {
		return err
	}
	if err := migrationSetIdentity(tx, state.TargetIdentity); err != nil {
		return err
	}
	state.State, state.RollbackAvailable = "rollback_window", true
	if err := migrationSave(tx, state); err != nil {
		return err
	}
	return tx.commit()
}

func migrationClear(tx migrationTransaction, state *migrationRecord) error {
	if err := tx.exec("DELETE FROM embedding_staging"); err != nil {
		return err
	}
	if err := tx.exec("DELETE FROM embedding_backup"); err != nil {
		return err
	}
	state.State, state.Generation, state.TargetFingerprint, state.Cursor = "idle", "", "", ""
	state.Processed, state.RollbackAvailable = 0, false
	state.AutoFinalize = false
	state.TargetIdentity, state.OldMeta = schema.EmbeddingIdentity{}, nil
	return migrationSave(tx, state)
}

func (m *embeddingMigrations) RollbackEmbeddingMigration() error {
	tx, err := m.begin()
	if err != nil {
		return err
	}
	defer tx.rollback()
	state, err := migrationRead(tx)
	if err != nil {
		return err
	}
	if state.State == "idle" {
		return ErrEmbeddingMigration
	}
	if state.RollbackAvailable {
		oldDim, _ := strconv.Atoi(state.OldMeta["embedding_dim"])
		if oldDim == 0 {
			vectors, err := tx.vectors("embedding_backup")
			if err != nil {
				return err
			}
			for _, row := range vectors {
				if row.Vector != nil {
					oldDim = len(row.Vector)
					break
				}
			}
		}
		if oldDim == 0 {
			oldDim = state.TargetIdentity.Dim
		}
		if err := migrationInternal(tx); err != nil {
			return err
		}
		if err := tx.switchVectors("embedding_backup", oldDim); err != nil {
			return err
		}
		for _, key := range embeddingMetaKeys {
			if err := tx.exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, state.OldMeta[key]); err != nil {
				return err
			}
		}
	}
	if err := migrationClear(tx, state); err != nil {
		return err
	}
	return tx.commit()
}

func (m *embeddingMigrations) FinalizeEmbeddingMigration() error {
	return m.finalizeEmbeddingMigration("", "", false)
}

// FinalizeAutomaticEmbeddingMigration can clear only the captured automatic
// generation and space, never an administrator's later rollback window.
func (m *embeddingMigrations) FinalizeAutomaticEmbeddingMigration(generation, targetFingerprint string) error {
	return m.finalizeEmbeddingMigration(generation, targetFingerprint, true)
}

func (m *embeddingMigrations) finalizeEmbeddingMigration(generation, targetFingerprint string, automatic bool) error {
	tx, err := m.begin()
	if err != nil {
		return err
	}
	defer tx.rollback()
	state, err := migrationRead(tx)
	if err != nil {
		return err
	}
	if state.State != "rollback_window" || !state.RollbackAvailable {
		return ErrEmbeddingMigration
	}
	if automatic && (!state.AutoFinalize || generation == "" || generation != state.Generation ||
		targetFingerprint == "" || targetFingerprint != state.TargetFingerprint || targetFingerprint != state.ActiveFingerprint) {
		return ErrEmbeddingMigration
	}
	if err := migrationClear(tx, state); err != nil {
		return err
	}
	return tx.commit()
}

func (m *embeddingMigrations) GetEmbedding(id string) ([]float32, error) {
	tx, err := m.begin()
	if err != nil {
		return nil, err
	}
	defer tx.rollback()
	if err := tx.checkIdentity(); err != nil {
		return nil, err
	}
	vectors, err := tx.vectorForID(id)
	if err != nil {
		return nil, err
	}
	return slices.Clone(vectors), tx.commit()
}

func migrationPrepareDelete(tx migrationTransaction) error {
	state, err := migrationRead(tx)
	if err != nil {
		return err
	}
	if state.State == "staging" {
		if err := migrationClear(tx, state); err != nil {
			return err
		}
	}
	return migrationInternal(tx)
}
