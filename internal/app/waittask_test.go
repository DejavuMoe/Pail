package app

import "context"

// waitTask blocks until task id is final or ctx ends and returns its last
// snapshot.
func (s *Service) waitTask(ctx context.Context, id string) (Task, error) {
	q := &s.q
	q.mu.Lock()
	t := q.tasks[id]
	q.mu.Unlock()
	if t == nil {
		return Task{}, opErr("Wait", ErrNotFound, "no task %q", id)
	}
	select {
	case <-t.done:
	case <-ctx.Done():
		return Task{}, ctx.Err()
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return t.snapshot(), nil
}
