package root

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/google/shlex"
	"github.com/schmitthub/clawker/internal/cmd/alias/shared"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/spf13/cobra"
)

// placeholderPattern matches $1..$N positional placeholders in an alias expansion.
var placeholderPattern = regexp.MustCompile(`\$(\d+)`)

// AnnotationAliasExpansion is the cobra annotation key carrying a user
// alias command's raw expansion string.
const AnnotationAliasExpansion = "alias-expansion"

// registerUserAliases registers user-configured aliases from the merged
// project config (all layers: walk-up files, the user config-dir file, and
// shipped defaults) as top-level commands. It must be called after every
// real command is
// registered, because existing commands always win name collisions.
//
// It never fails root construction: a nil Config closure (e.g. docs
// generation builds the tree with a bare Factory) or a config load error
// skips registration entirely — the load error resurfaces with full
// rendering as soon as any command that needs config runs.
func registerUserAliases(root *cobra.Command, f *cmdutil.Factory) {
	if f.Config == nil {
		return
	}
	log := rootLogger(f)
	cfg, err := f.Config()
	if err != nil {
		log.Debug().Err(err).Msg("user aliases skipped: config unavailable")
		return
	}

	aliases := cfg.Project().Aliases
	names := make([]string, 0, len(aliases))
	for name := range aliases {
		names = append(names, name)
	}
	slices.Sort(names)

	for _, name := range names {
		expansion := aliases[name]
		switch {
		case strings.TrimSpace(expansion) == "":
			// Nothing to execute — skip, like any other invalid entry.
			log.Debug().Str("alias", name).Msg("user alias skipped: empty expansion")
		case shared.ValidateName(name) != nil:
			// Match alias set/import write-time validation: rejects multi-word,
			// padded, and "-"-prefixed names. A padded key like "run " would
			// otherwise register a cobra command whose derived Name() collides
			// with the builtin and can shadow it at dispatch.
			log.Debug().Str("alias", name).Msg("user alias skipped: invalid name")
		case builtinCommandExists(root, name):
			log.Debug().Str("alias", name).Msg("user alias skipped: shadows an existing command")
		case aliasChainCyclic(name, aliases):
			log.Debug().Str("alias", name).Msg("user alias skipped: cyclic alias chain")
		default:
			root.AddCommand(newUserAliasCmd(name, expansion))
		}
	}
}

// newUserAliasCmd builds the cobra command for one user alias. Flag parsing
// is disabled so every argument — flags included — is forwarded verbatim
// into the expansion, which then re-executes the root command.
func newUserAliasCmd(name, expansion string) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: fmt.Sprintf("Alias for %q", expansion),
		Annotations: map[string]string{
			AnnotationAliasExpansion: expansion,
		},
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			expanded, err := expandAlias(expansion, args)
			if err != nil {
				return fmt.Errorf("alias %q: %w", name, err)
			}
			r := cmd.Root()
			r.SetArgs(expanded)
			return r.Execute()
		},
	}
}

// expandAlias substitutes $1..$N positional placeholders in expansion with
// args, splits the result into argv tokens, and appends any args beyond the
// highest placeholder index. Without placeholders, all args are appended.
func expandAlias(expansion string, args []string) ([]string, error) {
	maxIdx := 0
	if strings.Contains(expansion, "$") {
		for _, m := range placeholderPattern.FindAllStringSubmatch(expansion, -1) {
			i, err := strconv.Atoi(m[1])
			if err != nil || i < 1 {
				// $0 or an out-of-int-range index is not a placeholder; leave it literal.
				continue
			}
			maxIdx = max(maxIdx, i)
		}
		if maxIdx > len(args) {
			return nil, fmt.Errorf("not enough arguments: expansion references $%d but %d given", maxIdx, len(args))
		}
		// Substitute descending so $1 never clobbers the prefix of $10.
		for i := maxIdx; i >= 1; i-- {
			expansion = strings.ReplaceAll(expansion, fmt.Sprintf("$%d", i), args[i-1])
		}
	}

	expanded, err := shlex.Split(expansion)
	if err != nil {
		return nil, fmt.Errorf("invalid expansion %q: %w", expansion, err)
	}
	return append(expanded, args[maxIdx:]...), nil
}

// builtinCommandExists reports whether root has a real (non-user-alias)
// command answering to name, either directly or through a cobra alias.
// User alias commands (marked by AnnotationAliasExpansion) are excluded so
// that `alias set` can redefine an alias that is already registered.
// root.Find is unsuitable here: it prefix-matches and would treat any
// unknown name as root itself.
func builtinCommandExists(root *cobra.Command, name string) bool {
	for _, c := range root.Commands() {
		if _, isUserAlias := c.Annotations[AnnotationAliasExpansion]; isUserAlias {
			continue
		}
		if c.Name() == name || slices.Contains(c.Aliases, name) {
			return true
		}
	}
	return false
}

// aliasChainCyclic walks the chain of aliases starting at name, following
// each expansion's first token while it names another alias. A repeat
// visit means the chain can never reach a real command and would otherwise
// re-execute forever.
func aliasChainCyclic(name string, aliases map[string]string) bool {
	seen := make(map[string]bool)
	for cur := name; ; {
		if seen[cur] {
			return true
		}
		seen[cur] = true
		// Tokenize with shlex to match expandAlias — strings.Fields keeps
		// quote characters, letting a quoted cycle (a: `"b"`, b: `"a"`)
		// slip past detection while still dispatching at runtime.
		fields, err := shlex.Split(aliases[cur])
		if err != nil || len(fields) == 0 {
			// Unsplittable expansion can't execute as a chain; expandAlias
			// surfaces the same error at runtime.
			return false
		}
		next := fields[0]
		if _, ok := aliases[next]; !ok {
			return false
		}
		cur = next
	}
}

// rootLogger resolves the Factory logger, falling back to a no-op logger so
// registration paths never fail on logging.
func rootLogger(f *cmdutil.Factory) *logger.Logger {
	if f.Logger == nil {
		return logger.Nop()
	}
	log, err := f.Logger()
	if err != nil || log == nil {
		return logger.Nop()
	}
	return log
}
