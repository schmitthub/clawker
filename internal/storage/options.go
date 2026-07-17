package storage

// Option configures store construction via New.
type Option func(*Options)

// Migration is a caller-provided function that inspects and optionally mutates a
// store's fields via its Get/Set/Remove member functions. It returns true if it
// changed anything (the store then re-saves the touched fields) and an error if
// the transform could not be applied — a non-nil error aborts construction
// rather than silently skipping the migration. Because it edits fields on the
// store's own node tree, comments on untouched fields are carried along by the
// tree — they survive the re-save without any extra work.
//
// User-visible messages go through Store.Noticef (naming the owning file via
// Store.MigratingLayerPath), never straight to stderr: notices are flushed
// only after the layer's rewrite commits, so a migration cannot announce a
// file change that then fails to land. A rewrite that cannot be persisted
// degrades (in-memory migration + retry next load) instead of failing
// construction.
//
//	func dropLegacyKey(s *storage.Store[Settings]) (bool, error) {
//	    return s.Remove("monitoring.legacy_port")
//	}
type Migration[T Schema] = func(*Store[T]) (bool, error)

// Options holds the accumulated construction configuration. Callers build it
// through the With* functional options; a store's resolved configuration is
// inspectable afterwards via Store.Options().
type Options struct {
	// Filenames is the ordered list of filenames to discover (WithFilenames).
	Filenames []string
	// Defaults is the raw YAML string for the base layer (WithDefaults).
	Defaults string
	// WalkUpAnchor bounds walk-up discovery from CWD up to this dir
	// (inclusive); empty disables walk-up (WithWalkUp).
	WalkUpAnchor string
	// Dirs are directories probed with dual placement, highest priority
	// first (WithDirs).
	Dirs []string
	// Paths are explicit directories probed without dual placement
	// (WithPaths, WithConfigDir, WithDataDir, WithStateDir, WithCacheDir).
	Paths []string
	// Lock enables flock-based advisory locking for writes (WithLock).
	Lock bool
	// DotDefault applies the dual-placement dot prefix in the CWD write
	// fallback (WithDotDefault).
	DotDefault bool
	// DefaultFilename is the filename used when writing to a location with
	// no existing file; defaults to Filenames[0] (WithDefaultFilename).
	DefaultFilename string
	// Header is stamped as a comment block at the top of the file on every
	// write; empty disables it (WithHeader).
	Header string

	migrations []any // []Migration[T] (type-erased; asserted to func(*Store[T]) (bool, error) in migrateLayer)
}

// WithFilenames sets the ordered list of filenames to discover.
// All filenames must share the same schema type T.
// At each walk-up level the first filename in the list takes merge
// precedence when discovered at the same depth.
func WithFilenames(names ...string) Option {
	return func(o *Options) {
		o.Filenames = names
	}
}

// WithDefaults provides a YAML string as the lowest-priority base layer.
// The string is parsed and merged before any discovered files.
// The same constant can be used for scaffolding (clawker init) and defaults.
func WithDefaults(yaml string) Option {
	return func(o *Options) {
		o.Defaults = yaml
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
	return func(o *Options) {
		o.DefaultFilename = name
	}
}

// WithDotDefault enables dual-placement dot-prefix logic in defaultWritePath.
// When the store has no file layers and falls back to CWD, the filename is
// written as .{filename} (or .clawker/{filename} if .clawker/ exists) instead
// of the raw filename. Use this for stores discovered via walk-up where files
// are dot-prefixed by convention.
func WithDotDefault() Option {
	return func(o *Options) {
		o.DotDefault = true
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
	return func(o *Options) {
		o.WalkUpAnchor = anchorDir
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
	return func(o *Options) {
		o.Dirs = append(o.Dirs, dirs...)
	}
}

// WithConfigDir adds the resolved config directory to the explicit path list.
// Resolution: CLAWKER_CONFIG_DIR > XDG_CONFIG_HOME > ~/.config/clawker
func WithConfigDir() Option {
	return func(o *Options) {
		o.Paths = append(o.Paths, configDir())
	}
}

// WithDataDir adds the resolved data directory to the explicit path list.
// Resolution: CLAWKER_DATA_DIR > XDG_DATA_HOME > ~/.local/share/clawker
func WithDataDir() Option {
	return func(o *Options) {
		o.Paths = append(o.Paths, dataDir())
	}
}

// WithStateDir adds the resolved state directory to the explicit path list.
// Resolution: CLAWKER_STATE_DIR > XDG_STATE_HOME > ~/.local/state/clawker
func WithStateDir() Option {
	return func(o *Options) {
		o.Paths = append(o.Paths, stateDir())
	}
}

// WithCacheDir adds the resolved cache directory to the explicit path list.
// Resolution: CLAWKER_CACHE_DIR > XDG_CACHE_HOME > ~/.cache/clawker
func WithCacheDir() Option {
	return func(o *Options) {
		o.Paths = append(o.Paths, cacheDir())
	}
}

// WithPaths adds explicit directories to the discovery path list.
// Files are probed as {dir}/{filename} for each requested filename.
func WithPaths(dirs ...string) Option {
	return func(o *Options) {
		o.Paths = append(o.Paths, dirs...)
	}
}

// WithMigrations registers precondition-based migration functions.
// Each migration runs independently against every discovered file layer's own
// node tree. Migrations that return true trigger an atomic re-save of that file.
func WithMigrations[T Schema](fns ...Migration[T]) Option {
	return func(o *Options) {
		for _, fn := range fns {
			o.migrations = append(o.migrations, fn)
		}
	}
}

// WithLock enables flock-based advisory locking for Write operations.
// Use for stores that need cross-process mutual exclusion (e.g. a store

// writeFilename returns the filename used when creating a file at a location
// with no existing layer: DefaultFilename, falling back to the first
// configured filename. Empty when neither is set.
func (o *Options) writeFilename() string {
	if o.DefaultFilename != "" {
		return o.DefaultFilename
	}
	if len(o.Filenames) > 0 {
		return o.Filenames[0]
	}
	return ""
}

// written by concurrent CLI invocations).
func WithLock() Option {
	return func(o *Options) {
		o.Lock = true
	}
}

// WithHeader stamps the given text as a comment block at the top of the file
// on every Write, one comment line per input line ("# " prefixes are added by
// the YAML encoder — pass raw text). The header is re-applied on each write —
// it survives field-merge mutations and is idempotent: an existing comment
// line matching a header line's `key:` directive prefix (or the whole line,
// for lines without a colon) is replaced rather than duplicated, so a header
// whose value changes between writers (e.g. a version-pinned $schema URL)
// never stacks up. Unrelated comment lines are preserved. An empty header
// disables stamping, leaving the file comment-free.
func WithHeader(header string) Option {
	return func(o *Options) {
		o.Header = header
	}
}
