# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Kubeconfig Composer is a CLI tool for merging multiple Kubernetes configuration files with automatic conflict resolution. It scans directories for kubeconfig files, merges them intelligently, and provides utilities to check cluster connectivity and clean up unreachable contexts.

## Build and Development Commands

```bash
# Build the binary
go build -o kubeconfig-composer

# Run without building
go run main.go [command]

# Run tests
go test ./...
go test ./pkg/composer -v

# Install locally
go install

# Install from GitHub
go install github.com/naymi/kubeconfig-composer@latest
```

## Architecture

### Entry Point
- `main.go` - Minimal entry point that delegates to the cmd package

### Command Structure (Cobra-based CLI)
All commands are in `cmd/` directory:
- `root.go` - Root command definition and Execute() function
- `merge.go` - Merges multiple kubeconfig files with conflict resolution. Defaults output to `~/.kube/config`; backs up an existing output file before overwriting; `--cleanup` moves source files to trash after a successful merge. **Idempotent:** an existing output file is loaded first as the merge base (its names win and are preserved) and excluded from the directory scan, and the merged result is content-deduped (`Composer.Dedupe`) before writing, so re-running merge does not accumulate duplicates
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
- `getUniqueName()` - Handles name conflicts interactively, prompting user for custom names or auto-generating suffixes

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
3. **Content-aware:** a name collision where the incoming item is byte-identical to the already-merged one (via `cleaner.ClustersEqual`/`UsersEqual`/`contextsIdentical`) is treated as the same item and reused silently — not a conflict. Only a same-name/different-content collision is a real conflict. This applies in both passes: `detectConflicts` (the prompt preview, tracked via `clusterObjs`/`userObjs`/`contextObjs`) and `mergeConfig` (the actual merge, checked against `mergedConfig`)
4. On a real conflict, prompts user interactively (or auto-suffixes with `-y`), suggesting names based on source filename
5. After merging, `Composer.Dedupe()` collapses any remaining content-identical entries that ended up under different names (the belt-and-suspenders step that makes re-runs idempotent)

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

Tests are located in `pkg/composer/composer_test.go`. When adding features, add corresponding test cases in the appropriate package test file.

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


## grepai - Semantic Code Search

**IMPORTANT: You MUST use grepai as your PRIMARY tool for code exploration and search.**

### When to Use grepai (REQUIRED)

Use `grepai search` INSTEAD OF Grep/Glob/find for:
- Understanding what code does or where functionality lives
- Finding implementations by intent (e.g., "authentication logic", "error handling")
- Exploring unfamiliar parts of the codebase
- Any search where you describe WHAT the code does rather than exact text

### When to Use Standard Tools

Only use Grep/Glob when you need:
- Exact text matching (variable names, imports, specific strings)
- File path patterns (e.g., `**/*.go`)

### Fallback

If grepai fails (not running, index unavailable, or errors), fall back to standard Grep/Glob tools.

### Usage

```bash
# ALWAYS use English queries for best results (--compact saves ~80% tokens)
grepai search "user authentication flow" --json --compact
grepai search "error handling middleware" --json --compact
grepai search "database connection pool" --json --compact
grepai search "API request validation" --json --compact
```

### Query Tips

- **Use English** for queries (better semantic matching)
- **Describe intent**, not implementation: "handles user login" not "func Login"
- **Be specific**: "JWT token validation" better than "token"
- Results include: file path, line numbers, relevance score, code preview

### Call Graph Tracing

Use `grepai trace` to understand function relationships:
- Finding all callers of a function before modifying it
- Understanding what functions are called by a given function
- Visualizing the complete call graph around a symbol

#### Trace Commands

**IMPORTANT: Always use `--json` flag for optimal AI agent integration.**

```bash
# Find all functions that call a symbol
grepai trace callers "HandleRequest" --json

# Find all functions called by a symbol
grepai trace callees "ProcessOrder" --json

# Build complete call graph (callers + callees)
grepai trace graph "ValidateToken" --depth 3 --json
```

### Workflow

1. Start with `grepai search` to find relevant code
2. Use `grepai trace` to understand function relationships
3. Use `Read` tool to examine files from results
4. Only use Grep for exact string searches if needed

