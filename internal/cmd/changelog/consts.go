package changelog

// Emoji prefixes for the changelog tag badges rendered in the `clawker
// changelog` entry headers. One per recognized changelog.Tag*, plus a neutral
// fallback for an unrecognized tag.
const (
	tagEmojiFeature  = "🚀"
	tagEmojiFix      = "🐛"
	tagEmojiBreaking = "💥"
	tagEmojiPerf     = "⚡"
	tagEmojiChanged  = "🔧"
	tagEmojiDefault  = "📝"
)
