package receiver

import (
	"context"
	"sync"
)

// Locker serialises handling of notifications for the same alert group.
type Locker interface {
	// Lock blocks until the key is held or ctx is done, returning a
	// function that releases it.
	Lock(ctx context.Context, key string) (unlock func(), err error)
}

// MemoryLocker is a [Locker] for a single receiver instance.
type MemoryLocker struct {
	mu    sync.Mutex
	locks map[string]*keyLock
}

type keyLock struct {
	ch      chan struct{}
	waiters int
}

// NewMemoryLocker returns an in-process [Locker].
func NewMemoryLocker() *MemoryLocker {
	return &MemoryLocker{locks: map[string]*keyLock{}}
}

// Lock implements [Locker].
func (l *MemoryLocker) Lock(ctx context.Context, key string) (func(), error) {
	l.mu.Lock()
	kl, ok := l.locks[key]
	if !ok {
		kl = &keyLock{ch: make(chan struct{}, 1)}
		l.locks[key] = kl
	}
	kl.waiters++
	l.mu.Unlock()

	select {
	case kl.ch <- struct{}{}:
		return func() { l.release(key, kl) }, nil
	case <-ctx.Done():
		l.done(key, kl)
		return nil, ctx.Err()
	}
}

func (l *MemoryLocker) release(key string, kl *keyLock) {
	<-kl.ch
	l.done(key, kl)
}

// done drops a waiter and forgets the key once nobody holds or wants it.
func (l *MemoryLocker) done(key string, kl *keyLock) {
	l.mu.Lock()
	defer l.mu.Unlock()
	kl.waiters--
	if kl.waiters == 0 {
		delete(l.locks, key)
	}
}
