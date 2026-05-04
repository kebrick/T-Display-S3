package main

import "sync"

// feedRuntime — живые параметры потока (трей / окно «Параметры»); rev увеличивается при сохранении — порт переподключается.
type feedRuntime struct {
	mu  sync.RWMutex
	rev uint64
	cfg feedConfig

	pendMu       sync.Mutex
	pendingBoard []byte // очередь сырых строк на UART (R\n, и т.д.)
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

// EnqueueBoardLine — отправить строку на плату перед следующим тиком (напр. "R\n").
func (r *feedRuntime) EnqueueBoardLine(s string) {
	r.pendMu.Lock()
	defer r.pendMu.Unlock()
	r.pendingBoard = append(r.pendingBoard, []byte(s)...)
}

func (r *feedRuntime) takePendingBoard() []byte {
	r.pendMu.Lock()
	defer r.pendMu.Unlock()
	if len(r.pendingBoard) == 0 {
		return nil
	}
	out := r.pendingBoard
	r.pendingBoard = nil
	return out
}
