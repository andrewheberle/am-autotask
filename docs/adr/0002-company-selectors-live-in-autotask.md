# Company selectors live in Autotask and match group labels only

Which company an alert group belongs to is decided by company selectors stored in a Company UDF in Autotask (one Alertmanager-syntax label selector per line), matched against the Notification's `groupLabels` only. This keeps routing owned by the people who manage companies in Autotask rather than in this service's config or in Prometheus, and matching only on group labels guarantees every alert in a group agrees on the routing labels, so a group cannot flip company as alerts join or leave.

## Considered Options

- **Autotask company ID as a Prometheus label**: no lookup, but Autotask IDs leak into monitoring config and routing changes need Prometheus redeploys.
- **Mapping file in this service**: readable labels, but routing lives outside Autotask and needs a redeploy to change.
- **Matching on `commonLabels`**: more lenient, but the match can change as the group's membership changes.

## Consequences

- A label must be in Alertmanager's `group_by` to be usable for routing.
- No match, or matches from more than one company, sends the ticket to the catch-all company; ambiguity is logged and stated in the ticket description.
- The company is chosen once, at ticket creation; editing selectors never moves an existing ticket.
- Selectors are loaded in bulk and cached in memory with periodic refresh, so lookups never call Autotask on the hot path (except a rate-limited refresh on a miss). The receiver refuses to start if the first load fails.
