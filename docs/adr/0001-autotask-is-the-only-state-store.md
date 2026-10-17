# Autotask is the only state store

The receiver keeps no local or external database. A group's current ticket is found by querying Autotask for an open ticket whose `externalID` is `am:` plus a truncated sha256 of the group key, and the group's recorded alerts (fingerprints of alerts already noted) live in a Ticket UDF on that ticket. We chose this so that adding replicas later needs no new infrastructure and there is no second copy of state to drift from what technicians see in Autotask.

## Considered Options

- **Embedded store (e.g. bbolt) behind an interface, swapped for Redis/Postgres for replicas**: simpler queries, but replicas would require new infrastructure and the store can disagree with Autotask after manual ticket changes.
- **Reconstructing recorded alerts from ticket notes**: no UDF needed, but notes are human-editable and parsing them is fragile.

## Consequences

- Each Notification costs at least one Autotask query; the 10,000 requests/hour database-wide limit is shared with every other integration.
- A Ticket UDF must be created in Autotask before deployment. Its 8,000-character limit caps the recorded alerts at roughly 450; beyond that notes are still written but the diff degrades.
- On a change, the note is written before the UDF is updated, so a partial failure followed by a retry produces a duplicate note rather than a lost one.
- Autotask does not enforce `externalID` uniqueness, so concurrent handling of one group must be serialised by the receiver (an in-process lock now, behind a `Locker` interface for replicas). If duplicates still occur, the oldest open ticket wins; each other one receives a warning note and has its `externalID` changed to `am:dup:` plus the hash, so it is no longer found as current and is warned only once.
