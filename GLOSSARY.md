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

**Recorded Alerts**:
The firing alerts a ticket has already noted, against which each notification is compared to find what fired and what resolved.
_Avoid_: Known alerts, alert state, cache

**Ticket Note**:
An entry on a ticket recording a change in its alert group's membership or state.
_Avoid_: Comment, update
