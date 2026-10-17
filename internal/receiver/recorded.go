package receiver

import (
	"maps"
	"slices"
	"strings"
)

// maxRecordedAlerts is the length limit of the recorded alerts ticket UDF.
const maxRecordedAlerts = 8000

// recordedOverflow is stored instead of the fingerprints once they no longer
// fit; it cannot be mistaken for a fingerprint as those are hexadecimal.
const recordedOverflow = "overflow"

const overflowNotice = "This alert group has too many alerts to track individually; " +
	"further alerts firing or resolving will not be noted until the whole group resolves."

// encodeRecorded renders fingerprints for the recorded alerts UDF: sorted,
// de-duplicated and comma separated. It reports overflow, and returns
// [recordedOverflow], when they do not fit.
func encodeRecorded(fps []string) (value string, overflow bool) {
	fps = slices.Clone(fps)
	slices.Sort(fps)
	value = strings.Join(slices.Compact(fps), ",")
	if len(value) > maxRecordedAlerts {
		return recordedOverflow, true
	}
	return value, false
}

// decodeRecorded parses the recorded alerts UDF into a set of fingerprints.
func decodeRecorded(value string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, fp := range strings.Split(value, ",") {
		if fp = strings.TrimSpace(fp); fp != "" {
			set[fp] = struct{}{}
		}
	}
	return set
}

// diff compares a notification's alerts with a ticket's recorded alerts,
// returning those that fired or resolved since and the updated recorded set.
// Only an explicit resolved status removes an alert; one missing from the
// notification stays recorded. Once recording has overflowed individual
// changes cannot be told apart, so ok is false unless the whole group
// resolved, in which case every alert counts as resolved.
func diff(recordedValue string, alerts []Alert, groupResolved bool) (fired, resolved []Alert, recorded map[string]struct{}, ok bool) {
	if recordedValue == recordedOverflow {
		if !groupResolved {
			return nil, nil, nil, false
		}
		return nil, alerts, map[string]struct{}{}, true
	}

	recorded = decodeRecorded(recordedValue)
	for _, a := range alerts {
		_, known := recorded[a.Fingerprint]
		switch {
		case a.Status == statusFiring && !known:
			fired = append(fired, a)
			recorded[a.Fingerprint] = struct{}{}
		case a.Status == statusResolved && known:
			resolved = append(resolved, a)
			delete(recorded, a.Fingerprint)
		}
	}

	return fired, resolved, recorded, true
}

func keys(set map[string]struct{}) []string {
	return slices.Collect(maps.Keys(set))
}
