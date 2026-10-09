# AGENTS.md

Guidance for AI coding agents working in this repository. User-facing documentation is in `README.md`.

## Project Overview

Kubeconfig Composer is a CLI tool for merging multiple Kubernetes configuration files with automatic conflict resolution. It scans directories for kubeconfig files, merges them intelligently, and provides utilities to check cluster connectivity, clean up unreachable contexts, deduplicate entries and revert from backups.

## Build and Development Commands

`Taskfile.yml` ([go-task](https://taskfile.dev)) is the single entry point for build, test and lint:

```bash
task setup        # fresh checkout: go mod download + pre-commit hook (runs `task check`); idempotent
task build        # go build -o kubeconfig-composer .
task run -- merge # go run . merge
task test         # go test ./...
task test:pkg -- ./pkg/composer   # tests of one package, verbose
task test:install # tests of install.sh against a local HTTP server (needs python3)
task lint:sh      # shellcheck for install.sh and scripts/ (skipped if shellcheck is missing)
task check        # fmt:check + vet + test + lint:sh + test:install; run before committing
task install      # go install .
task --list       # all tasks
```

## Architecture

### Entry Point
- `main.go` - Minimal entry point that delegates to the cmd package

### Command Structure (Cobra-based CLI)
All commands are in `cmd/` directory:
- `root.go` - Root command definition and Execute() function
- `merge.go` - Merges multiple kubeconfig files with conflict resolution. Defaults output to `~/.kube/config`; backs up an existing output file before overwriting; `--cleanup` moves source files to trash after a successful merge. Every merged cluster/user/context is named `<prefix>:<name>:<path-from---dir>` (prefix defaults to `kc`, `--prefix` configurable); a name that's a common placeholder (`local`, `user`, `default`, `kubernetes-admin`, etc. — see `isGenericName`) is replaced with the source filename instead of kept. **Idempotent:** an existing output file is loaded first as the merge base (its names win and are preserved) and excluded from the directory scan, the merged result is content-deduped (`Composer.Dedupe`) before writing, and `Composer.OutputUnchanged`/`ContextDiff` skip the write+prompt entirely (exit 0) when nothing actually changed
- `list.go` - Lists all kubeconfig files found in a directory
- `status.go` - Checks connectivity to clusters and displays status table
- `cleanup.go` - Removes unreachable contexts and optionally deletes empty files
- `revert.go` - Restores an output file (default `~/.kube/config`) from the most recent backup that differs from the current file; `--list` shows available backups
- `dedupe.go` - Collapses content-identical clusters, users, and contexts in a single kubeconfig (default `$KUBECONFIG`/`~/.kube/config`); `--dry-run` previews, `-y` skips confirmation. Backs up before writing. Core logic lives in `pkg/cleaner/dedupe.go`

### Core Packages

#### `pkg/composer`
Main merging logic:
- `Composer` struct manages the merge state with conflict tracking maps
- `ScanDirectory()` - Recursively finds kubeconfig files
- `LoadFiles()` - Loads specific kubeconfig files
- `Merge()` - Merges all loaded configs with conflict resolution
- `effectiveName()` - Builds every entity's `<prefix>:<name>:<path>` name (naming.go has `isGenericName`/placeholder detection); a name already starting with `<prefix>:` is left untouched (structural "already finalized" marker — required for idempotency since the current run's own path differs from the source path baked into a reloaded name)
- `getUniqueName()` - Rare fallback for genuine same-name/different-content collisions; numeric `_N` suffix normally, interactive prompt/`-source` suffix only for names not yet in `<prefix>:...` form

#### `pkg/scanner`
File scanning utilities:
- `IsKubeconfigFile()` - Identifies valid kubeconfig files by extension (.yaml, .yml), name (config, kubeconfig*), or content structure
- `FindKubeconfigFiles()` - Scans directory and returns list of valid kubeconfig files

#### `pkg/checker`
Cluster connectivity checking:
- `ClusterStatus` - Struct representing cluster connection status
- `CheckClusterStatus()` - Verifies cluster accessibility and retrieves version info
- `TruncateError()` - Formats error messages for display

#### `pkg/cleaner`
Kubeconfig cleanup utilities:
- `RemoveContextsFromFile()` - Removes specified contexts and unused clusters/users
- `WriteConfig()` - Safely writes kubeconfig to file with proper permissions
- `Dedupe()` (`dedupe.go`) - Collapses content-identical entries. Clusters/users are grouped by content via `reflect.DeepEqual` (with `LocationOfOrigin` cleared, since it only records the source file); contexts are grouped by their `(Cluster, AuthInfo)` target, **ignoring namespace** — namespace-only variants collapse and the non-empty namespace is kept. Canonical name is the shortest (ties broken alphabetically), except names referenced by `current-context` always win so the active context never breaks. Returns a `DedupeReport` of what merged.

### Conflict Resolution Strategy
When merging configs, the tool:
1. Processes clusters and users first, creating name mappings
2. Handles contexts last, using the mappings to update references
3. **Content-aware, never name-based:** equality is always resolved-content comparison, never a name-string comparison — `cleaner.ClustersEqual`/`UsersEqual` (ignore `LocationOfOrigin` and normalize empty-vs-nil `Extensions`), and `contextsSameContent` (resolves a context's `Cluster`/`AuthInfo` name to the actual object before comparing, so a cluster renamed between runs with unchanged content isn't a false conflict). A content match reuses the existing name silently — not a conflict. This applies in both passes: `detectConflicts` (the prompt preview) and `mergeConfig` (the actual merge)
4. Every entity name is built by `effectiveName()` as `<prefix>:<name-or-filename>:<path>` (see merge.go bullet above) — a genuine same-name/different-content collision is rare (only when two files resolve to the identical prefix:name:path) and falls back to a numeric `_N` suffix, or the old interactive prompt/`-source` suffix if the name was already finalized from a prior run
5. After merging, `Composer.Dedupe()` collapses any remaining content-identical entries that ended up under different names (the belt-and-suspenders step that makes re-runs idempotent)

### Installation and Releases
- `install.sh` (repo root) installs/updates the binary from GitHub Releases: picks the version (`KC_VERSION`, else `KC_CHANNEL=stable` → `releases/latest`, `alpha` → newest release including prereleases), verifies sha256 from `checksums.txt`, installs atomically into `KC_INSTALL_DIR` (default `~/.local/bin`), and exits early if `--version` of the installed binary already matches. It expects GoReleaser's default archive name `<project>_<version>_<os>_<arch>.tar.gz` — don't change the archive `name_template`.
- `install.sh` is attached to every release (`release.extra_files` and `checksum.extra_files` in `.goreleaser.yaml`); the README points at `releases/latest/download/install.sh`, which returns 404 until the first non-prerelease exists.
- `scripts/install_test.sh` tests it offline: `KC_API_URL`/`KC_DOWNLOAD_URL` point at a local `python3 -m http.server`.
- `--version` comes from `cmd.version`, injected by GoReleaser via ldflags; local builds report `dev`.

### State Directory (`~/.kubeconfig-composer/`)
The tool keeps its own working state outside the kube directory:
- `backups/` - `merge` copies the pre-existing output file here as `<name>.backup.<timestamp>` before overwriting. `revert` reads from here.
- `trash/` - `merge --cleanup` moves merged source files here as `<timestamp>.<name>` (uses `os.Rename`, never deletes outright), skipping the output file itself.

### Cluster Deduplication Cache
`status` and `cleanup` dedupe clusters before hitting the network: a cluster is keyed by `(server, CertificateAuthorityData)`, and the result is cached so identical clusters referenced by multiple contexts/files are only checked once. The cache is shared across goroutines and guarded by a 1-slot channel used as a mutex (`clusterCacheMutex <- struct{}{}` / `<-clusterCacheMutex`). This pattern appears inline in both `cmd/status.go` and `cmd/cleanup.go`.

### Kubernetes Client Integration
Uses `k8s.io/client-go` for:
- Loading and parsing kubeconfig files (`clientcmd.LoadFromFile`)
- Working with kubeconfig API types (`clientcmdapi.Config`)
- Creating Kubernetes clients for connectivity checks
- Serializing merged configs back to YAML

## Testing

Tests are located in `pkg/composer/composer_test.go` and `pkg/cleaner/dedupe_test.go`. When adding features, add corresponding test cases in the appropriate package test file.

## Common Development Patterns

### Adding a New Command
1. Create new file in `cmd/` directory (e.g., `cmd/newcommand.go`)
2. Define cobra.Command with Use, Short, Long, Example, and RunE
3. Add command to rootCmd in init() function
4. Define flags using command.Flags()
5. Implement the RunE function with error handling
6. Use existing packages (`pkg/scanner`, `pkg/checker`, `pkg/cleaner`) for common operations

### Working with Kubeconfig Files
- Always use `clientcmd.LoadFromFile()` to load configs
- Use `clientcmdapi.Config` type for in-memory representation
- Serialize with `clientcmd.Write()` before writing to disk
- Set file permissions to 0600 for security

### Error Handling
- Return errors with context using fmt.Errorf with %w for wrapping
- Print warnings to stderr using fmt.Fprintf(os.Stderr, ...)
- Use Russian language for user-facing messages (as per existing code)

## Dependencies

Key dependencies:
- `github.com/spf13/cobra` - CLI framework
- `k8s.io/client-go` - Kubernetes client library
- `k8s.io/apimachinery` - Kubernetes API types
- `gopkg.in/yaml.v3` - YAML parsing
- `github.com/pterm/pterm` - Colored terminal output and spinners (progress during `status`/`cleanup`)
- `github.com/olekukonko/tablewriter` - Renders the conflict/applied-changes tables in `merge`

## Notes

- User-facing messages are in Russian
- The tool protects `~/.kube/config` from deletion (only cleans contexts, never deletes the file)
- Interactive prompts are used for conflict resolution during merge
- Timeout defaults to 3 seconds for cluster connectivity checks
- **Rebuild before testing manually:** `task build` — the maintainer runs the compiled binary directly, not `go run .`; an unrebuilt binary after source changes looks identical to a real bug
- **`reflect.DeepEqual` gotcha:** after `clientcmd.Write`+`LoadFromFile`, `Extensions` becomes an empty non-nil map instead of `nil` — normalize (treat `len == 0` as equal) before any `reflect.DeepEqual` comparison of loaded-vs-in-memory kubeconfig objects
- **Testing `merge`'s interactive prompts non-interactively:** pipe `< /dev/null` (to hit the EOF/error path) or run in background with `sleep`+`kill` after the prompt appears — don't let it block

