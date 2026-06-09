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

## Config Doc Generation (configdoc.go)

Generates the Mintlify configuration reference page from the live `storage.Schema` — single source of truth for config field metadata across projects and settings.

- `GenConfigDoc(w, tmplContent)` — executes a Go template against `ConfigDocData` (assembled from `internal/config` project + settings schemas) and writes the rendered MDX to `w`
- `ConfigDocData`, `ConfigSection`, `ConfigGroup`, `ConfigField` — template data model (schema → sections → groups → fields)
- Helpers: `buildSections`, `toConfigField`, `kindToType`, `renderFieldTable`, `renderYAMLSchema`, `renderStructSliceElement` — reflection-based rendering driven by `yaml`/`label`/`desc`/`default`/`required` struct tags
- `escapeMDX(s)` — MDX-safe escaping for bare `<word>` angle brackets in descriptions

Consumers: `cmd/gen-docs` writes `docs/configuration.mdx` from a template pipeline that calls `GenConfigDoc`.

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

`configdoc_test.go`, `docs_test.go`, `man_test.go`, `markdown_test.go`, `rst_test.go`, `yaml_test.go` — format-specific output tests.
