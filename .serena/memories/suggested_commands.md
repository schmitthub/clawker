# Development Commands

## Build

```bash
go build ./...                    # Build all packages
go build -o bin/clawker ./cmd/clawker  # Build CLI binary
```

## Test

```bash
go test ./...                     # Run all tests
go test ./... -short              # Skip integration tests (require Docker)
go test -v ./internal/engine/...  # Test specific package
```

## Run CLI

```bash
./bin/clawker --debug start      # Start with debug logging
./bin/clawker init               # Initialize project
./bin/clawker start              # Start container (random agent name)
./bin/clawker start --agent ralph  # Start with named agent
./bin/clawker run -- npm test    # Run ephemeral command
./bin/clawker stop               # Stop all containers
./bin/clawker stop --agent ralph # Stop specific agent
./bin/clawker ls                 # List running containers
./bin/clawker ls -a              # List all containers (incl. stopped)
./bin/clawker rm -p myproject    # Remove all project containers
./bin/clawker sh --agent ralph   # Shell into specific container
./bin/clawker logs -f            # Follow logs
./bin/clawker generate latest    # Generate versions.json for latest
./bin/clawker generate latest 2.1 # Generate multiple version patterns
```

## Lint/Format

```bash
go fmt ./...                      # Format code
go vet ./...                      # Static analysis
```
