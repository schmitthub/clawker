package bundler

// VariantConfig defines supported variants and architectures for Docker images.
type VariantConfig struct {
	// DebianDefault is the default Debian variant (e.g., "trixie")
	DebianDefault string

	// AlpineDefault is the default Alpine variant (e.g., "alpine3.23")
	AlpineDefault string

	// Variants maps variant names to supported architectures
	// e.g., {"trixie": ["amd64", "arm64v8"], "alpine3.23": ["amd64", "arm64v8"]}
	Variants map[string][]string

	// Arches is the list of all supported architectures
	Arches []string
}

// DefaultVariantConfig returns the default variant configuration.
func DefaultVariantConfig() *VariantConfig {
	arches := []string{"amd64", "arm64v8"}

	return &VariantConfig{
		DebianDefault: "trixie",
		AlpineDefault: "alpine3.23",
		Arches:        arches,
		Variants: map[string][]string{
			"trixie":     arches,
			"bookworm":   arches,
			"alpine3.23": arches,
			"alpine3.22": arches,
		},
	}
}
