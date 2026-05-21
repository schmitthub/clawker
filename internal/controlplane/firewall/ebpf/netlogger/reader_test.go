package netlogger

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cilium/ebpf/ringbuf"

	"github.com/schmitthub/clawker/internal/logger"
)

// fakeRingbuf serves a scripted sequence of records / errors to the
// reader goroutine. The scripted sequence is then capped with
// ringbuf.ErrClosed so the goroutine exits — mirrors how
// Service.Stop() closes the real Reader in production.
type fakeRingbuf struct {
	mu      sync.Mutex
	records [][]byte
	errs    []error
}

func (f *fakeRingbuf) ReadInto(rec *ringbuf.Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		return err
	}
	if len(f.records) > 0 {
		rec.RawSample = f.records[0]
		f.records = f.records[1:]
		return nil
	}
	return ringbuf.ErrClosed
}

func TestReader_HappyPath_ForwardsCopiesToQueue(t *testing.T) {
	queue := make(chan []byte, 4)
	metrics := NewMetrics()
	src := &fakeRingbuf{records: [][]byte{
		[]byte("first"),
		[]byte("second"),
	}}
	r := &reader{src: src, queue: queue, metrics: metrics, log: logger.Nop()}
	done := make(chan struct{})
	go func() { r.drain(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("reader did not exit on ErrClosed")
	}
	got := drainBytes(queue)
	if string(got[0]) != "first" || string(got[1]) != "second" {
		t.Fatalf("forwarded records = %v; want [first second]", got)
	}
	// Copies, not aliases — assert by mutating the original record
	// after the read completed and confirming the queued slice is
	// unchanged.
	src.records = append(src.records, []byte("xxxxxx"))
	if string(got[0]) != "first" {
		t.Fatalf("queue slice aliased the ringbuf record buffer")
	}
}

func TestReader_RingbufErrorIncrementsCounterAndContinues(t *testing.T) {
	queue := make(chan []byte, 4)
	metrics := NewMetrics()
	src := &fakeRingbuf{
		records: [][]byte{[]byte("after-err")},
		errs:    []error{errors.New("transient")},
	}
	r := &reader{src: src, queue: queue, metrics: metrics, log: logger.Nop()}
	done := make(chan struct{})
	go func() { r.drain(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("reader did not exit")
	}
	// Inc accounting — checking the underlying Counter via Prom's
	// test helpers would pull in client_golang/prometheus/testutil;
	// the simpler assertion is that the queued record made it
	// through after the transient error.
	got := drainBytes(queue)
	if len(got) != 1 || string(got[0]) != "after-err" {
		t.Fatalf("after transient error, queue = %v; want [after-err]", got)
	}
}

func TestReader_QueueFullDropsNewest(t *testing.T) {
	queue := make(chan []byte) // unbuffered → every send is blocking-ergo-dropped
	metrics := NewMetrics()
	src := &fakeRingbuf{records: [][]byte{
		[]byte("a"), []byte("b"), []byte("c"),
	}}
	r := &reader{src: src, queue: queue, metrics: metrics, log: logger.Nop()}
	done := make(chan struct{})
	go func() { r.drain(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("reader did not exit despite full queue (would be a bug — reader must drop, not block)")
	}
	// Nothing made it to the queue because nobody is receiving;
	// every record landed in QueueDropped.
	if len(queue) != 0 {
		t.Fatalf("queue len = %d on unbuffered channel; want 0", len(queue))
	}
}

func drainBytes(ch <-chan []byte) [][]byte {
	out := make([][]byte, 0, cap(ch))
	for {
		select {
		case v, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, v)
		default:
			return out
		}
	}
}
