package autotask_test

import (
	"testing"
	"time"

	"github.com/andrewheberle/am-autotask/internal/autotask"
	"github.com/andrewheberle/am-autotask/internal/receiver/receivertest"
)

func TestClientKeepsTheContract(t *testing.T) {
	receivertest.TestAutotask(t, func(t *testing.T) receivertest.Harness {
		f := newFakeServer(t)
		return receivertest.Harness{
			Autotask: newClientWith(t, f, autotask.Options{AppliedPriorityUDF: appliedUDF}),
			Complete: func(t *testing.T, id int64, completed time.Time) {
				t.Helper()
				f.complete(t, int(id), completed)
			},
		}
	})
}
