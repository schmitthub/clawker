# Docs Package

CLI documentation generation in multiple formats from Cobra commands.

## Formats

| Function | Output |
|----------|--------|
| `GenMarkdownTree` / `GenMarkdownCustom` | Markdown docs |
| `GenManTree` / `GenMan` | Man pages |
| `GenReSTTree` / `GenReST` | reStructuredText |
| `GenYamlTree` / `GenYaml` | YAML (structured) |

## Key Types

```go
type GenManHeader struct {
    Title, Section, Date, Source, Manual string
}

type CommandDoc struct {  // YAML output structure
    Name, Synopsis, Description, Usage string
    Aliases []string
    Options, InheritedOptions []OptionDoc
    Commands []CommandDoc
    Examples, SeeAlso []string
}
```

## Usage

Called by `cmd/gen-docs` to regenerate CLI documentation:

```bash
go run ./cmd/gen-docs --doc-path docs --markdown
```
