package buildkit

import (
	bkclient "github.com/moby/buildkit/client"
	"github.com/rs/zerolog/log"
)

// drainProgress reads from the BuildKit status channel until it is closed.
// When suppress is true, statuses are consumed silently. Otherwise, vertex
// names and log lines are forwarded to zerolog at debug level.
func drainProgress(ch chan *bkclient.SolveStatus, suppress bool) {
	for status := range ch {
		if suppress {
			continue
		}
		for _, v := range status.Vertexes {
			if v.Name != "" {
				log.Debug().Str("vertex", v.Name).Msg("buildkit")
			}
		}
		for _, l := range status.Logs {
			log.Debug().Str("vertex", l.Vertex.String()).Bytes("data", l.Data).Msg("buildkit log")
		}
	}
}
