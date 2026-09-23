// Package licenses implements the "licenses" command. Release builds embed the
// third-party license texts for their own platform; see scripts/licenses.sh.
package licenses

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/schmitthub/clawker/internal/cmdutil"
)

const placeholder = "License information is only available in official release builds.\n"

const separator = "================================================================================\n"

// NewCmdLicenses creates the "licenses" command.
func NewCmdLicenses(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "licenses",
		Short: "View third-party license information",
		Long:  "View license information for third-party libraries used in this build of clawker.",
		Example: `  # Show third-party licenses
  clawker licenses`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			text, err := content(embedFS, rootDir)
			if err != nil {
				return err
			}
			ios := f.IOStreams
			if pagerErr := ios.StartPager(); pagerErr == nil {
				defer ios.StopPager()
			}
			if _, writeErr := fmt.Fprint(ios.Out, text); writeErr != nil {
				return fmt.Errorf("writing licenses: %w", writeErr)
			}
			return nil
		},
	}
}

// content joins the report and each module's license files under root.
func content(fsys fs.ReadFileFS, root string) (string, error) {
	report, err := fsys.ReadFile(path.Join(root, "report.txt"))
	if errors.Is(err, fs.ErrNotExist) {
		return placeholder, nil
	}
	if err != nil {
		return "", fmt.Errorf("reading embedded license report: %w", err)
	}

	thirdParty := path.Join(root, "third-party")
	modules, err := moduleFiles(fsys, thirdParty)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.Write(report)
	b.WriteString("\n")
	for _, dir := range slices.Sorted(maps.Keys(modules)) {
		b.WriteString(separator)
		b.WriteString(strings.TrimPrefix(dir, thirdParty+"/") + "\n")
		b.WriteString(separator + "\n")
		for _, p := range modules[dir] {
			data, readErr := fsys.ReadFile(p)
			if readErr != nil {
				return "", fmt.Errorf("reading embedded license %s: %w", p, readErr)
			}
			b.Write(data)
			b.WriteString("\n\n")
		}
	}
	return b.String(), nil
}

// moduleFiles maps each module directory under dir to its files.
// A missing dir holds no modules.
func moduleFiles(fsys fs.FS, dir string) (map[string][]string, error) {
	modules := map[string][]string{}
	err := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		modules[path.Dir(p)] = append(modules[path.Dir(p)], p)
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("reading embedded licenses: %w", err)
	}
	return modules, nil
}
