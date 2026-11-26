package server

import (
	"strconv"
	"sync"

	"github.com/Autumn-27/artex/db"
)

// Broadcaster is a per-task in-process pub/sub for live activity events. The
// engine publishes each appended activity at its single emit point; SSE handlers
// subscribe per task. Storage (activity table) and the live stream come from the
// same Publish call, so they never diverge.
type Broadcaster struct {
	mu   sync.Mutex
	subs map[string]map[chan db.Activity]struct{} // task id -> set of subscriber channels
	sys  map[string]map[chan db.SystemEvent]struct{}
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		subs: map[string]map[chan db.Activity]struct{}{},
		sys:  map[string]map[chan db.SystemEvent]struct{}{},
	}
}

// Subscribe returns a buffered channel of activities for a task plus an
// unsubscribe func the caller must invoke (defer) to release it.
func (b *Broadcaster) Subscribe(task string) (<-chan db.Activity, func()) {
	ch := make(chan db.Activity, 256)
	b.mu.Lock()
	if b.subs[task] == nil {
		b.subs[task] = map[chan db.Activity]struct{}{}
	}
	b.subs[task][ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			if m := b.subs[task]; m != nil {
				delete(m, ch)
				if len(m) == 0 {
					delete(b.subs, task)
				}
			}
			b.mu.Unlock()
			close(ch)
		})
	}
}

// Publish fans an activity out to all subscribers of a task. Non-blocking: if a
// subscriber's buffer is full the event is dropped — the client reconnects with
// its last seq cursor and catches up the gap from the DB, so liveness never
// stalls the engine.
func (b *Broadcaster) Publish(task string, a db.Activity) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs[task] {
		select {
		case ch <- a:
		default:
		}
	}
}

// SubscribeSystem returns a buffered channel of system events for a task plus an
// unsubscribe func the caller must invoke (defer). Same lossy semantics as
// Subscribe: a full buffer drops the event, the agent_events table is the source
// of truth the client replays from.
func (b *Broadcaster) SubscribeSystem(taskID int64) (<-chan db.SystemEvent, func()) {
	task := strconv.FormatInt(taskID, 10)
	ch := make(chan db.SystemEvent, 64)
	b.mu.Lock()
	if b.sys[task] == nil {
		b.sys[task] = map[chan db.SystemEvent]struct{}{}
	}
	b.sys[task][ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			if m := b.sys[task]; m != nil {
				delete(m, ch)
				if len(m) == 0 {
					delete(b.sys, task)
				}
			}
			b.mu.Unlock()
			close(ch)
		})
	}
}

// PublishSystem fans a system event out to all swarm-stream subscribers of the
// event's task. Non-blocking, like Publish.
func (b *Broadcaster) PublishSystem(ev db.SystemEvent) {
	task := strconv.FormatInt(ev.Task, 10)
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.sys[task] {
		select {
		case ch <- ev:
		default:
		}
	}
}
