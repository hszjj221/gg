package app

import "encoding/json"

type retainedEvent struct {
	event Event
	bytes int
}

// eventReplay is a bounded transport cache. Its owner provides synchronization;
// eviction never changes the execution outcome held by Run.
type eventReplay struct {
	events    []retainedEvent
	head      int
	count     int
	bytes     int
	maxEvents int
	maxBytes  int
}

func (b *eventReplay) firstSequence(lastSequence int64) int64 {
	if b.count == 0 {
		return lastSequence + 1
	}
	return b.events[b.head].event.Sequence
}

func (b *eventReplay) after(sequence, lastSequence int64) ([]Event, error) {
	oldest := b.firstSequence(lastSequence)
	if sequence < oldest-1 {
		return nil, errorf(ErrorEventHistoryExpired, true, "run event history before sequence %d has expired", oldest)
	}
	start := 0
	if sequence >= oldest {
		start = int(min(sequence-oldest+1, int64(b.count)))
	}
	if start == b.count {
		return nil, nil
	}
	events := make([]Event, b.count-start)
	for i := range events {
		events[i] = b.events[(b.head+start+i)%len(b.events)].event
	}
	return events, nil
}

func (b *eventReplay) append(event Event) {
	encoded, _ := json.Marshal(event)
	size := len(encoded)
	for b.count > 0 && ((b.maxEvents > 0 && b.count >= b.maxEvents) || (b.maxBytes > 0 && b.bytes+size > b.maxBytes)) {
		b.drop()
	}
	// A single oversized event expires replay. Transports reload a snapshot.
	if b.maxBytes > 0 && size > b.maxBytes {
		return
	}
	if b.count == len(b.events) {
		capacity := max(16, len(b.events)*2)
		if b.maxEvents > 0 {
			capacity = min(capacity, b.maxEvents)
		}
		grown := make([]retainedEvent, capacity)
		for i := 0; i < b.count; i++ {
			grown[i] = b.events[(b.head+i)%len(b.events)]
		}
		b.events, b.head = grown, 0
	}
	index := (b.head + b.count) % len(b.events)
	b.events[index] = retainedEvent{event: event, bytes: size}
	b.count++
	b.bytes += size
}

func (b *eventReplay) drop() {
	b.bytes -= b.events[b.head].bytes
	b.events[b.head] = retainedEvent{}
	b.head = (b.head + 1) % len(b.events)
	b.count--
}
