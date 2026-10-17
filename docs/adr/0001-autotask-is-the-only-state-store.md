# Autotask is the only state store

The receiver keeps no local or external database. A group's current ticket is found by querying Autotask for an open ticket whose `externalID` is `am:` plus a truncated sha256 of the group key, and the group's recorded alerts (fingerprints of alerts already noted) live in a Ticket UDF on that ticket. We chose this so that adding replicas later needs no new infrastructure and there is no second copy of state to drift from what technicians see in Autotask.

## Considered Options

- **Embedded store (e.g. bbolt) behind an interface, swapped for Redis/Postgres for replicas**: simpler queries, but replicas would require new infrastructure and the store can disagree with Autotask after manual ticket changes.
- **Reconstructing recorded alerts from ticket notes**: no UDF needed, but notes are human-editable and parsing them is fragile.

## Ticket external ID format

The external ID is `am:` followed by the first 40 lowercase hex characters of sha256 of the group key, 43 characters within Autotask's 50. This format has been fixed since v0.1.0: changing the encoding, length or what is hashed would leave every open ticket unfound. A future change must also look up the old format during a transition period.

- **Collisions** are not a practical concern. 160 bits gives a probability of about n²/2¹⁶¹ across n groups (around 10⁻³⁷ for a million), and only groups with open tickets at the same time would be affected.
- **Hex rather than base32 or base64.** A more compact encoding gains nothing within the 50 character limit; hex is unaffected if Autotask compares strings case-insensitively (base64 would not be) and can be reproduced with `sha256sum`.
- **The group key alone, not the receiver name.** Including the receiver would separate overlapping routes sent to different receivers, but renaming a receiver would then orphan its open tickets, which is surprising. Overlapping routes are documented as unsupported instead. Alertmanager's group key omits the route's position among siblings, so identical sibling routes cannot be told apart either way.

## Consequences

- A ticket is tied to its group key, so changing Alertmanager's routes or `group_by` leaves the affected groups' open tickets unfound; they must be completed by hand.
- Each Notification costs at least one Autotask query; the 10,000 requests/hour database-wide limit is shared with every other integration.
- A Ticket UDF must be created in Autotask before deployment. Its 8,000-character limit caps the recorded alerts at roughly 450; beyond that notes are still written but the diff degrades.
- On a change, the note is written before the UDF is updated, so a partial failure followed by a retry produces a duplicate note rather than a lost one.
- Autotask does not enforce `externalID` uniqueness, so concurrent handling of one group must be serialised by the receiver (an in-process lock now, behind a `Locker` interface for replicas). If duplicates still occur, the oldest open ticket wins; each other one receives a warning note and has its `externalID` changed to `am:dup:` plus the hash, so it is no longer found as current and is warned only once.
