package licenses_test

import (
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmd/licenses"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
)

func newTestFactory(ios *iostreams.IOStreams) *cmdutil.Factory {
	return &cmdutil.Factory{
		Version:         "",
		IOStreams:       ios,
		TUI:             nil,
		Client:          nil,
		Config:          nil,
		Logger:          nil,
		CLIState:        nil,
		ProjectRegistry: nil,
		ProjectManager:  nil,
		GitManager:      nil,
		HostProxy:       nil,
		SocketBridge:    nil,
		Prompter:        nil,
		AdminClient:     nil,
		ControlPlane:    nil,
		HttpClient:      nil,
		BundleManager:   nil,
		Session:         nil,
	}
}

func mapFile(data string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(data), Mode: 0, ModTime: time.Time{}, Sys: nil}
}

func TestNewCmdLicenses(t *testing.T) {
	tio, _, out, _ := iostreams.Test()
	cmd := licenses.NewCmdLicenses(newTestFactory(tio))
	cmd.SetArgs([]string{})

	require.NoError(t, cmd.Execute())
	// Only release builds embed license texts; a test build must hold none.
	assert.Equal(t, licenses.Placeholder, out.String())
}

func TestContent(t *testing.T) {
	const root = "embed/os-arch"
	const report = "Third-party report\n"
	sep := "================================================================================\n"

	tests := []struct {
		name string
		fsys fstest.MapFS
		want string
	}{
		{
			name: "placeholder only",
			fsys: fstest.MapFS{root + "/PLACEHOLDER": mapFile("")},
			want: licenses.Placeholder,
		},
		{
			name: "report only",
			fsys: fstest.MapFS{
				root + "/PLACEHOLDER": mapFile(""),
				root + "/report.txt":  mapFile(report),
			},
			want: report + "\n",
		},
		{
			name: "files outside third-party ignored",
			fsys: fstest.MapFS{
				root + "/report.txt":                        mapFile(report),
				root + "/unknown":                           mapFile("x"),
				root + "/other/example.com/mod/LICENSE":     mapFile("x"),
				root + "/third-party/example.com/a/LICENSE": mapFile("A License"),
			},
			want: report + "\n" + sep + "example.com/a\n" + sep + "\nA License\n\n",
		},
		{
			name: "modules sorted and nested modules kept separate",
			fsys: fstest.MapFS{
				root + "/report.txt":                                   mapFile(report),
				root + "/third-party/example.com/z/LICENSE":            mapFile("Z License"),
				root + "/third-party/example.com/a/LICENSE":            mapFile("A License"),
				root + "/third-party/example.com/a/NOTICE":             mapFile("A Notice"),
				root + "/third-party/example.com/a/internal/v/LICENSE": mapFile("V License"),
			},
			want: report + "\n" +
				sep + "example.com/a\n" + sep + "\nA License\n\nA Notice\n\n" +
				sep + "example.com/a/internal/v\n" + sep + "\nV License\n\n" +
				sep + "example.com/z\n" + sep + "\nZ License\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := licenses.Content(tt.fsys, root)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestContent_ReadError(t *testing.T) {
	const root = "embed/os-arch"
	bad := root + "/third-party/example.com/a/LICENSE"
	fsys := fstest.MapFS{
		root + "/report.txt": mapFile("report\n"),
		bad:                  mapFile("A License"),
	}

	_, err := licenses.Content(brokenFS{fsys, bad}, root)
	require.ErrorIs(t, err, fs.ErrPermission)
}

// brokenFS fails reads of one path.
type brokenFS struct {
	fstest.MapFS

	bad string
}

func (b brokenFS) ReadFile(name string) ([]byte, error) {
	if name == b.bad {
		return nil, fs.ErrPermission
	}
	return b.MapFS.ReadFile(name)
}
