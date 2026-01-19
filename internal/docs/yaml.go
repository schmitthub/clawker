package docs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"
)

// CommandDoc represents YAML documentation structure for a command.
type CommandDoc struct {
	Name             string       `yaml:"name"`
	Synopsis         string       `yaml:"synopsis,omitempty"`
	Description      string       `yaml:"description,omitempty"`
	Usage            string       `yaml:"usage,omitempty"`
	Aliases          []string     `yaml:"aliases,omitempty"`
	Options          []OptionDoc  `yaml:"options,omitempty"`
	InheritedOptions []OptionDoc  `yaml:"inherited_options,omitempty"`
	Commands         []CommandDoc `yaml:"commands,omitempty"`
	Examples         string       `yaml:"examples,omitempty"`
	SeeAlso          []string     `yaml:"see_also,omitempty"`
}

// OptionDoc represents YAML documentation for a command flag.
type OptionDoc struct {
	Name         string `yaml:"name"`
	Shorthand    string `yaml:"shorthand,omitempty"`
	DefaultValue string `yaml:"default_value,omitempty"`
	Usage        string `yaml:"usage"`
	Type         string `yaml:"type,omitempty"`
}

// GenYamlTree generates YAML documentation for a command and all its subcommands.
// Each command is written to a separate file.
func GenYamlTree(cmd *cobra.Command, dir string) error {
	for _, c := range cmd.Commands() {
		if c.Hidden {
			continue
		}
		if err := GenYamlTree(c, dir); err != nil {
			return err
		}
	}

	filename := filepath.Join(dir, yamlFilename(cmd))
	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", filename, err)
	}
	defer f.Close()

	return GenYaml(cmd, f)
}

// GenYamlTreeCustom generates YAML documentation with a custom filename function.
func GenYamlTreeCustom(cmd *cobra.Command, dir string, filenameFunc func(*cobra.Command) string) error {
	for _, c := range cmd.Commands() {
		if c.Hidden {
			continue
		}
		if err := GenYamlTreeCustom(c, dir, filenameFunc); err != nil {
			return err
		}
	}

	filename := filepath.Join(dir, filenameFunc(cmd))
	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", filename, err)
	}
	defer f.Close()

	return GenYaml(cmd, f)
}

// GenYaml generates YAML documentation for a single command.
func GenYaml(cmd *cobra.Command, w io.Writer) error {
	cmd.InitDefaultHelpCmd()
	cmd.InitDefaultHelpFlag()

	doc := buildCommandDoc(cmd)

	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	return enc.Encode(doc)
}

// GenYamlCustom generates YAML documentation with custom processing.
func GenYamlCustom(cmd *cobra.Command, w io.Writer, customizer func(*CommandDoc)) error {
	cmd.InitDefaultHelpCmd()
	cmd.InitDefaultHelpFlag()

	doc := buildCommandDoc(cmd)
	if customizer != nil {
		customizer(&doc)
	}

	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	return enc.Encode(doc)
}

func buildCommandDoc(cmd *cobra.Command) CommandDoc {
	doc := CommandDoc{
		Name:        cmd.CommandPath(),
		Synopsis:    cmd.Short,
		Description: cmd.Long,
		Examples:    cmd.Example,
	}

	if cmd.Runnable() {
		doc.Usage = cmd.UseLine()
	}

	if len(cmd.Aliases) > 0 {
		doc.Aliases = cmd.Aliases
	}

	// Options
	doc.Options = collectFlags(cmd.NonInheritedFlags())
	doc.InheritedOptions = collectFlags(cmd.InheritedFlags())

	// Subcommands (only names, not full docs)
	for _, c := range getNonHiddenCommands(cmd) {
		subDoc := CommandDoc{
			Name:     c.Name(),
			Synopsis: c.Short,
		}
		doc.Commands = append(doc.Commands, subDoc)
	}

	// See also
	if cmd.HasParent() {
		doc.SeeAlso = append(doc.SeeAlso, cmd.Parent().CommandPath())
	}
	for _, c := range getNonHiddenCommands(cmd) {
		doc.SeeAlso = append(doc.SeeAlso, c.CommandPath())
	}

	return doc
}

func collectFlags(fs *pflag.FlagSet) []OptionDoc {
	var opts []OptionDoc

	fs.VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		opt := OptionDoc{
			Name:         f.Name,
			Shorthand:    f.Shorthand,
			DefaultValue: f.DefValue,
			Usage:        f.Usage,
			Type:         f.Value.Type(),
		}
		// Don't include empty default values for cleaner YAML
		if opt.DefaultValue == "" || opt.DefaultValue == "false" || opt.DefaultValue == "0" || opt.DefaultValue == "[]" {
			opt.DefaultValue = ""
		}
		opts = append(opts, opt)
	})

	// Sort by name for consistent output
	sort.Slice(opts, func(i, j int) bool {
		return opts[i].Name < opts[j].Name
	})

	return opts
}

func yamlFilename(cmd *cobra.Command) string {
	return strings.ReplaceAll(cmd.CommandPath(), " ", "_") + ".yaml"
}
