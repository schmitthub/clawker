package storage

// Option configures store construction via New.
type Option func(*options)

// Migration is a caller-provided function that inspects and optionally mutates a
// store's fields via its Get/Set/Remove member functions. It returns true if it
// changed anything (the store then re-saves the touched fields) and an error if
// the transform could not be applied — a non-nil error aborts construction
// rather than silently skipping the migration. Because it edits fields on the
// store's own node tree, comments on untouched fields are carried along by the
// tree — they survive the re-save without any extra work.
//
//	func dropLegacyKey(s *storage.Store[Settings]) (bool, error) {
//	    return s.Remove("monitoring.legacy_port")
//	}
type Migration[T Schema] = func(*Store[T]) (bool, error)

// options holds the accumulated construction configuration.
type options struct {
	filenames       []string
	defaults        string   // raw YAML string for the base layer
	walkUpAnchor    string   // bound walk-up from CWD up to this dir (inclusive); empty disables walk-up
	dirs            []string // directories probed with dual placement (highest priority first)
	paths           []string // explicit directories to probe (no dual placement)
	migrations      []any    // []Migration[T] (type-erased; asserted to func(*Store[T]) bool in New)
	lock            bool
	dotDefault      bool   // apply dual-placement dot prefix in defaultWritePath CWD fallback
	defaultFilename string // filename for new writes when no file layers exist; defaults to filenames[0]
	schemaURL       string // JSON Schema URL stamped as a yaml-language-server head comment; empty disables the header
}

// WithFilenames sets the ordered list of filenames to discover.
// All filenames must share the same schema type T.
// At each walk-up level the first filename in the list takes merge
// precedence when discovered at the same depth.
func WithFilenames(names ...string) Option {
	return func(o *options) {
		o.filenames = names
	}
}

// WithDefaults provides a YAML string as the lowest-priority base layer.
// The string is parsed and merged before any discovered files.
// The same constant can be used for scaffolding (clawker init) and defaults.
func WithDefaults(yaml string) Option {
	return func(o *options) {
		o.defaults = yaml
	}
}

// WithDefaultsFromStruct generates a defaults YAML string from the `default`
// struct tags of T and registers it as the lowest-priority base layer.
// This is equivalent to WithDefaults(GenerateDefaultsYAML[T]()).
func WithDefaultsFromStruct[T Schema]() Option {
	return WithDefaults(GenerateDefaultsYAML[T]())
}

// WithDefaultFilename sets the filename used when writing to a directory with
// no existing file layers. Without this, filenames[0] is used, which may be
// a local override variant rather than the main file.
func WithDefaultFilename(name string) Option {
	return func(o *options) {
		o.defaultFilename = name
	}
}

// WithDotDefault enables dual-placement dot-prefix logic in defaultWritePath.
// When the store has no file layers and falls back to CWD, the filename is
// written as .{filename} (or .clawker/{filename} if .clawker/ exists) instead
// of the raw filename. Use this for stores discovered via walk-up where files
// are dot-prefixed by convention.
func WithDotDefault() Option {
	return func(o *options) {
		o.dotDefault = true
	}
}

// WithWalkUp enables bounded walk-up discovery from CWD up to anchorDir
// (inclusive). The store resolves CWD via os.Getwd(); anchorDir is a plain
// directory supplied by the caller (the storage engine holds no project-domain
// knowledge of how it was chosen). At each level the store checks for
// .clawker/{filename} (dir form) first, then .{filename} (flat dotfile form).
// Walk-up never proceeds past anchorDir.
//
// anchorDir must be CWD or an ancestor of it. A non-ancestor anchor (a path
// beside or below CWD, a relative path, or a garbage path) is a caller
// programming error: store construction fails with an error wrapping
// ErrAnchorNotAncestor rather than letting the walk escape to the filesystem
// root. An empty anchorDir disables walk-up, so discovery falls back to
// explicit paths only.
func WithWalkUp(anchorDir string) Option {
	return func(o *options) {
		o.walkUpAnchor = anchorDir
	}
}

// WithDirs adds directories to be probed with dual placement discovery.
// Each directory uses the same dual-placement logic as walk-up: if a .clawker/
// subdirectory exists, it probes .clawker/{filename} (dir form); otherwise it
// probes .{filename} (flat dotfile form). Both .yaml and .yml extensions are
// accepted.
// Directories are probed in the order given (first = highest priority).
// Priority: walk-up > dirs > explicit paths (WithPaths/WithConfigDir/etc.).
func WithDirs(dirs ...string) Option {
	return func(o *options) {
		o.dirs = append(o.dirs, dirs...)
	}
}

// WithConfigDir adds the resolved config directory to the explicit path list.
// Resolution: CLAWKER_CONFIG_DIR > XDG_CONFIG_HOME > ~/.config/clawker
func WithConfigDir() Option {
	return func(o *options) {
		o.paths = append(o.paths, configDir())
	}
}

// WithDataDir adds the resolved data directory to the explicit path list.
// Resolution: CLAWKER_DATA_DIR > XDG_DATA_HOME > ~/.local/share/clawker
func WithDataDir() Option {
	return func(o *options) {
		o.paths = append(o.paths, dataDir())
	}
}

// WithStateDir adds the resolved state directory to the explicit path list.
// Resolution: CLAWKER_STATE_DIR > XDG_STATE_HOME > ~/.local/state/clawker
func WithStateDir() Option {
	return func(o *options) {
		o.paths = append(o.paths, stateDir())
	}
}

// WithCacheDir adds the resolved cache directory to the explicit path list.
// Resolution: CLAWKER_CACHE_DIR > XDG_CACHE_HOME > ~/.cache/clawker
func WithCacheDir() Option {
	return func(o *options) {
		o.paths = append(o.paths, cacheDir())
	}
}

// WithPaths adds explicit directories to the discovery path list.
// Files are probed as {dir}/{filename} for each requested filename.
func WithPaths(dirs ...string) Option {
	return func(o *options) {
		o.paths = append(o.paths, dirs...)
	}
}

// WithMigrations registers precondition-based migration functions.
// Each migration runs independently on every discovered file's raw map.
// Migrations that return true trigger an atomic re-save of that file.
func WithMigrations[T Schema](fns ...Migration[T]) Option {
	return func(o *options) {
		for _, fn := range fns {
			o.migrations = append(o.migrations, fn)
		}
	}
}

// WithLock enables flock-based advisory locking for Write operations.
// Use for stores that need cross-process mutual exclusion (e.g. a store
// written by concurrent CLI invocations).
func WithLock() Option {
	return func(o *options) {
		o.lock = true
	}
}

// WithSchemaURL stamps a `# yaml-language-server: $schema=<url>` head comment
// onto the file on every Write, so editors (VS Code, JetBrains via the YAML
// language server) validate and autocomplete the persisted YAML against the
// published JSON Schema. The header is re-applied on each write — it survives
// field-merge mutations and is idempotent (no duplicate lines). An empty URL
// disables the header, leaving the file comment-free.
func WithSchemaURL(url string) Option {
	return func(o *options) {
		o.schemaURL = url
	}
}
