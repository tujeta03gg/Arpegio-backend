// Package events is the in-memory pub/sub bus domains use to talk to each other.
package events

import "sync"

// Event is a typed message such as "library.file_added".
type Event struct {
	Type string
	Data any
}

// Bus fans events out to subscribers. Publish never blocks: a subscriber
// whose buffer is full drops the event (state is always re-readable from the DB).
type Bus struct {
	mu   sync.RWMutex
	subs map[chan Event]struct{}
}

func New() *Bus { return &Bus{subs: map[chan Event]struct{}{}} }

// Subscribe returns a channel of events and a cancel func that closes it.
func (b *Bus) Subscribe(buffer int) (<-chan Event, func()) {
	ch := make(chan Event, buffer)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, ch)
			b.mu.Unlock()
			close(ch)
		})
	}
}

func (b *Bus) Publish(e Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
}
