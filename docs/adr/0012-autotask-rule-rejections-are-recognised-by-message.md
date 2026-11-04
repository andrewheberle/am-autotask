# Autotask rule rejections are recognised by message, not status

Whether a failed Autotask request is worth retrying decides whether Alertmanager retries a notification and whether a recurrence falls back to an unlinked ticket, so the Autotask client marks errors retrying cannot fix with `ErrPermanent`. HTTP 4xx answers other than 429 are permanent, but Autotask also answers requests that break its rules with HTTP 500, the same status as a deadlock or an outage. Run against Autotask in November 2026, linking a ticket to one that is not a problem ticket answered `The UPDATE statement conflicted with the CHECK constraint "CK_tblTask_child_ticket_count"`, and linking a Service Request to a problem ticket answered `Data violation: Service Request tickets cannot be associated with a Problem Ticket..`, both with HTTP 500. So an HTTP 500 is permanent when one of Autotask's error messages contains a known rule rejection: `Data violation` or `conflicted with the CHECK constraint`. Every other HTTP 500 stays temporary.

## Considered Options

- **Status code only**: what the client did before, assuming Autotask answers rule rejections with 4xx. A recurrence whose open problem ticket refuses another incident then failed as temporary, so Alertmanager retried it indefinitely and the alert group got no ticket.
- **Every HTTP 500 carrying an errors array as permanent**: catches rule rejections not yet seen, but Datto documents lock contention answered with HTTP 500 too, and a deadlock or outage would then never be retried.
- **Falling back to an unlinked ticket on any error when creating a linked one**: no matching needed, but a timeout after Autotask created the linked ticket would raise a second one.

## Consequences

- A new kind of rule rejection is temporary until its message is added to the list in `internal/autotask/client.go`; until then it shows as `am_autotask_autotask_errors_total{kind="temporary"}` and a notification retried indefinitely.
- The messages are Autotask's own wording, and two of the three seen are SQL Server's, so a change on Autotask's side can stop one matching. The fake Autotask server in the client's tests answers rejected links with the messages seen, so the contract both fakes are held to (`internal/receiver/receivertest`) checks they stay permanent.
