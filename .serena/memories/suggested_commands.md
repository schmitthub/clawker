# Development Commands

## Build
```bash
go build ./...                    # Build all packages
go build -o bin/claucker ./cmd/claucker  # Build CLI binary
```

## Test
```bash
go test ./...                     # Run all tests
go test ./... -short              # Skip integration tests (require Docker)
go test -v ./internal/engine/...  # Test specific package
```

## Run CLI
```bash
./bin/claucker --debug start      # Start with debug logging
./bin/claucker init               # Initialize project
./bin/claucker start              # Start container (random agent name)
./bin/claucker start --agent ralph  # Start with named agent
./bin/claucker run -- npm test    # Run ephemeral command
./bin/claucker stop               # Stop all containers
./bin/claucker stop --agent ralph # Stop specific agent
./bin/claucker ls                 # List running containers
./bin/claucker ls -a              # List all containers (incl. stopped)
./bin/claucker rm -p myproject    # Remove all project containers
./bin/claucker sh --agent ralph   # Shell into specific container
./bin/claucker logs -f            # Follow logs
```

## Lint/Format
```bash
go fmt ./...                      # Format code
go vet ./...                      # Static analysis
```
