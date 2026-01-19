package docs

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schmitthub/clawker/pkg/cmdutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestNewCmdDocs(t *testing.T) {
	f := cmdutil.New("1.0.0", "abc123")
	cmd := NewCmdDocs(f)

	require.Equal(t, "docs", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	require.NotEmpty(t, cmd.Long)
	require.NotEmpty(t, cmd.Example)
	require.NotNil(t, cmd.RunE)
}

func TestCmd_Flags(t *testing.T) {
	tests := []struct {
		name      string
		flag      string
		shorthand string
		defValue  string
	}{
		{"doc-path flag", "doc-path", "", ""},
		{"markdown flag", "markdown", "", "false"},
		{"man flag", "man", "", "false"},
		{"yaml flag", "yaml", "", "false"},
		{"rst flag", "rst", "", "false"},
		{"website flag", "website", "", "false"},
	}

	f := &cmdutil.Factory{}
	cmd := NewCmdDocs(f)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flag := cmd.Flags().Lookup(tt.flag)
			require.NotNil(t, flag, "flag --%s should exist", tt.flag)

			if tt.shorthand != "" {
				require.Equal(t, tt.shorthand, flag.Shorthand,
					"flag --%s should have shorthand -%s", tt.flag, tt.shorthand)
			}

			require.Equal(t, tt.defValue, flag.DefValue,
				"flag --%s should have default value %q", tt.flag, tt.defValue)
		})
	}
}

func TestCmd_FlagValidation(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantErr    bool
		errContain string
	}{
		{
			name:       "no format specified",
			args:       []string{"--doc-path", "/tmp/docs"},
			wantErr:    true,
			errContain: "no format specified",
		},
		{
			name:       "website without markdown",
			args:       []string{"--doc-path", "/tmp/docs", "--website"},
			wantErr:    true,
			errContain: "no format specified",
		},
		{
			name:       "website with only yaml",
			args:       []string{"--doc-path", "/tmp/docs", "--yaml", "--website"},
			wantErr:    true,
			errContain: "--website requires --markdown",
		},
		{
			name:    "markdown only",
			args:    []string{"--doc-path", t.TempDir(), "--markdown"},
			wantErr: false,
		},
		{
			name:    "markdown with website",
			args:    []string{"--doc-path", t.TempDir(), "--markdown", "--website"},
			wantErr: false,
		},
		{
			name:    "multiple formats",
			args:    []string{"--doc-path", t.TempDir(), "--markdown", "--man", "--yaml", "--rst"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{
				Version: "test",
				Commit:  "test",
			}
			cmd := NewCmdDocs(f)

			// Cobra hack-around for help flag
			cmd.Flags().BoolP("help", "x", false, "")

			cmd.SetArgs(tt.args)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err := cmd.ExecuteC()
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContain != "" {
					require.Contains(t, err.Error(), tt.errContain)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCmd_FlagValuePropagation(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		verify func(t *testing.T, opts *DocsOptions)
	}{
		{
			name: "doc-path value",
			args: []string{"--doc-path", "/tmp/test-docs", "--markdown"},
			verify: func(t *testing.T, opts *DocsOptions) {
				require.Equal(t, "/tmp/test-docs", opts.DocPath)
			},
		},
		{
			name: "markdown flag",
			args: []string{"--doc-path", "/tmp/docs", "--markdown"},
			verify: func(t *testing.T, opts *DocsOptions) {
				require.True(t, opts.Markdown)
				require.False(t, opts.Man)
				require.False(t, opts.YAML)
				require.False(t, opts.RST)
			},
		},
		{
			name: "man flag",
			args: []string{"--doc-path", "/tmp/docs", "--man"},
			verify: func(t *testing.T, opts *DocsOptions) {
				require.False(t, opts.Markdown)
				require.True(t, opts.Man)
			},
		},
		{
			name: "yaml flag",
			args: []string{"--doc-path", "/tmp/docs", "--yaml"},
			verify: func(t *testing.T, opts *DocsOptions) {
				require.True(t, opts.YAML)
			},
		},
		{
			name: "rst flag",
			args: []string{"--doc-path", "/tmp/docs", "--rst"},
			verify: func(t *testing.T, opts *DocsOptions) {
				require.True(t, opts.RST)
			},
		},
		{
			name: "website flag with markdown",
			args: []string{"--doc-path", "/tmp/docs", "--markdown", "--website"},
			verify: func(t *testing.T, opts *DocsOptions) {
				require.True(t, opts.Markdown)
				require.True(t, opts.Website)
			},
		},
		{
			name: "all formats",
			args: []string{"--doc-path", "/tmp/docs", "--markdown", "--man", "--yaml", "--rst"},
			verify: func(t *testing.T, opts *DocsOptions) {
				require.True(t, opts.Markdown)
				require.True(t, opts.Man)
				require.True(t, opts.YAML)
				require.True(t, opts.RST)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			var capturedOpts *DocsOptions
			cmd := NewCmdDocs(f)

			// Override RunE to capture options without actual execution
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				opts := &DocsOptions{
					DocPath:  cmd.Flags().Lookup("doc-path").Value.String(),
					Markdown: cmd.Flags().Lookup("markdown").Value.String() == "true",
					Man:      cmd.Flags().Lookup("man").Value.String() == "true",
					YAML:     cmd.Flags().Lookup("yaml").Value.String() == "true",
					RST:      cmd.Flags().Lookup("rst").Value.String() == "true",
					Website:  cmd.Flags().Lookup("website").Value.String() == "true",
				}
				capturedOpts = opts
				return nil
			}

			cmd.Flags().BoolP("help", "x", false, "")
			cmd.SetArgs(tt.args)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err := cmd.ExecuteC()
			require.NoError(t, err)
			require.NotNil(t, capturedOpts)

			tt.verify(t, capturedOpts)
		})
	}
}

func TestJekyllFilePrepender(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		wantPath string
		wantName string
	}{
		{
			name:     "root command",
			filename: "/docs/clawker.md",
			wantPath: "/cli/clawker/",
			wantName: "clawker",
		},
		{
			name:     "subcommand",
			filename: "/docs/clawker_container.md",
			wantPath: "/cli/clawker/container/",
			wantName: "clawker container",
		},
		{
			name:     "deep subcommand",
			filename: "/docs/clawker_container_run.md",
			wantPath: "/cli/clawker/container/run/",
			wantName: "clawker container run",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := jekyllFilePrepender(tt.filename)

			require.Contains(t, result, "---")
			require.Contains(t, result, "layout: manual")
			require.Contains(t, result, "permalink: "+tt.wantPath)
			require.Contains(t, result, "title: "+tt.wantName)
		})
	}
}

func TestJekyllLinkHandler(t *testing.T) {
	tests := []struct {
		name    string
		cmdPath string
		want    string
	}{
		{
			name:    "root command",
			cmdPath: "clawker",
			want:    "clawker.md",
		},
		{
			name:    "subcommand",
			cmdPath: "clawker container",
			want:    "clawker_container.md",
		},
		{
			name:    "deep subcommand",
			cmdPath: "clawker container run",
			want:    "clawker_container_run.md",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := jekyllLinkHandler(tt.cmdPath)
			require.Equal(t, tt.want, result)
		})
	}
}

func TestDocGeneration_Integration(t *testing.T) {
	// Create temp directory for output
	tmpDir := t.TempDir()

	f := &cmdutil.Factory{
		Version: "test-version",
		Commit:  "test-commit",
	}

	cmd := NewCmdDocs(f)
	cmd.SetArgs([]string{
		"--doc-path", tmpDir,
		"--markdown",
	})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	require.NoError(t, err)

	// Verify markdown directory was created
	markdownDir := filepath.Join(tmpDir, "markdown")
	_, err = os.Stat(markdownDir)
	require.NoError(t, err, "markdown directory should exist")

	// Verify at least the root command file was created
	rootFile := filepath.Join(markdownDir, "clawker.md")
	_, err = os.Stat(rootFile)
	require.NoError(t, err, "clawker.md should exist")

	// Verify content has expected structure
	content, err := os.ReadFile(rootFile)
	require.NoError(t, err)
	require.Contains(t, string(content), "## clawker")
}

func TestDocGeneration_AllFormats(t *testing.T) {
	tmpDir := t.TempDir()

	f := &cmdutil.Factory{
		Version: "test-version",
		Commit:  "test-commit",
	}

	cmd := NewCmdDocs(f)
	cmd.SetArgs([]string{
		"--doc-path", tmpDir,
		"--markdown",
		"--man",
		"--yaml",
		"--rst",
	})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	require.NoError(t, err)

	// Verify all format directories were created
	formats := []struct {
		dir      string
		fileGlob string
	}{
		{"markdown", "*.md"},
		{"man", "*.1"},
		{"yaml", "*.yaml"},
		{"rst", "*.rst"},
	}

	for _, fmt := range formats {
		t.Run(fmt.dir, func(t *testing.T) {
			dir := filepath.Join(tmpDir, fmt.dir)
			_, err := os.Stat(dir)
			require.NoError(t, err, "%s directory should exist", fmt.dir)

			// Verify files were generated
			files, err := filepath.Glob(filepath.Join(dir, fmt.fileGlob))
			require.NoError(t, err)
			require.NotEmpty(t, files, "should have generated %s files", fmt.dir)
		})
	}
}

func TestDocGeneration_JekyllWebsite(t *testing.T) {
	tmpDir := t.TempDir()

	f := &cmdutil.Factory{
		Version: "test-version",
		Commit:  "test-commit",
	}

	cmd := NewCmdDocs(f)
	cmd.SetArgs([]string{
		"--doc-path", tmpDir,
		"--markdown",
		"--website",
	})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	require.NoError(t, err)

	// Verify Jekyll front matter in generated files
	rootFile := filepath.Join(tmpDir, "markdown", "clawker.md")
	content, err := os.ReadFile(rootFile)
	require.NoError(t, err)

	// Check for Jekyll front matter
	contentStr := string(content)
	require.True(t, strings.HasPrefix(contentStr, "---"), "should start with Jekyll front matter")
	require.Contains(t, contentStr, "layout: manual")
	require.Contains(t, contentStr, "permalink:")
	require.Contains(t, contentStr, "title: clawker")
}
