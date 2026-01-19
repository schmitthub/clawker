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

	// Verify markdown with Jekyll front matter
	mdContent, err := os.ReadFile(filepath.Join(dir, "markdown", "clawker_container_run.md"))
	require.NoError(t, err)
	require.Contains(t, string(mdContent), "## clawker container run")
	require.Contains(t, string(mdContent), "layout: manual")
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
		{"markdown", "*.md"},
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

func TestRunMarkdownOnly(t *testing.T) {
	dir := t.TempDir()

	args := []string{
		"gen-docs",
		"--doc-path", dir,
		"--markdown",
	}

	err := run(args)
	require.NoError(t, err)

	// Verify markdown directory was created
	markdownDir := filepath.Join(dir, "markdown")
	_, err = os.Stat(markdownDir)
	require.NoError(t, err, "markdown directory should exist")

	// Verify at least the root command file was created
	rootFile := filepath.Join(markdownDir, "clawker.md")
	_, err = os.Stat(rootFile)
	require.NoError(t, err, "clawker.md should exist")

	// Verify content has expected structure (no Jekyll front matter)
	content, err := os.ReadFile(rootFile)
	require.NoError(t, err)
	require.Contains(t, string(content), "## clawker")
	// Should NOT have Jekyll front matter
	require.False(t, strings.HasPrefix(string(content), "---"), "should not have Jekyll front matter without --website")
}

func TestRunJekyllWebsite(t *testing.T) {
	dir := t.TempDir()

	args := []string{
		"gen-docs",
		"--doc-path", dir,
		"--markdown",
		"--website",
	}

	err := run(args)
	require.NoError(t, err)

	// Verify Jekyll front matter in generated files
	rootFile := filepath.Join(dir, "markdown", "clawker.md")
	content, err := os.ReadFile(rootFile)
	require.NoError(t, err)

	contentStr := string(content)
	require.True(t, strings.HasPrefix(contentStr, "---"), "should start with Jekyll front matter")
	require.Contains(t, contentStr, "layout: manual")
	require.Contains(t, contentStr, "permalink:")
	require.Contains(t, contentStr, "title: clawker")
}
