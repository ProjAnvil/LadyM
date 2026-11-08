package cli

import (
	"encoding/json"
	"errors"

	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
	"github.com/spf13/cobra"
)

func migrateCmd() *cobra.Command {
	parent := &cobra.Command{Use: "migrate", Short: "Explicit storage migration and recovery."}
	var db, workspace, providerName, model, tokenizer string
	var dim, batchSize int
	var status, resume, rollback, finalize, adopt bool
	cmd := &cobra.Command{
		Use: "embeddings", Short: "Status, resume, rollback, finalize or explicitly adopt an embedding space.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) (result error) {
			actions := 0
			for _, selected := range []bool{status, resume, rollback, finalize, adopt} {
				if selected {
					actions++
				}
			}
			if actions != 1 {
				return errors.New("select exactly one of --status, --resume, --rollback, --finalize or --adopt")
			}
			cfg, err := loadConfig(db, workspace)
			if err != nil {
				return err
			}
			if dim > 0 {
				cfg.EmbeddingDim = dim
			}
			if providerName != "" {
				cfg.EmbeddingProvider = providerName
			}
			if model != "" {
				cfg.EmbeddingModel = model
			}
			var provider storage.EmbeddingProvider
			var identity schema.EmbeddingIdentity
			if resume {
				provider, identity, err = engine.MigrationProvider(cfg)
				if err != nil {
					return err
				}
				cfg.EmbeddingDim = identity.Dim
			}
			// No Engine.New here, and status/control/adoption never call a provider.
			store, err := storage.OpenStore(cfg, cfg.EmbeddingDim)
			if err != nil {
				return err
			}
			defer func() { result = errors.Join(result, store.Close()) }()
			switch {
			case resume:
				err = engine.MigrateEmbeddings(cmd.Context(), store, identity, provider, batchSize, true)
			case rollback:
				err = store.RollbackEmbeddingMigration()
			case finalize:
				err = store.FinalizeEmbeddingMigration()
			case adopt:
				identity = schema.EmbeddingIdentity{Provider: providerName, Model: model, Dim: dim, Tokenizer: tokenizer}
				err = store.AdoptEmbeddingIdentity(identity)
			}
			if err != nil {
				return err
			}
			current, err := store.EmbeddingMigrationStatus()
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(current)
		},
	}
	addDBWS(cmd, &db, &workspace)
	cmd.Flags().BoolVar(&status, "status", false, "Read durable migration status without loading a provider")
	cmd.Flags().BoolVar(&resume, "resume", false, "Stage/resume configured embeddings; retain rollback backup")
	cmd.Flags().BoolVar(&rollback, "rollback", false, "Abort staging or atomically restore the previous space")
	cmd.Flags().BoolVar(&finalize, "finalize", false, "Delete backup and unfreeze writes")
	cmd.Flags().BoolVar(&adopt, "adopt", false, "Attest legacy vector identity without changing vectors")
	cmd.Flags().StringVar(&providerName, "provider", "", "Provider identity/configuration")
	cmd.Flags().StringVar(&model, "model", "", "Model/version identity/configuration")
	cmd.Flags().StringVar(&tokenizer, "tokenizer", "", "Tokenizer/version identity (required for adoption)")
	cmd.Flags().IntVar(&dim, "dim", 0, "Embedding dimension (required for adoption)")
	cmd.Flags().IntVar(&batchSize, "batch-size", 64, "Finite ID keyset batch size, 1..10000")
	parent.AddCommand(cmd)
	return parent
}
