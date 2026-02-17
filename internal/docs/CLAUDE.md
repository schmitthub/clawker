# Docs Package

CLI documentation generation in multiple formats from Cobra commands.

## Exported Types

```go
type GenManHeader struct {
    Title, Section, Date, Source, Manual string
}

type GenManTreeOptions struct {
    Path             string
    CommandSeparator string
    Header           *GenManHeader
}

type CommandDoc struct {  // YAML output structure
    Name, Synopsis, Description, Usage string
    Aliases, Examples, SeeAlso         []string
    Options, InheritedOptions          []OptionDoc
    Commands                           []CommandDoc
}

type OptionDoc struct {
    Name, Shorthand, DefaultValue, Usage, Type string
}
```

## Markdown Generation (markdown.go)

- `GenMarkdownTree(cmd, dir)` — write markdown files for cmd tree to dir
- `GenMarkdownTreeCustom(cmd, dir, filePrepender, linkHandler)` — with custom prepender/link callbacks
- `GenMarkdown(cmd, w)` — write single command markdown to writer
- `GenMarkdownCustom(cmd, w, linkHandler)` — single command with custom link handler
- `GenMarkdownTreeWebsite(cmd, dir, filePrepender, linkHandler)` — like `GenMarkdownTreeCustom` but produces MDX-safe output via `GenMarkdownWebsite`
- `GenMarkdownWebsite(cmd, w, linkHandler)` — single command markdown with `EscapeMDXProse` applied to description/long/example text
- `EscapeMDXProse(s)` — escapes bare `<word>` angle brackets to `` `<word>` `` so MDX parsers don't treat them as JSX tags

## Man Page Generation (man.go)

- `GenManTree(cmd, dir)` — write man pages for cmd tree to dir
- `GenManTreeFromOpts(cmd, GenManTreeOptions)` — with custom options (path, separator, header)
- `GenMan(cmd, *GenManHeader, w)` — write single command man page to writer

## reStructuredText Generation (rst.go)

- `GenReSTTree(cmd, dir)` — write RST files for cmd tree to dir
- `GenReSTTreeCustom(cmd, dir, filePrepender, linkHandler)` — with custom prepender/link callbacks
- `GenReST(cmd, w)` — write single command RST to writer
- `GenReSTCustom(cmd, w, linkHandler)` — single command with custom link handler

## YAML Generation (yaml.go)

- `GenYamlTree(cmd, dir)` — write YAML files for cmd tree to dir
- `GenYamlTreeCustom(cmd, dir, filenameFunc)` — with custom filename function
- `GenYaml(cmd, w)` — write single command YAML to writer
- `GenYamlCustom(cmd, w, customizer)` — single command with `func(*CommandDoc)` customizer

## Usage

Called by `cmd/gen-docs` to regenerate CLI documentation:

```bash
go run ./cmd/gen-docs --doc-path docs --markdown            # Standard markdown
go run ./cmd/gen-docs --doc-path docs --markdown --website   # Mintlify-safe (MDX-escaped + frontmatter)
```

## Tests

`docs_test.go`, `man_test.go`, `markdown_test.go`, `rst_test.go`, `yaml_test.go` — format-specific output tests.
