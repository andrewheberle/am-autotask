# am-autotask

Turns Alertmanager notifications into Autotask tickets, one ticket per alert group, raised against the company the alerts belong to.

## Alertmanager side

**Alert**:
A single firing or resolved condition reported by Prometheus, identified by its fingerprint.
_Avoid_: Event, alarm

**Alert Group**:
The set of alerts Alertmanager has bundled together for one receiver, identified by its group key. Alerts join the group by firing and leave it by resolving.
_Avoid_: Incident, alert batch

**Notification**:
One webhook delivery from Alertmanager describing the current state of an alert group.
_Avoid_: Webhook call, message, event

**Severity**:
How serious an alert says it is, given by one of its labels. An alert group's severity is the highest severity among its firing alerts.
_Avoid_: Level, urgency, priority (for alerts)

**Runbook**:
The documentation an alert links to, telling whoever handles its ticket how to respond.
_Avoid_: Playbook, SOP, KB article

## Autotask side

**Company**:
An Autotask company that a ticket is raised against.
_Avoid_: Client, customer, org, tenant, account

**Company Selector**:
A set of label matchers held on a company; an alert group whose group labels satisfy every matcher belongs to that company. A company may hold several.
_Avoid_: Routing rule, company key, mapping

**Catch-all Company**:
The company that receives tickets for alert groups matched by no company's selectors, or by more than one company's.
_Avoid_: Default company, fallback org

**Ticket**:
The Autotask ticket representing exactly one alert group. A group has at most one open ticket at a time, its **Current Ticket**; once closed, a ticket is never reopened and the group's next firing gets a new one.
_Avoid_: Case, incident

**Priority**:
A ticket's Autotask priority. It follows its alert group's severity, rising and falling with it, until a priority override.
_Avoid_: Severity (for tickets), urgency

**Priority Override**:
A person changing a ticket's priority. From then on, the ticket's priority is never changed automatically for the rest of the ticket's life.
_Avoid_: Manual priority, pinned priority, locked priority

**Orphaned Ticket**:
An open ticket whose alert group no longer receives notifications because its group key changed, recognised when its recorded alerts fire in another alert group.
_Avoid_: Stale ticket, abandoned ticket

**Recorded Alerts**:
The firing alerts a ticket has already noted, against which each notification is compared to find what fired and what resolved.
_Avoid_: Known alerts, alert state, cache

**Ticket Note**:
Any entry am-autotask adds to a ticket.
_Avoid_: Comment, update

**Change Note**:
A ticket note recording alerts firing or resolving in its alert group, or the whole group resolving.
_Avoid_: Status note, alert note

**System Notice**:
Fixed wording on a ticket or ticket note explaining something am-autotask did or detected, such as raising against the catch-all company or no longer recording alerts. A ticket template cannot remove it.
_Avoid_: Warning, banner

**Ticket Template**:
Operator-supplied wording for a ticket's title and description, or for a change note's title and description. Where none is supplied, the built-in wording applies.
_Avoid_: Format, layout
