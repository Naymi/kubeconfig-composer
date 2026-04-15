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
- `merge.go` - Merges multiple kubeconfig files with conflict resolution
- `list.go` - Lists all kubeconfig files found in a directory
- `status.go` - Checks connectivity to clusters and displays status table
- `cleanup.go` - Removes unreachable contexts and optionally deletes empty files

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

### Conflict Resolution Strategy
When merging configs, the tool:
1. Processes clusters and users first, creating name mappings
2. Handles contexts last, using the mappings to update references
3. On name conflicts, prompts user interactively with context about both conflicting items
4. Suggests auto-generated names based on source filename
5. Tracks all names across clusters, users, and contexts separately

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

## Notes

- User-facing messages are in Russian
- The tool protects `~/.kube/config` from deletion (only cleans contexts, never deletes the file)
- Interactive prompts are used for conflict resolution during merge
- Timeout defaults to 3 seconds for cluster connectivity checks
