# ladyM memory provider — next steps

The plugin is installed. Now enable the provider and get the `ladym` binary:

1. **Enable the provider** (this also registers the `hermes ladym` CLI —
   plugin commands only exist for the active memory provider):

   ```sh
   hermes memory setup ladym
   ```

2. **Install the ladyM binary** (one command, downloads from GitHub releases):

   ```sh
   hermes ladym install
   ```

   Chinese users: add `--fulldict` for the variant with the embedded CJK
   dictionary (better Chinese recall, +31MB):

   ```sh
   hermes ladym install --fulldict
   ```

   Other options: `--version vX.Y.Z` to pin a release, `--force` to
   overwrite an existing install. (Windows/unsupported platforms: build from
   source with `go build -o bin/ladym ./cmd/ladym` and set `LADYM_BIN`.)

3. **Verify:**

   ```sh
   hermes memory status     # Provider: ladym, available ✓
   hermes ladym status      # binary path, effective config, store stats
   ```

All state lives under `$HERMES_HOME/ladym/` — profile-isolated and covered by
`hermes backup`.
