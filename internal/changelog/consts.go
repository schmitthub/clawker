package changelog

import "github.com/schmitthub/clawker/internal/consts"

// ChangelogURL is the raw CHANGELOG.md on the main branch. It is fetched from
// main (not a release tag) so a changelog entry committed after a release —
// anchored to the latest tag — is picked up with no re-release; the installed
// binary version remains the ceiling for what the teaser shows, so pulling
// tip-of-main leaks nothing premature. Built from the shared repo identity
// consts rather than re-spelling the literal repo string. It is a const, not a
// test seam: tests inject an internal/httpmock client (the transport is the
// seam), so the production URL is never swapped.
const ChangelogURL = consts.RawGitHubBaseURL + "/" + consts.GitHubRepo + "/" + consts.GitHubRefMain + "/CHANGELOG.md"

// Parsing tokens for the Keep a Changelog format. Centralized here so the
// parser references named consts rather than scattering literals.
const (
	// versionHeaderPrefix marks a version section: "## [x.y.z] - YYYY-MM-DD".
	versionHeaderPrefix = "## ["

	// htmlCommentPrefix and htmlCommentSuffix bound a single-line HTML comment.
	// Comment lines are stripped from the body (this also drops the legacy
	// "<!-- clawker: ... -->" metadata lines that may linger in older sources).
	htmlCommentPrefix = "<!--"
	htmlCommentSuffix = "-->"

	// dateDash splits the version from the date in a header line
	// ("[x.y.z] - YYYY-MM-DD") at its first dash; the date's own dashes follow.
	dateDash = "-"
)
