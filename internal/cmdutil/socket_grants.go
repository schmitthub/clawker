package cmdutil

import (
	"fmt"

	"github.com/schmitthub/clawker/internal/db"
)

// SocketGrantStore returns a lazy socket grant store constructor.
func SocketGrantStore(factory *Factory) func() (db.SocketGrantStore, error) {
	return func() (db.SocketGrantStore, error) {
		database, err := factory.DB()
		if err != nil {
			return nil, fmt.Errorf("open CLI database: %w", err)
		}
		log, err := factory.Logger()
		if err != nil {
			return nil, fmt.Errorf("open CLI database logger: %w", err)
		}
		return db.NewSocketGrantStore(database, log), nil
	}
}
