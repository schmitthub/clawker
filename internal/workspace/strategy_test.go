package workspace

import (
	"errors"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/mount"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/mocks"
)

func TestGetShareVolumeMount(t *testing.T) {
	hostPath := "/tmp/test-clawker-share"
	m := GetShareVolumeMount(hostPath)

	if m.Type != mount.TypeBind {
		t.Errorf("Type = %v, want %v", m.Type, mount.TypeBind)
	}

	if m.Source != hostPath {
		t.Errorf("Source = %q, want %q", m.Source, hostPath)
	}

	if m.Target != ShareStagingPath {
		t.Errorf("Target = %q, want %q", m.Target, ShareStagingPath)
	}

	if !m.ReadOnly {
		t.Error("ReadOnly = false, want true")
	}
}

// TestGetConfigVolumeMounts_InfraVolumes pins the clawker infra mounts: the
// history volume at /commandhistory and the lifecycle volume at
// $HOME/.clawker — the latter is what keeps the post-init marker alive
// across container recreation (marker lifetime must match the config
// volumes post_init mutates).
func TestGetConfigVolumeMounts_InfraVolumes(t *testing.T) {
	mounts, err := GetConfigVolumeMounts("proj", "agent", "claude", nil)
	if err != nil {
		t.Fatal(err)
	}

	byTarget := map[string]mount.Mount{}
	for _, m := range mounts {
		byTarget[m.Target] = m
	}

	clawkerTarget := consts.ContainerHomeDir + "/" + consts.DotClawkerDir
	cm, ok := byTarget[clawkerTarget]
	if !ok {
		t.Fatalf("no mount at %s — post-init marker would die with the container", clawkerTarget)
	}
	if cm.Type != mount.TypeVolume {
		t.Errorf("clawker mount Type = %v, want volume", cm.Type)
	}
	// The lifecycle volume is harness-scoped: it carries the post-init
	// marker and the image's staged seeds, both of which belong to the
	// harness image the container was created from.
	if want := "clawker.proj.agent-claude." + consts.VolumePurposeClawker; cm.Source != want {
		t.Errorf("clawker mount Source = %q, want %q", cm.Source, want)
	}

	hm, ok := byTarget["/commandhistory"]
	if !ok {
		t.Fatal("no history mount at /commandhistory")
	}
	// History is harness-neutral shell history — shared across harnesses.
	if want := "clawker.proj.agent-" + consts.VolumePurposeHistory; hm.Source != want {
		t.Errorf("history mount Source = %q, want %q", hm.Source, want)
	}
}

// TestGetConfigVolumeMounts_HarnessScopedIdentity is the regression test for
// the cross-harness volume collision: both shipped harnesses declare a
// volume named "config", so without a harness discriminator in the volume
// identity a codex run would mount the claude volume — settings, plugins,
// and the in-container OAuth login — at ~/.codex.
func TestGetConfigVolumeMounts_HarnessScopedIdentity(t *testing.T) {
	claudeMounts, err := GetConfigVolumeMounts("proj", "dev", "claude",
		[]config.VolumeSpec{{Name: "config", Path: ".claude"}})
	if err != nil {
		t.Fatal(err)
	}
	codexMounts, err := GetConfigVolumeMounts("proj", "dev", "codex",
		[]config.VolumeSpec{{Name: "config", Path: ".codex"}})
	if err != nil {
		t.Fatal(err)
	}

	if got, want := claudeMounts[0].Source, "clawker.proj.dev-claude.config"; got != want {
		t.Errorf("claude config volume = %q, want %q", got, want)
	}
	if got, want := codexMounts[0].Source, "clawker.proj.dev-codex.config"; got != want {
		t.Errorf("codex config volume = %q, want %q", got, want)
	}
	if claudeMounts[0].Source == codexMounts[0].Source {
		t.Fatalf("claude and codex resolved the same config volume %q — codex would mount claude's credentials",
			claudeMounts[0].Source)
	}
}

// TestGetConfigVolumeMounts_QualifiedHarness pins that an installed-bundle
// harness composes volume mounts: its selection spelling — what
// loadHarnessResolved puts in Bundle.Name and what flows here — is the
// qualified namespace.bundle.component address, so rejecting dots in the
// harness segment would make every bundled harness fail container create at
// volume naming.
func TestGetConfigVolumeMounts_QualifiedHarness(t *testing.T) {
	mounts, err := GetConfigVolumeMounts("proj", "dev", "acme.tools.myharness",
		[]config.VolumeSpec{{Name: "config", Path: ".myharness"}})
	if err != nil {
		t.Fatalf("GetConfigVolumeMounts() error = %v — qualified harness spelling must compose", err)
	}
	if got, want := mounts[0].Source, "clawker.proj.dev-acme.tools.myharness.config"; got != want {
		t.Errorf("config volume = %q, want %q", got, want)
	}
}

// TestGetConfigVolumeMounts_RejectsEmptyHarness pins that the harness segment
// is mandatory: a blank harness would collapse every harness back onto one
// shared volume namespace.
func TestGetConfigVolumeMounts_RejectsEmptyHarness(t *testing.T) {
	_, err := GetConfigVolumeMounts("proj", "dev", "",
		[]config.VolumeSpec{{Name: "config", Path: ".claude"}})
	if err == nil {
		t.Fatal("GetConfigVolumeMounts() error = nil, want error for empty harness name")
	}
}

// TestEnsureConfigVolumes_ExistingOtherHarnessVolume reproduces the setup of
// the credential-bleed bug: a claude run under the pre-fix flat naming left
// its config volume at clawker.proj.dev-config (named volumes survive --rm,
// and pre-fix volumes carried no harness label), then the same agent runs
// the codex harness. The fixture stages the volume at that PRE-FIX colliding
// name on purpose: post-fix, codex composes the harness-scoped
// clawker.proj.dev-codex.config, never touches the flat volume, and creates
// its own — pre-fix (mutation: drop the harness segment from
// HarnessVolumeName) codex composes the flat name, hits claude's volume, and
// this test goes red instead of silently mounting claude's state (including
// .credentials.json) at ~/.codex.
func TestEnsureConfigVolumes_ExistingOtherHarnessVolume(t *testing.T) {
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupVolumeExists("clawker.proj.dev-config", true) // the pre-fix flat name; all other names: not found
	fake.SetupVolumeCreate()

	result, err := EnsureConfigVolumes(t.Context(), fake.Client, "proj", "dev", "codex",
		[]config.VolumeSpec{{Name: "config", Path: ".codex"}})
	if err != nil {
		t.Fatalf("EnsureConfigVolumes() error = %v", err)
	}

	if !result.CreatedByName["config"] {
		t.Error("CreatedByName[config] = false — codex adopted the existing claude volume instead of creating its own")
	}
	if !result.HistoryCreated {
		t.Error("HistoryCreated = false, want true")
	}
	if !result.ClawkerCreated {
		t.Error("ClawkerCreated = false, want true")
	}
}

// TestEnsureConfigVolumes_SameHarnessReentryAdopts is the recreation
// regression guard for the cross-harness ownership failsafe: recreating a
// container (or repeating run) for the same agent+harness must silently
// adopt its own existing volumes — only a genuine cross-harness mismatch
// refuses.
func TestEnsureConfigVolumes_SameHarnessReentryAdopts(t *testing.T) {
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupVolumeExistsWithLabels("clawker.proj.dev-codex.config", map[string]string{
		consts.LabelHarness: "codex",
	})
	fake.SetupVolumeCreate()

	result, err := EnsureConfigVolumes(t.Context(), fake.Client, "proj", "dev", "codex",
		[]config.VolumeSpec{{Name: "config", Path: ".codex"}})
	if err != nil {
		t.Fatalf("EnsureConfigVolumes() error = %v — same-harness re-entry must adopt silently", err)
	}
	if result.CreatedByName["config"] {
		t.Error("CreatedByName[config] = true, want false — the existing same-harness volume must be adopted")
	}
}

// TestEnsureConfigVolumes_CrossHarnessMismatchRefuses pins the label
// failsafe behind the naming fix: if a volume already sits at the target
// name but its harness ownership label names a DIFFERENT component, refuse
// rather than silently hand one harness another's state. The error must be
// the typed ownership refusal and name both harnesses so the user can act.
func TestEnsureConfigVolumes_CrossHarnessMismatchRefuses(t *testing.T) {
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupVolumeExistsWithLabels("clawker.proj.dev-codex.config", map[string]string{
		consts.LabelHarness: "acme.tools.claude",
	})
	fake.SetupVolumeCreate()

	_, err := EnsureConfigVolumes(t.Context(), fake.Client, "proj", "dev", "codex",
		[]config.VolumeSpec{{Name: "config", Path: ".codex"}})
	if err == nil {
		t.Fatal("EnsureConfigVolumes() error = nil, want cross-harness ownership refusal")
	}
	var ownershipErr *docker.HarnessVolumeOwnershipError
	if !errors.As(err, &ownershipErr) {
		t.Fatalf("error = %v, want a *docker.HarnessVolumeOwnershipError in the chain", err)
	}
	if !strings.Contains(err.Error(), "acme.tools.claude") || !strings.Contains(err.Error(), "codex") {
		t.Errorf("error = %q, must name both the owning and the requesting harness", err.Error())
	}
}

// TestEnsureConfigVolumes_UnlabeledExistingVolumeAdopts pins the ownership
// failsafe's deliberate soft arm. A MANAGED volume at a harness-scoped name
// with no ownership label cannot come from any shipped clawker (clawker
// always labels harness-scoped volumes, and flat pre-harness names are
// uncomposable here) — the population is hand-placed volumes, above all
// legitimate backup/restore (docker volume create + tar restore drops
// labels). Those are adopted: refusing them would not stop deliberate
// placement (whoever creates the volume can forge the label) and Docker
// cannot retro-label a local volume, so strictness would only break restore
// workflows while buying accident-protection the mismatch arm already
// provides.
func TestEnsureConfigVolumes_UnlabeledExistingVolumeAdopts(t *testing.T) {
	fake := mocks.NewFakeClient(configmocks.NewBlankConfig())
	fake.SetupVolumeExistsWithLabels("clawker.proj.dev-codex.config", nil)
	fake.SetupVolumeCreate()

	result, err := EnsureConfigVolumes(t.Context(), fake.Client, "proj", "dev", "codex",
		[]config.VolumeSpec{{Name: "config", Path: ".codex"}})
	if err != nil {
		t.Fatalf("EnsureConfigVolumes() error = %v — an unlabeled managed occupant must be adopted, not refused", err)
	}
	if result.CreatedByName["config"] {
		t.Error("CreatedByName[config] = true, want false — the unlabeled existing volume must be adopted")
	}
}

func TestGetHostStateMount(t *testing.T) {
	hostPath := "/home/alice/.claude/projects"
	m, err := GetHostStateMount(hostPath, ".claude/projects")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if m.Type != mount.TypeBind {
		t.Errorf("Type = %v, want %v", m.Type, mount.TypeBind)
	}
	if m.Source != hostPath {
		t.Errorf("Source = %q, want %q", m.Source, hostPath)
	}
	want := consts.ContainerHomeDir + "/.claude/projects"
	if m.Target != want {
		t.Errorf("Target = %q, want %q", m.Target, want)
	}
	// RW is intentional — auto-memory and session jsonls are written from inside the container.
	if m.ReadOnly {
		t.Error("ReadOnly = true, want false (auto-memory needs RW)")
	}
}

func TestGetHostStateMount_RejectsRelativePath(t *testing.T) {
	_, err := GetHostStateMount("relative/path", ".claude/projects")
	if err == nil {
		t.Fatal("GetHostStateMount() error = nil, want error about absolute path")
	}
	if !strings.Contains(err.Error(), "must be absolute") {
		t.Errorf("error message = %q, should mention 'must be absolute'", err.Error())
	}
}

func TestGetHostStateMount_RejectsEmptyPath(t *testing.T) {
	_, err := GetHostStateMount("", ".claude/projects")
	if err == nil {
		t.Fatal("GetHostStateMount() error = nil, want error about absolute path")
	}
}

func TestShareConstants(t *testing.T) {
	if SharePurpose != "share" {
		t.Errorf("SharePurpose = %q, want %q", SharePurpose, "share")
	}

	if ShareStagingPath != "/home/claude/.clawker-share" {
		t.Errorf("ShareStagingPath = %q, want %q", ShareStagingPath, "/home/claude/.clawker-share")
	}
}

func TestConfigVolumeResult(t *testing.T) {
	// Zero value: nothing created.
	var result ConfigVolumeResult
	if result.CreatedByName["config"] {
		t.Error("CreatedByName zero value should report false")
	}
	if result.HistoryCreated {
		t.Error("HistoryCreated zero value should be false")
	}
}
