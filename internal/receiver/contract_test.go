package receiver_test

import (
	"testing"
	"time"

	"github.com/andrewheberle/am-autotask/internal/receiver/receivertest"
)

func TestFakeAutotaskKeepsTheContract(t *testing.T) {
	receivertest.TestAutotask(t, func(t *testing.T) receivertest.Harness {
		at := newFakeAutotask()
		return receivertest.Harness{
			Autotask: at,
			Complete: func(_ *testing.T, id int64, completed time.Time) { at.closeTicketAt(id, completed) },
		}
	})
}
