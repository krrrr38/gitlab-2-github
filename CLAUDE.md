# CLAUDE.md - Go Project Guidelines

## Build & Run Commands
```bash
# Build the project
go build

# Run the application
go run main.go

# Run tests (when added)
go test ./...
go test -v ./path/to/specific/package
go test -run TestFunctionName ./path/to/package

# Format code
go fmt ./...

# Lint code (install golangci-lint first)
golangci-lint run
```

## Code Style Guidelines
- **Imports**: Group imports by standard library, third-party, and local packages
- **Formatting**: Follow Go standard formatting with `go fmt`
- **Error Handling**: Always check returned errors; use wrapped errors with context
- **Naming**: Use CamelCase; exported functions/variables start with uppercase
- **Comments**: Document all exported functions, types, and constants
- **Types**: Use strong typing; avoid interface{} when possible
- **Testing**: Write table-driven tests for functions
- **Package Structure**: Organize code by domain functionality
- **Dependency Management**: Use go modules for dependencies

This project uses Cobra for CLI commands and interfaces with both GitLab and GitHub APIs.