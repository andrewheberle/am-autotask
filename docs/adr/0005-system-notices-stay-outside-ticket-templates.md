# System notices stay outside ticket templates

Operators can replace the wording of ticket titles, ticket descriptions and change notes with ticket templates, but system notices (raised against the catch-all company because several companies matched, alerts no longer recorded because of overflow, the previous ticket, the orphaned tickets this one continues, and priority changes or overrides) are written by the receiver itself, ahead of the template's output. We chose this because these notices are how operators find out that something needs fixing, such as a company selector matching too much or a ticket no longer tracking its alerts, and a template that leaves them out would hide the problem with no other sign that it exists.

## Considered Options

- **Pass the notices to templates as data**: the default template renders them, and operators get full control over the wording and position, but a custom template written before a new notice existed, or written carelessly, silently drops it.
- **Make system notes (orphan handover, duplicate ticket) templatable too**: they explain what the receiver did rather than describing alerts, so there is little to gain and the same risk of losing the explanation.

## Consequences

- Templates control only how an alert group and its alerts are presented; the notices' wording and placement change only with a release.
- A new system notice reaches every ticket immediately, whatever templates operators have written.
- The template data does not include the ambiguous companies, previous ticket or continued tickets, since nothing in a template needs them.
- Notices come first in a description or note, so they are read before the alert detail.
