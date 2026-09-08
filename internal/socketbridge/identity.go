package socketbridge

// ListenerIdentity identifies a Unix socket listener. Numeric IDs are the
// comparison values. Owner and Group are display values and can be empty.
type ListenerIdentity struct {
	UID   int
	GID   int
	Owner string
	Group string
}
