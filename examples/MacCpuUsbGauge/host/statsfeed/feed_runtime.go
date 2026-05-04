package main

import "sync"

// feedRuntime — живые параметры потока (трей / окно «Параметры»); rev увеличивается при сохранении — порт переподключается.
type feedRuntime struct {
	mu sync.RWMutex
	rev uint64
	cfg feedConfig
}

func newFeedRuntime(c feedConfig) *feedRuntime {
	return &feedRuntime{cfg: c}
}

func (r *feedRuntime) snapshot() feedConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cfg
}

func (r *feedRuntime) revSnapshot() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.rev
}

func (r *feedRuntime) apply(c feedConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rev++
	r.cfg = c
}
