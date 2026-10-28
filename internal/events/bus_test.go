package events

import "testing"

func TestPublishReachesAllSubscribers(t *testing.T) {
	b := New()
	c1, _ := b.Subscribe(1)
	c2, _ := b.Subscribe(1)
	b.Publish(Event{Type: "queue.changed"})
	for _, c := range []<-chan Event{c1, c2} {
		if e := <-c; e.Type != "queue.changed" {
			t.Fatalf("got %+v", e)
		}
	}
}

func TestFullSubscriberDropsInsteadOfBlocking(t *testing.T) {
	b := New()
	c, _ := b.Subscribe(1)
	b.Publish(Event{Type: "a"})
	b.Publish(Event{Type: "b"}) // would deadlock if Publish blocked
	if e := <-c; e.Type != "a" {
		t.Fatalf("got %+v", e)
	}
	select {
	case e := <-c:
		t.Fatalf("expected drop, got %+v", e)
	default:
	}
}

func TestCancelClosesAndIsIdempotent(t *testing.T) {
	b := New()
	c, cancel := b.Subscribe(1)
	cancel()
	cancel()
	if _, ok := <-c; ok {
		t.Fatal("channel should be closed")
	}
	b.Publish(Event{Type: "x"}) // must not panic on closed subscriber
}
