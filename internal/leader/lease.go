// Package leader elects one replica to handle notifications, using a
// Kubernetes Lease as the only shared state (ADR-0007).
package leader

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrNotFound is returned by [Client.Get] when the Lease does not exist.
	ErrNotFound = errors.New("lease not found")
	// ErrConflict is returned by [Client.Create] when the Lease already
	// exists, and by [Client.Update] when it changed since it was read.
	ErrConflict = errors.New("lease changed concurrently")
)

// Lease is the part of a Kubernetes Lease used for leader election.
type Lease struct {
	// Holder is the identity of the leader, or empty once released.
	Holder string
	// Address is the leader's host:port, which other replicas forward
	// notifications to.
	Address     string
	Duration    time.Duration
	AcquireTime time.Time
	RenewTime   time.Time
	Transitions int32
	// ResourceVersion is the version read, which an update must match.
	ResourceVersion string
}

// Client reads and writes the one Lease used for leader election.
type Client interface {
	Get(ctx context.Context) (Lease, error)
	Create(ctx context.Context, l Lease) (Lease, error)
	Update(ctx context.Context, l Lease) (Lease, error)
}
