package changelog

import "github.com/schmitthub/clawker/internal/consts"

// ChangelogURL is the raw CHANGELOG.md on the main branch. It is fetched from
// main (not a release tag) so a changelog entry committed after a release —
// anchored to the latest tag — is picked up with no re-release; the installed
// binary version remains the ceiling for what the teaser shows, so pulling
// tip-of-main leaks nothing premature. Built from the shared repo identity
// consts rather than re-spelling the literal repo string.
var ChangelogURL = consts.RawGitHubBaseURL + "/" + consts.GitHubRepo + "/main/CHANGELOG.md"

// Parsing tokens for the Keep a Changelog format and clawker's HTML-comment
// metadata convention. Centralized here so the parser references named consts
// rather than scattering literals.
const (
	// versionHeaderPrefix marks a version section: "## [x.y.z] - YYYY-MM-DD".
	versionHeaderPrefix = "## ["
	// subsectionPrefix marks a Keep-a-Changelog category: "### Added" etc.
	subsectionPrefix = "### "

	// metaCommentPrefix and metaCommentSuffix bound the per-entry metadata
	// HTML comment: "<!-- clawker: tag=feature docs=<url> -->".
	metaCommentPrefix = "<!-- clawker:"
	metaCommentSuffix = "-->"
	// metaKeyword namespaces the metadata so a plain HTML comment is ignored.
	metaKeyword = "clawker:"

	// metaKeyTag and metaKeyDocs are the recognized metadata keys.
	metaKeyTag  = "tag"
	metaKeyDocs = "docs"

	// dateSeparator splits the version from the date in a header line:
	// "[x.y.z] - YYYY-MM-DD".
	dateSeparator = " - "
)

// Tag is the closed set of entry tags. It is a string subtype so it still
// formats and renders verbatim, while letting the compiler enforce that only
// the named constants flow through the tag-handling switches.
type Tag string

// Recognized entry tags. tagFromSubsection maps a Keep-a-Changelog "###"
// subsection heading to a tag when no explicit metadata tag is present.
const (
	TagFeature  Tag = "feature"
	TagFix      Tag = "fix"
	TagBreaking Tag = "breaking"
	TagPerf     Tag = "perf"
	TagChanged  Tag = "changed"
)

// Keep a Changelog standard subsection headings (case-insensitive match).
const (
	sectionAdded      = "added"
	sectionChanged    = "changed"
	sectionDeprecated = "deprecated"
	sectionRemoved    = "removed"
	sectionFixed      = "fixed"
	sectionSecurity   = "security"
)
