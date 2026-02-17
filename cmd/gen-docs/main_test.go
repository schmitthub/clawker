package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRun(t *testing.T) {
	dir := t.TempDir()

	args := []string{
		"gen-docs",
		"--doc-path", dir,
		"--markdown",
		"--man-page",
		"--website",
	}

	err := run(args)
	require.NoError(t, err)

	// Verify man page generated
	manFiles, err := filepath.Glob(filepath.Join(dir, "man", "*.1"))
	require.NoError(t, err)
	require.NotEmpty(t, manFiles, "should have generated man pages")

	// Pick a known man page to verify content
	manContent, err := os.ReadFile(filepath.Join(dir, "man", "clawker-container-run.1"))
	require.NoError(t, err)
	require.Contains(t, string(manContent), `\fBclawker container run`)

	// Verify markdown with Mintlify front matter
	mdContent, err := os.ReadFile(filepath.Join(dir, "cli-reference", "clawker_container_run.md"))
	require.NoError(t, err)
	require.Contains(t, string(mdContent), "## clawker container run")
	require.Contains(t, string(mdContent), `title: "clawker container run"`)
}

func TestRunValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "missing doc-path",
			args:    []string{"gen-docs", "--markdown"},
			wantErr: "--doc-path is required",
		},
		{
			name:    "no format specified",
			args:    []string{"gen-docs", "--doc-path", t.TempDir()},
			wantErr: "at least one format must be specified",
		},
		{
			name:    "website without markdown",
			args:    []string{"gen-docs", "--doc-path", t.TempDir(), "--website", "--yaml"},
			wantErr: "--website requires --markdown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(tt.args)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestRunAllFormats(t *testing.T) {
	dir := t.TempDir()

	args := []string{
		"gen-docs",
		"--doc-path", dir,
		"--markdown",
		"--man-page",
		"--yaml",
		"--rst",
	}

	err := run(args)
	require.NoError(t, err)

	// Verify all format directories were created with files
	formats := []struct {
		dir      string
		fileGlob string
	}{
		{"cli-reference", "*.md"},
		{"man", "*.1"},
		{"yaml", "*.yaml"},
		{"rst", "*.rst"},
	}

	for _, fmt := range formats {
		t.Run(fmt.dir, func(t *testing.T) {
			formatDir := filepath.Join(dir, fmt.dir)
			_, err := os.Stat(formatDir)
			require.NoError(t, err, "%s directory should exist", fmt.dir)

			files, err := filepath.Glob(filepath.Join(formatDir, fmt.fileGlob))
			require.NoError(t, err)
			require.NotEmpty(t, files, "should have generated %s files", fmt.dir)
		})
	}
}

func TestMintlifyFilePrepender(t *testing.T) {
	tests := []struct {
		name      string
		filename  string
		wantTitle string
	}{
		{
			name:      "root command",
			filename:  "/docs/clawker.md",
			wantTitle: `title: "clawker"`,
		},
		{
			name:      "subcommand",
			filename:  "/docs/clawker_container.md",
			wantTitle: `title: "clawker container"`,
		},
		{
			name:      "deep subcommand",
			filename:  "/docs/clawker_container_run.md",
			wantTitle: `title: "clawker container run"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mintlifyFilePrepender(tt.filename)

			require.Contains(t, result, "---")
			require.Contains(t, result, tt.wantTitle)
		})
	}
}

func TestMintlifyLinkHandler(t *testing.T) {
	tests := []struct {
		name    string
		cmdPath string
		want    string
	}{
		{
			name:    "root command",
			cmdPath: "clawker",
			want:    "clawker",
		},
		{
			name:    "subcommand",
			cmdPath: "clawker container",
			want:    "clawker_container",
		},
		{
			name:    "deep subcommand",
			cmdPath: "clawker container run",
			want:    "clawker_container_run",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mintlifyLinkHandler(tt.cmdPath)
			require.Equal(t, tt.want, result)
		})
	}
}

func TestRunMarkdownOnly(t *testing.T) {
	dir := t.TempDir()

	args := []string{
		"gen-docs",
		"--doc-path", dir,
		"--markdown",
	}

	err := run(args)
	require.NoError(t, err)

	// Verify cli-reference directory was created
	cliRefDir := filepath.Join(dir, "cli-reference")
	_, err = os.Stat(cliRefDir)
	require.NoError(t, err, "cli-reference directory should exist")

	// Verify at least the root command file was created
	rootFile := filepath.Join(cliRefDir, "clawker.md")
	_, err = os.Stat(rootFile)
	require.NoError(t, err, "clawker.md should exist")

	// Verify content has expected structure (no front matter)
	content, err := os.ReadFile(rootFile)
	require.NoError(t, err)
	require.Contains(t, string(content), "## clawker")
	require.False(t, strings.HasPrefix(string(content), "---"), "should not have front matter without --website")
}

func TestRunWebsite(t *testing.T) {
	dir := t.TempDir()

	args := []string{
		"gen-docs",
		"--doc-path", dir,
		"--markdown",
		"--website",
	}

	err := run(args)
	require.NoError(t, err)

	// Verify Mintlify front matter in generated files
	rootFile := filepath.Join(dir, "cli-reference", "clawker.md")
	content, err := os.ReadFile(rootFile)
	require.NoError(t, err)

	contentStr := string(content)
	require.True(t, strings.HasPrefix(contentStr, "---"), "should start with Mintlify front matter")
	require.Contains(t, contentStr, `title: "clawker"`)
}
