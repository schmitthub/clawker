package docs

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestGenYaml(t *testing.T) {
	rootCmd := newTestRootCmd()
	containerCmd, _, _ := rootCmd.Find([]string{"container"})
	require.NotNil(t, containerCmd)

	buf := new(bytes.Buffer)
	err := GenYaml(containerCmd, buf)
	require.NoError(t, err)

	// Parse the YAML output
	var doc CommandDoc
	err = yaml.Unmarshal(buf.Bytes(), &doc)
	require.NoError(t, err)

	// Check basic fields
	assert.Equal(t, "clawker container", doc.Name)
	assert.Equal(t, "Manage containers", doc.Synopsis)
	assert.Contains(t, doc.Description, "Manage clawker containers")

	// Check aliases
	assert.Equal(t, []string{"c"}, doc.Aliases)

	// Check subcommands
	require.Len(t, doc.Commands, 3) // list, start, stop
	commandNames := make([]string, len(doc.Commands))
	for i, c := range doc.Commands {
		commandNames[i] = c.Name
	}
	assert.Contains(t, commandNames, "list")
	assert.Contains(t, commandNames, "start")
	assert.Contains(t, commandNames, "stop")

	// Check see also
	assert.Contains(t, doc.SeeAlso, "clawker")
}

func TestGenYaml_WithFlags(t *testing.T) {
	rootCmd := newTestRootCmd()
	listCmd, _, _ := rootCmd.Find([]string{"container", "list"})
	require.NotNil(t, listCmd)

	buf := new(bytes.Buffer)
	err := GenYaml(listCmd, buf)
	require.NoError(t, err)

	var doc CommandDoc
	err = yaml.Unmarshal(buf.Bytes(), &doc)
	require.NoError(t, err)

	// Check that options include our defined flags (may include help flag added by Cobra)
	require.GreaterOrEqual(t, len(doc.Options), 2) // --all, --quiet (and possibly --help)

	// Find --all flag
	var allFlag *OptionDoc
	for i := range doc.Options {
		if doc.Options[i].Name == "all" {
			allFlag = &doc.Options[i]
			break
		}
	}
	require.NotNil(t, allFlag)
	assert.Equal(t, "a", allFlag.Shorthand)
	assert.Equal(t, "Show all containers (default shows just running)", allFlag.Usage)
	assert.Equal(t, "bool", allFlag.Type)

	// Check inherited options from parent
	assert.GreaterOrEqual(t, len(doc.InheritedOptions), 2) // --debug, --config
}

func TestGenYaml_WithExamples(t *testing.T) {
	rootCmd := newTestRootCmd()
	listCmd, _, _ := rootCmd.Find([]string{"container", "list"})
	require.NotNil(t, listCmd)

	buf := new(bytes.Buffer)
	err := GenYaml(listCmd, buf)
	require.NoError(t, err)

	var doc CommandDoc
	err = yaml.Unmarshal(buf.Bytes(), &doc)
	require.NoError(t, err)

	// Check examples
	assert.Contains(t, doc.Examples, "clawker container list")
	assert.Contains(t, doc.Examples, "clawker container list --all")
}

func TestGenYaml_UsageLine(t *testing.T) {
	// Create a runnable command with positional args to test usage line
	root := &cobra.Command{Use: "clawker"}
	start := &cobra.Command{
		Use:   "start [CONTAINER]",
		Short: "Start a container",
		RunE:  func(cmd *cobra.Command, args []string) error { return nil },
	}
	root.AddCommand(start)

	buf := new(bytes.Buffer)
	err := GenYaml(start, buf)
	require.NoError(t, err)

	var doc CommandDoc
	err = yaml.Unmarshal(buf.Bytes(), &doc)
	require.NoError(t, err)

	// Check usage includes positional args
	assert.Contains(t, doc.Usage, "[CONTAINER]")
}

func TestGenYamlTree(t *testing.T) {
	rootCmd := newTestRootCmd()
	dir := t.TempDir()

	err := GenYamlTree(rootCmd, dir)
	require.NoError(t, err)

	// Verify root file exists
	_, err = os.Stat(filepath.Join(dir, "clawker.yaml"))
	require.NoError(t, err)

	// Verify container command file exists
	_, err = os.Stat(filepath.Join(dir, "clawker_container.yaml"))
	require.NoError(t, err)

	// Verify container subcommand files exist
	_, err = os.Stat(filepath.Join(dir, "clawker_container_list.yaml"))
	require.NoError(t, err)

	// Verify hidden command was NOT generated
	_, err = os.Stat(filepath.Join(dir, "clawker_hidden.yaml"))
	assert.True(t, os.IsNotExist(err), "hidden command should not generate YAML docs")
}

func TestGenYamlTreeCustom(t *testing.T) {
	rootCmd := newTestRootCmd()
	dir := t.TempDir()

	// Custom filename function
	filenameFunc := func(cmd *cobra.Command) string {
		return cmd.Name() + "_doc.yaml"
	}

	err := GenYamlTreeCustom(rootCmd, dir, filenameFunc)
	require.NoError(t, err)

	// Verify files use custom naming
	_, err = os.Stat(filepath.Join(dir, "clawker_doc.yaml"))
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(dir, "container_doc.yaml"))
	require.NoError(t, err)
}

func TestGenYamlCustom(t *testing.T) {
	rootCmd := newTestRootCmd()
	containerCmd, _, _ := rootCmd.Find([]string{"container"})
	require.NotNil(t, containerCmd)

	buf := new(bytes.Buffer)
	customizer := func(doc *CommandDoc) {
		doc.Description = "Custom description"
	}

	err := GenYamlCustom(containerCmd, buf, customizer)
	require.NoError(t, err)

	var doc CommandDoc
	err = yaml.Unmarshal(buf.Bytes(), &doc)
	require.NoError(t, err)

	assert.Equal(t, "Custom description", doc.Description)
}

func TestYamlFilename(t *testing.T) {
	t.Run("root command", func(t *testing.T) {
		cmd := &cobra.Command{Use: "clawker"}
		assert.Equal(t, "clawker.yaml", yamlFilename(cmd))
	})

	t.Run("subcommand", func(t *testing.T) {
		root := &cobra.Command{Use: "clawker"}
		container := &cobra.Command{Use: "container"}
		root.AddCommand(container)
		assert.Equal(t, "clawker_container.yaml", yamlFilename(container))
	})

	t.Run("nested subcommand", func(t *testing.T) {
		root := &cobra.Command{Use: "clawker"}
		container := &cobra.Command{Use: "container"}
		list := &cobra.Command{Use: "list"}
		root.AddCommand(container)
		container.AddCommand(list)
		assert.Equal(t, "clawker_container_list.yaml", yamlFilename(list))
	})
}
