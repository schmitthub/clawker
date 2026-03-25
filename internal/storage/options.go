package storage

// Option configures store construction via NewStore.
type Option func(*options)

// Migration is a caller-provided function that inspects a raw YAML map and
// optionally transforms it. Returns true if the map was modified (triggers
// an atomic re-save of the source file).
type Migration func(raw map[string]any) bool

// options holds the accumulated construction configuration.
type options struct {
	filenames  []string
	defaults   string // raw YAML string for the base layer
	walkUp     bool
	dirs       []string // directories probed with dual placement (highest priority first)
	paths      []string // explicit directories to probe (no dual placement)
	migrations []Migration
	lock       bool
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

// WithWalkUp enables bounded walk-up discovery from CWD to the registered
// project root. The store resolves both CWD and project root internally:
// CWD via os.Getwd(), project root by reading the registry at dataDir().
// At each level the store checks for .clawker/{filename} (dir form) first,
// then .{filename} (flat dotfile form). Walk-up never proceeds past the
// project root. If CWD is not within a registered project, walk-up is
// skipped and discovery falls back to explicit paths only.
func WithWalkUp() Option {
	return func(o *options) {
		o.walkUp = true
	}
}

// WithDirs adds directories to be probed with dual placement discovery.
// Each directory uses the same dual-placement logic as walk-up: if a .clawker/
// subdirectory exists, it probes .clawker/{filename} (dir form); otherwise it
// probes .{filename} (flat dotfile form). Both .yaml and .yml extensions are
// accepted. No registry required.
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
// Files are probed as {dir}/{filename} for each configured filename.
func WithPaths(dirs ...string) Option {
	return func(o *options) {
		o.paths = append(o.paths, dirs...)
	}
}

// WithMigrations registers precondition-based migration functions.
// Each migration runs independently on every discovered file's raw map.
// Migrations that return true trigger an atomic re-save of that file.
func WithMigrations(fns ...Migration) Option {
	return func(o *options) {
		o.migrations = append(o.migrations, fns...)
	}
}

// WithLock enables flock-based advisory locking for Write operations.
// Use for stores that need cross-process mutual exclusion (e.g. registry).
func WithLock() Option {
	return func(o *options) {
		o.lock = true
	}
}
