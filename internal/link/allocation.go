package link

import "sync"

const AllocationLimit = 512 << 20

type AllocationBudget struct {
	mu      sync.Mutex
	limit   int
	used    int
	changed chan struct{}
}

func NewAllocationBudget(limit int) *AllocationBudget {
	return &AllocationBudget{limit: max(0, min(limit, AllocationLimit)), changed: make(chan struct{})}
}

var allocations = NewAllocationBudget(AllocationLimit)

type Reservation struct {
	budget *AllocationBudget
	n      int
	once   sync.Once
}

func (b *AllocationBudget) Used() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

func (b *AllocationBudget) take(n int) *Reservation {
	if n < 0 || n > b.limit-b.used {
		return nil
	}
	b.used += n
	return &Reservation{budget: b, n: n}
}

func (r *Reservation) Release() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		b := r.budget
		b.mu.Lock()
		b.used -= r.n
		close(b.changed)
		b.changed = make(chan struct{})
		b.mu.Unlock()
	})
}

func (s *Stream) ReserveAllocation(n int, wait bool) *Reservation {
	b := s.l.opts.AllocationBudget
	if b == nil {
		b = allocations
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.changed == nil {
		b.changed = make(chan struct{})
	}
	for {
		select {
		case <-s.Done():
			return nil
		default:
		}
		r := b.take(n)
		if r != nil || !wait || n < 0 || n > b.limit {
			return r
		}
		changed := b.changed
		b.mu.Unlock()
		select {
		case <-changed:
		case <-s.Done():
		}
		b.mu.Lock()
	}
}
