package controlplane

import (
	"fmt"
	"sync"
	"testing"

	v1 "github.com/schmitthub/clawker/internal/clawkerd/protocol/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestRegistry_Register_And_Get(t *testing.T) {
	t.Parallel()
	r := NewRegistry(nil)

	r.Register("ctr-1", 9090, "v0.1.0")

	got := r.Get("ctr-1")
	if got == nil {
		t.Fatal("expected non-nil agent after Register")
	}
	if got.ContainerID != "ctr-1" {
		t.Errorf("ContainerID = %q, want %q", got.ContainerID, "ctr-1")
	}
	if got.ListenPort != 9090 {
		t.Errorf("ListenPort = %d, want %d", got.ListenPort, 9090)
	}
	if got.Version != "v0.1.0" {
		t.Errorf("Version = %q, want %q", got.Version, "v0.1.0")
	}
	if got.InitStatus != InitPending {
		t.Errorf("InitStatus = %d, want InitPending (%d)", got.InitStatus, InitPending)
	}
	if got.InitEvents != nil && len(got.InitEvents) != 0 {
		t.Errorf("InitEvents should be empty, got %d", len(got.InitEvents))
	}
}

func TestRegistry_Get_Snapshot(t *testing.T) {
	t.Parallel()
	r := NewRegistry(nil)

	r.Register("ctr-snap", 8080, "v1")
	r.AppendInitEvent("ctr-snap", &v1.RunInitResponse{StepName: "step-1"})

	snap1 := r.Get("ctr-snap")
	if snap1 == nil {
		t.Fatal("expected non-nil snapshot")
	}

	// Mutate the snapshot: append to InitEvents and change a field.
	snap1.InitEvents = append(snap1.InitEvents, &v1.RunInitResponse{StepName: "injected"})
	snap1.Version = "tampered"

	// Get again — original should be unchanged.
	snap2 := r.Get("ctr-snap")
	if snap2 == nil {
		t.Fatal("expected non-nil snapshot on second Get")
	}
	if snap2.Version != "v1" {
		t.Errorf("Version = %q after snapshot mutation, want %q", snap2.Version, "v1")
	}
	if len(snap2.InitEvents) != 1 {
		t.Errorf("InitEvents len = %d after snapshot mutation, want 1", len(snap2.InitEvents))
	}
}

func TestRegistry_Get_NotFound(t *testing.T) {
	t.Parallel()
	r := NewRegistry(nil)

	got := r.Get("nonexistent")
	if got != nil {
		t.Errorf("expected nil for unknown container, got %+v", got)
	}
}

func TestRegistry_IsRegistered(t *testing.T) {
	t.Parallel()
	r := NewRegistry(nil)

	if r.IsRegistered("ctr-x") {
		t.Error("expected false before Register")
	}

	r.Register("ctr-x", 1234, "v1")

	if !r.IsRegistered("ctr-x") {
		t.Error("expected true after Register")
	}
}

func TestRegistry_Register_Overwrites(t *testing.T) {
	t.Parallel()
	r := NewRegistry(nil)

	r.Register("ctr-ow", 1000, "v1")
	r.Register("ctr-ow", 2000, "v2")

	got := r.Get("ctr-ow")
	if got == nil {
		t.Fatal("expected non-nil agent after overwrite")
	}
	if got.Version != "v2" {
		t.Errorf("Version = %q, want %q", got.Version, "v2")
	}
	if got.ListenPort != 2000 {
		t.Errorf("ListenPort = %d, want %d", got.ListenPort, 2000)
	}
}

func TestRegistry_SetInitCompleted(t *testing.T) {
	t.Parallel()
	r := NewRegistry(nil)

	r.Register("ctr-c", 5000, "v1")
	r.SetInitCompleted("ctr-c")

	got := r.Get("ctr-c")
	if got == nil {
		t.Fatal("expected non-nil agent")
	}
	if got.InitStatus != InitCompleted {
		t.Errorf("InitStatus = %d, want InitCompleted (%d)", got.InitStatus, InitCompleted)
	}
}

func TestRegistry_SetInitFailed(t *testing.T) {
	t.Parallel()
	r := NewRegistry(nil)

	r.Register("ctr-f", 5000, "v1")
	r.SetInitFailed("ctr-f")

	got := r.Get("ctr-f")
	if got == nil {
		t.Fatal("expected non-nil agent")
	}
	if got.InitStatus != InitFailed {
		t.Errorf("InitStatus = %d, want InitFailed (%d)", got.InitStatus, InitFailed)
	}
}

func TestRegistry_SetClientConn(t *testing.T) {
	t.Parallel()
	r := NewRegistry(nil)

	r.Register("ctr-conn", 5000, "v1")

	conn, err := grpc.NewClient("passthrough:///localhost:0", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to create test grpc client: %v", err)
	}
	defer conn.Close()

	r.SetClientConn("ctr-conn", conn)

	got := r.Get("ctr-conn")
	if got == nil {
		t.Fatal("expected non-nil agent")
	}
	// The snapshot shares the same *grpc.ClientConn pointer (documented behavior:
	// ClientConn is a shared pointer, not deep-copied by Get).
	if got.ClientConn != conn {
		t.Error("expected ClientConn in snapshot to be the same pointer as the one set")
	}
}

func TestRegistry_AppendInitEvent(t *testing.T) {
	t.Parallel()
	r := NewRegistry(nil)

	r.Register("ctr-ev", 5000, "v1")
	r.AppendInitEvent("ctr-ev", &v1.RunInitResponse{StepName: "packages"})
	r.AppendInitEvent("ctr-ev", &v1.RunInitResponse{StepName: "config"})

	got := r.Get("ctr-ev")
	if got == nil {
		t.Fatal("expected non-nil agent")
	}
	if len(got.InitEvents) != 2 {
		t.Fatalf("InitEvents len = %d, want 2", len(got.InitEvents))
	}
	if got.InitEvents[0].StepName != "packages" {
		t.Errorf("InitEvents[0].StepName = %q, want %q", got.InitEvents[0].StepName, "packages")
	}
	if got.InitEvents[1].StepName != "config" {
		t.Errorf("InitEvents[1].StepName = %q, want %q", got.InitEvents[1].StepName, "config")
	}
}

func TestRegistry_Close(t *testing.T) {
	t.Parallel()
	r := NewRegistry(nil)

	// Register agents with real (idle) gRPC client connections.
	conns := make([]*grpc.ClientConn, 3)
	for i := range conns {
		conn, err := grpc.NewClient("passthrough:///localhost:0", grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatalf("failed to create test grpc client %d: %v", i, err)
		}
		conns[i] = conn
		r.Register(fmt.Sprintf("ctr-close-%d", i), uint32(6000+i), "v1")
		r.SetClientConn(fmt.Sprintf("ctr-close-%d", i), conn)
	}

	// Register one agent with nil ClientConn to verify Close handles nils.
	r.Register("ctr-close-nil", 7000, "v1")

	// Close should not panic.
	r.Close()
}

func TestRegistry_Concurrent_Access(t *testing.T) {
	t.Parallel()
	r := NewRegistry(nil)

	const goroutines = 20
	const opsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			cid := fmt.Sprintf("ctr-%d", id%5) // 5 shared container IDs for contention
			for i := 0; i < opsPerGoroutine; i++ {
				switch i % 6 {
				case 0:
					r.Register(cid, uint32(8000+id), "v1")
				case 1:
					r.Get(cid)
				case 2:
					r.IsRegistered(cid)
				case 3:
					r.SetInitCompleted(cid)
				case 4:
					r.AppendInitEvent(cid, &v1.RunInitResponse{StepName: "step"})
				case 5:
					r.SetInitFailed(cid)
				}
			}
		}(g)
	}

	wg.Wait()
}
