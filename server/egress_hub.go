package server

import "sync"

// egressHub fans a wake signal to live egress SSE subscribers. Payloads are
// computed by each subscriber so a slow probe never stalls the publisher.
type egressHub struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

var egressLive = &egressHub{subs: map[chan struct{}]struct{}{}}

// Notify wakes every subscriber. Non-blocking: a full buffer means that client
// already has a pending refresh queued.
func (h *egressHub) Notify() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Subscribe returns a wake channel and an unsubscribe func the caller must defer.
func (h *egressHub) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, ch)
			h.mu.Unlock()
			close(ch)
		})
	}
}
