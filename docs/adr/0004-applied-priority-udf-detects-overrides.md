# Applied priority lives in its own Ticket UDF and detects priority overrides

When priority follows severity, the receiver records the priority it last applied in a dedicated Ticket UDF ("Applied Priority"). A ticket whose Autotask priority differs from that value has had a priority override: the receiver writes a one-off note, sets the UDF to `override`, and never changes that ticket's priority again. We chose this because Autotask is the only state store (ADR 0001), so knowing whether a person changed a priority needs a remembered value on the ticket itself, and a separate UDF keeps it visible to technicians without touching the recorded alerts format that existing tickets already carry.

## Considered Options

- **Encode the applied priority in the Recorded Alerts UDF** (e.g. a `p=4;` prefix): no extra UDF to create, but it changes a format already on every open ticket, interacts with the `overflow` marker, and every reader would need to parse both old and new values.
- **Infer overrides from ticket notes or Autotask ticket history**: no stored value, but notes are human-editable and history is costly to query; ADR 0001 already rejected reconstructing state from notes.
- **Compare without a marker**: would re-detect the same override on every re-evaluation, and an operator setting the priority back to the receiver's value would silently hand control back.

## Consequences

- Priority following is enabled only when the Applied Priority UDF is configured; the UDF must be created in Autotask first.
- An empty UDF means the ticket is not managed, so tickets open before the feature was enabled keep whatever priority they have.
- An override is permanent for the ticket's life, including an operator later choosing the same priority the receiver would have set.
- The priority, applied priority and recorded alerts are changed in one ticket update, after the note, so a partial failure repeats the note on retry rather than losing it (as in ADR 0001).
