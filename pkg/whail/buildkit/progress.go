package buildkit

import (
	bkclient "github.com/moby/buildkit/client"
	digest "github.com/opencontainers/go-digest"
	"github.com/rs/zerolog/log"
)

// drainProgress reads from the BuildKit status channel until it is closed.
// When suppress is true, only error-state vertexes are logged. Otherwise,
// vertex names and log lines are forwarded to zerolog at debug level.
func drainProgress(ch chan *bkclient.SolveStatus, suppress bool) {
	logged := make(map[digest.Digest]bool)
	for status := range ch {
		for _, v := range status.Vertexes {
			if v.Error != "" {
				name := v.Name
				if name == "" {
					name = v.Digest.String()
				}
				log.Error().Str("vertex", name).Str("error", v.Error).Msg("buildkit vertex error")
				continue
			}
			if suppress {
				continue
			}
			// Log each vertex once when it starts (or is cached). BuildKit sends
			// full-state snapshots, so we deduplicate by digest and gate on
			// Started/Completed to emit lines in execution order.
			if v.Name != "" && !logged[v.Digest] && (v.Started != nil || v.Completed != nil) {
				logged[v.Digest] = true
				log.Debug().Str("vertex", v.Name).Msg("buildkit")
			}
		}
		if suppress {
			continue
		}
		for _, l := range status.Logs {
			log.Debug().Str("vertex", l.Vertex.String()).Bytes("data", l.Data).Msg("buildkit log")
		}
	}
}
