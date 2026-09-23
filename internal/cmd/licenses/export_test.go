package licenses

import "io/fs"

// Placeholder is the text shown by builds without embedded licenses.
const Placeholder = placeholder

// Content exposes content for black-box tests.
func Content(fsys fs.ReadFileFS, root string) (string, error) {
	return content(fsys, root)
}
