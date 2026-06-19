package overseer

import "sync"

// Topic manages subscribers for a SPECIFIC event schema
type Topic[T any] struct {
	mu          sync.RWMutex
	subscribers []func(Event[T])
}

func (t *Topic[T]) Subscribe(handler func(Event[T])) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.subscribers = append(t.subscribers, handler)
}

func (t *Topic[T]) Publish(event Event[T]) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, sub := range t.subscribers {
		// In-memory execution (consider running in a goroutine depending on blocking needs)
		go sub(event)
	}
}
