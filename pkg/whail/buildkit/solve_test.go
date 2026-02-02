package buildkit

import (
	"testing"

	"github.com/schmitthub/clawker/pkg/whail"
)

func TestToSolveOpt_DefaultDockerfile(t *testing.T) {
	dir := t.TempDir()
	opts := whail.ImageBuildKitOptions{
		ContextDir: dir,
	}

	solveOpt, err := toSolveOpt(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if solveOpt.FrontendAttrs["filename"] != "Dockerfile" {
		t.Errorf("expected default filename %q, got %q", "Dockerfile", solveOpt.FrontendAttrs["filename"])
	}
	if solveOpt.Frontend != "dockerfile.v0" {
		t.Errorf("expected frontend %q, got %q", "dockerfile.v0", solveOpt.Frontend)
	}
}

func TestToSolveOpt_CustomDockerfile(t *testing.T) {
	dir := t.TempDir()
	opts := whail.ImageBuildKitOptions{
		ContextDir: dir,
		Dockerfile: "build/Dockerfile.dev",
	}

	solveOpt, err := toSolveOpt(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if solveOpt.FrontendAttrs["filename"] != "build/Dockerfile.dev" {
		t.Errorf("expected filename %q, got %q", "build/Dockerfile.dev", solveOpt.FrontendAttrs["filename"])
	}
}

func TestToSolveOpt_BuildArgs(t *testing.T) {
	dir := t.TempDir()
	v := "bar"
	opts := whail.ImageBuildKitOptions{
		ContextDir: dir,
		BuildArgs:  map[string]*string{"FOO": &v},
	}

	solveOpt, err := toSolveOpt(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if solveOpt.FrontendAttrs["build-arg:FOO"] != "bar" {
		t.Errorf("expected build-arg:FOO=bar, got %q", solveOpt.FrontendAttrs["build-arg:FOO"])
	}
}

func TestToSolveOpt_Labels(t *testing.T) {
	dir := t.TempDir()
	opts := whail.ImageBuildKitOptions{
		ContextDir: dir,
		Labels:     map[string]string{"com.test.managed": "true", "app": "myapp"},
	}

	solveOpt, err := toSolveOpt(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if solveOpt.FrontendAttrs["label:com.test.managed"] != "true" {
		t.Errorf("expected label:com.test.managed=true, got %q", solveOpt.FrontendAttrs["label:com.test.managed"])
	}
	if solveOpt.FrontendAttrs["label:app"] != "myapp" {
		t.Errorf("expected label:app=myapp, got %q", solveOpt.FrontendAttrs["label:app"])
	}
}

func TestToSolveOpt_NoCache(t *testing.T) {
	dir := t.TempDir()
	opts := whail.ImageBuildKitOptions{
		ContextDir: dir,
		NoCache:    true,
	}

	solveOpt, err := toSolveOpt(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := solveOpt.FrontendAttrs["no-cache"]; !ok {
		t.Error("expected no-cache attribute to be set")
	}
}

func TestToSolveOpt_Target(t *testing.T) {
	dir := t.TempDir()
	opts := whail.ImageBuildKitOptions{
		ContextDir: dir,
		Target:     "builder",
	}

	solveOpt, err := toSolveOpt(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if solveOpt.FrontendAttrs["target"] != "builder" {
		t.Errorf("expected target=builder, got %q", solveOpt.FrontendAttrs["target"])
	}
}

func TestToSolveOpt_Pull(t *testing.T) {
	dir := t.TempDir()
	opts := whail.ImageBuildKitOptions{
		ContextDir: dir,
		Pull:       true,
	}

	solveOpt, err := toSolveOpt(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if solveOpt.FrontendAttrs["image-resolve-mode"] != "pull" {
		t.Errorf("expected image-resolve-mode=pull, got %q", solveOpt.FrontendAttrs["image-resolve-mode"])
	}
}

func TestToSolveOpt_NetworkMode(t *testing.T) {
	dir := t.TempDir()
	opts := whail.ImageBuildKitOptions{
		ContextDir:  dir,
		NetworkMode: "host",
	}

	solveOpt, err := toSolveOpt(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if solveOpt.FrontendAttrs["force-network-mode"] != "host" {
		t.Errorf("expected force-network-mode=host, got %q", solveOpt.FrontendAttrs["force-network-mode"])
	}
}

func TestToSolveOpt_Tags(t *testing.T) {
	dir := t.TempDir()
	opts := whail.ImageBuildKitOptions{
		ContextDir: dir,
		Tags:       []string{"myimage:latest", "myimage:v1"},
	}

	solveOpt, err := toSolveOpt(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(solveOpt.Exports) != 1 {
		t.Fatalf("expected 1 export entry, got %d", len(solveOpt.Exports))
	}
	export := solveOpt.Exports[0]
	if export.Type != "moby" {
		t.Errorf("expected export type %q, got %q", "moby", export.Type)
	}
	if export.Attrs["name"] != "myimage:latest,myimage:v1" {
		t.Errorf("expected name %q, got %q", "myimage:latest,myimage:v1", export.Attrs["name"])
	}
	if export.Attrs["push"] != "false" {
		t.Errorf("expected push=false, got %q", export.Attrs["push"])
	}
}

func TestToSolveOpt_LocalMounts(t *testing.T) {
	dir := t.TempDir()
	opts := whail.ImageBuildKitOptions{
		ContextDir: dir,
	}

	solveOpt, err := toSolveOpt(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if solveOpt.LocalMounts["context"] == nil {
		t.Error("expected context local mount to be set")
	}
	if solveOpt.LocalMounts["dockerfile"] == nil {
		t.Error("expected dockerfile local mount to be set")
	}
}

func TestToSolveOpt_NilBuildArgs(t *testing.T) {
	dir := t.TempDir()
	v := "val"
	opts := whail.ImageBuildKitOptions{
		ContextDir: dir,
		BuildArgs:  map[string]*string{"SET": &v, "NIL": nil},
	}

	solveOpt, err := toSolveOpt(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if solveOpt.FrontendAttrs["build-arg:SET"] != "val" {
		t.Errorf("expected build-arg:SET=val, got %q", solveOpt.FrontendAttrs["build-arg:SET"])
	}
	if _, ok := solveOpt.FrontendAttrs["build-arg:NIL"]; ok {
		t.Error("expected nil build arg to be omitted")
	}
}
