# Tickets awaiting closure are reopened; completed tickets never are

Some Autotask instances resolve tickets into a status such as Pending Closure, which a workflow rule completes later. With `ticket.resolvedstatus` set to it, the ticket stays open and so stays its group's current ticket, but nothing moved it out of that status when the group fired again, so Autotask completed it while its alerts were firing. With `ticket.reopenstatus` set, a ticket awaiting closure is set to the reopen status when a change note records a newly firing alert. A ticket is awaiting closure when it is in the resolved status with no recorded alerts, which only happens when am-autotask resolved its group or emptied it as an orphan. The rule that a completed ticket is never reopened (ADR-0006) still holds: reopening applies only to tickets Autotask has not yet completed.

## Considered Options

- **A separate "pending" status option**: duplicates `ticket.resolvedstatus`, which can already be any status.
- **Reopen from any status other than Complete**: also covers tickets in Waiting Customer or On Hold, but overrides a status a person chose, unlike priority overrides, which stand.
- **Reopen whenever the ticket is in the resolved status**, including when a technician set it while alerts were still recorded: catches more, but undoes a person's decision that the problem is fixed. Such a ticket is noted as usual and, once completed, the next firing gets a new ticket, as when a technician completes a ticket while alerts fire.
- **Re-read the ticket just before writing**, to avoid reopening one the workflow rule has just completed: narrows the race but cannot close it, since Autotask has no conditional update, and costs a request on every reopen.

## Consequences

- The reopen status is set in the same update as the recorded alerts, after the change note, so a failed update repeats the note on retry and reapplies the status, as the resolved status already is.
- A reopen can race the workflow rule. The Autotask REST API accepts moving a Complete ticket to another status and clears its completed date (confirmed against Autotask in October 2026), so a lost race leaves the ticket open in the reopen status. That suits its firing alerts, but completion side effects such as customer notifications have already run. The window is the few requests between reading the ticket and updating it, against a rule that runs on a schedule, so the risk is accepted.
- The lookback period counts from when Autotask completes the ticket, not from when the group resolved.
- The due date is not reset, so a reopened ticket may already be overdue.
- An emptied orphan awaiting closure is reopened if its old group key fires again, for example after an Alertmanager change is reverted, since it is still that group's current ticket.
- Tickets awaiting closure stay in the orphan scan until completed, enlarging its results but adding no requests.
