package agent

import "sync"

// MessageQueue is shared by the UI and the running agent. Steering is consumed
// at model boundaries; follow-ups are started by the UI after a successful run.
type MessageQueue struct {
	mu        sync.Mutex
	steering  []string
	followUps []string
}

func (q *MessageQueue) Add(text string, followUp bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if followUp {
		q.followUps = append(q.followUps, text)
	} else {
		q.steering = append(q.steering, text)
	}
}

func (q *MessageQueue) DrainSteering() []Message {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	messages := make([]Message, 0, len(q.steering))
	for _, text := range q.steering {
		messages = append(messages, Message{Role: RoleUser, Content: text})
	}
	q.steering = nil
	return messages
}

func (q *MessageQueue) PopNext() string {
	if q == nil {
		return ""
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, pending := range []*[]string{&q.steering, &q.followUps} {
		if len(*pending) > 0 {
			text := (*pending)[0]
			*pending = (*pending)[1:]
			return text
		}
	}
	return ""
}

func (q *MessageQueue) Len() int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.steering) + len(q.followUps)
}
