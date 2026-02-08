package tui

import (
	"testing"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/stretchr/testify/require"
)

func TestTablePlain_Golden(t *testing.T) {
	tests := []struct {
		name    string
		headers []string
		rows    [][]string
	}{
		{
			name:    "basic",
			headers: []string{"NAME", "STATUS", "IMAGE"},
			rows: [][]string{
				{"web", "running", "nginx:latest"},
				{"db", "stopped", "postgres:16"},
			},
		},
		{
			name:    "image_list",
			headers: []string{"IMAGE", "ID", "CREATED", "SIZE"},
			rows: [][]string{
				{"clawker-fawker-demo:latest", "a1b2c3d4e5f6", "2 months ago", "256.00MB"},
				{"node:20-slim", "a1b2c3d4e5f6", "2 months ago", "256.00MB"},
			},
		},
		{
			name:    "empty",
			headers: []string{"NAME", "STATUS"},
			rows:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tio := iostreams.NewTestIOStreams()
			tui := NewTUI(tio.IOStreams)

			tp := tui.NewTable(tt.headers...)
			for _, row := range tt.rows {
				tp.AddRow(row...)
			}

			err := tp.Render()
			require.NoError(t, err)

			compareGolden(t, tt.name, tio.OutBuf.String())
		})
	}
}

func TestTableStyled_Golden(t *testing.T) {
	forceColorProfile(t)

	tests := []struct {
		name      string
		headers   []string
		rows      [][]string
		termWidth int
	}{
		{
			name:    "basic",
			headers: []string{"NAME", "STATUS", "IMAGE"},
			rows: [][]string{
				{"web", "running", "nginx:latest"},
				{"db", "stopped", "postgres:16"},
			},
			termWidth: 80,
		},
		{
			name:    "image_list",
			headers: []string{"IMAGE", "ID", "CREATED", "SIZE"},
			rows: [][]string{
				{"clawker-fawker-demo:latest", "a1b2c3d4e5f6", "2 months ago", "256.00MB"},
				{"node:20-slim", "a1b2c3d4e5f6", "2 months ago", "256.00MB"},
			},
			termWidth: 80,
		},
		{
			name:    "narrow",
			headers: []string{"IMAGE", "ID", "CREATED", "SIZE"},
			rows: [][]string{
				{"clawker-fawker-demo:latest", "a1b2c3d4e5f6", "2 months ago", "256.00MB"},
				{"node:20-slim", "a1b2c3d4e5f6", "2 months ago", "256.00MB"},
			},
			termWidth: 40,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tio := iostreams.NewTestIOStreams()
			tio.SetInteractive(true)
			tio.SetColorEnabled(true)
			tio.SetTerminalSize(tt.termWidth, 24)

			tui := NewTUI(tio.IOStreams)

			tp := tui.NewTable(tt.headers...)
			for _, row := range tt.rows {
				tp.AddRow(row...)
			}

			err := tp.Render()
			require.NoError(t, err)

			compareGolden(t, tt.name, tio.OutBuf.String())
		})
	}
}
