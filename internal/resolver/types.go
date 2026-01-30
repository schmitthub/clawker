package resolver

// ImageSource indicates where an image reference was resolved from.
type ImageSource string

const (
	ImageSourceExplicit ImageSource = "explicit" // User specified via CLI or args
	ImageSourceProject  ImageSource = "project"  // Found via project label search
	ImageSourceDefault  ImageSource = "default"  // From config/settings default_image
)

// ResolvedImage contains the result of image resolution with source tracking.
type ResolvedImage struct {
	Reference string      // The image reference (name:tag)
	Source    ImageSource // Where the image was resolved from
}
