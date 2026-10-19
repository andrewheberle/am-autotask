# Recurrences link through problem tickets

A completed ticket is never reopened, so an alert group that fires again soon after its ticket was completed gets a new ticket with nothing tying the two together beyond the "previous ticket" system notice. When a lookback period is configured, a new ticket raised within that period of its previous ticket's completion is a recurrence: the receiver links it to an Autotask Problem ticket, following the previous ticket's problem ticket while that is still open, or raising a new one and linking both tickets to it. Problem tickets are raised once and then left to people; the receiver adds no change notes to them, never changes their priority or status, and gives them no `am:` external ID. We chose Autotask's own problem/incident model because technicians already use it to track an underlying cause across several tickets, and it keeps the link in Autotask, the only state store (ADR-0001).

## Considered Options

- **Reopen the previous ticket within the lookback period**: no extra ticket, but contradicts the rule that a completed ticket is never reopened, loses the record that the first occurrence was resolved, and reopens work that may already be billed.
- **Only a system notice naming the previous ticket** (the behaviour without a lookback period): no extra writes, but nothing groups repeated tickets for someone looking for an underlying cause.
- **Manage the problem ticket like an alert group ticket** (notes, priority following severity, resolving with the group): keeps it current, but duplicates every change note and fights the people working the problem, whose status and priority decisions should stand.
- **Change ticket types as needed** (Alert to Incident) when linking: works with the default ticket type, but changes completed tickets a person may have classified deliberately.

## Consequences

- Autotask only links Incident tickets to a problem, so a lookback period requires `ticket.type` to be Incident (2); the receiver never changes a ticket's type. A previous ticket that is not an Incident, or already belongs to a completed problem ticket, is named on the new problem ticket instead of being linked, so that the completed problem's history is left intact.
- A recurrence is only recognised when its ticket is created. To avoid a second problem ticket on retry, the new ticket is created first, then the problem ticket, then each ticket is linked. If any step after creating the new ticket fails, the notification still succeeds: the failure is logged, counted as a `failed` recurrence and explained in a note on the new ticket for a person to link by hand. A retry would find the new ticket as current and never revisit the link.
- When following an open problem ticket, the new ticket is created already linked, in one request. Raising a problem ticket costs a problem ticket, two links and a note on the new ticket: four extra writes against the shared request limit, only on recurrences.
- Completing a problem ticket through the API does not complete its incidents, and an incident's link survives being completed, so linking a completed previous ticket is safe (confirmed against Autotask in October 2026).
- Problem tickets may be raised against a fixed company rather than the new ticket's; Autotask allows a problem's incidents to belong to other companies.
