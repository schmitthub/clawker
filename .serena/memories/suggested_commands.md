# Development Commands

## Build
```bash
go build ./...                    # Build all packages
go build -o bin/claucker .        # Build CLI binary
```

## Test
```bash
go test ./...                     # Run all tests
go test ./... -short              # Skip integration tests (require Docker)
go test -v ./internal/config/...  # Test specific package
```

## Run
```bash
./bin/claucker --debug up         # Run with debug logging
./bin/claucker init               # Initialize project
./bin/claucker up                 # Start container
./bin/claucker down               # Stop container
./bin/claucker sh                 # Shell into container
```

## Lint/Format
```bash
go fmt ./...                      # Format code
go vet ./...                      # Static analysis
```
