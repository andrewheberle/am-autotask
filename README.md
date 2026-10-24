# am-autotask

[![codecov](https://codecov.io/gh/andrewheberle/am-autotask/graph/badge.svg?token=K1fgnzaogT)](https://codecov.io/gh/andrewheberle/am-autotask)
[![GitHub version](https://badge.fury.io/gh/andrewheberle%2Fam-autotask.svg?icon=si%3Agithub)](https://badge.fury.io/gh/andrewheberle%2Fam-autotask)

This is a [webhook receiver](https://prometheus.io/docs/alerting/configuration/#webhook_config) for [Alertmanager](https://github.com/prometheus/alertmanager) that creates tickets in [Autotask](https://www.datto.com/products/autotask-psa/).

Each Alertmanager alert group gets one Autotask ticket, raised against the company the group belongs to. As alerts in the group fire and resolve, internal ticket notes record the change, and when the whole group resolves the ticket is completed.

## How it works

- **One ticket per alert group.** A group's ticket is found by its Autotask `externalID`, which is `am:` followed by a hash of Alertmanager's group key. The receiver keeps no database of its own: all state lives in Autotask (see [ADR-0001](docs/adr/0001-autotask-is-the-only-state-store.md)).
- **Choosing the company.** Each company in Autotask can hold one or more *company selectors* in a Company UDF. They are matched against the notification's `groupLabels`. A group matched by no company, or by more than one, is raised against the configured *catch-all company*; an ambiguous match is explained in the ticket description (see [ADR-0002](docs/adr/0002-company-selectors-live-in-autotask.md)).
- **Notes only on change.** A note is added when an alert fires that the ticket hasn't recorded yet, or a recorded alert resolves. Re-sends with nothing new (for example on `repeat_interval`) add nothing. The fingerprints of recorded alerts are kept in a Ticket UDF.
- **Resolution.** When every alert in the group has resolved, a final note is added and the ticket is set to `ticket.resolvedstatus` (default 5, *Complete*).
- **Closed tickets are never reopened.** If a group fires again after its ticket was completed, including when a technician completed it while alerts were still firing, a new ticket is created that references the previous one. With a lookback period, a new ticket raised soon after the previous one was completed is also linked to an Autotask problem ticket; see [Recurrences](#recurrences).
- **Orphaned tickets are handed over.** Changing route matchers, `group_by` or how routes nest gives the affected groups new group keys, leaving their open tickets behind. Alertmanager then sends the alerts again under the new keys. When a firing alert is still recorded on another group's open ticket, that ticket is orphaned: the alert is noted there as moved, naming its new ticket, and is no longer recorded on it. Once all its alerts have moved, the orphaned ticket is set to `ticket.resolvedstatus`. A new ticket that takes over alerts says which ticket it continues.
- **Priority follows severity.** Optionally, a ticket's priority rises and falls with its alert group's severity, for example when an Alertmanager inhibition rule mutes a `warning` alert while the matching `critical` one fires. A person changing the priority takes over from then on. See [Priority and severity](#priority-and-severity).
- **Retries.** Temporary Autotask failures (timeouts, 5xx, 429) return HTTP 500 so Alertmanager retries the notification. Permanent failures (Autotask rejecting the request) are logged with the payload and acknowledged so they are not retried forever.

See [GLOSSARY.md](GLOSSARY.md) for the terms used here.

## Ticket contents

- **Title.** The `summary` annotation when every alert shares it, otherwise the alert name, followed by the remaining group labels, for example `Pod is crash looping {cluster="prod", namespace="payments"}` or `KubePodCrashLooping {cluster="prod", namespace="payments"}`. Severity is left out, since it can change while the ticket is open; the ticket's priority shows it instead.
- **Description.** Any system notices first (see below), then the `summary` and `description` annotations when every alert shares them, then the group labels, a link to the alerts in Alertmanager (when Alertmanager sends its `externalURL`), and each firing alert with its labels, summary, start time and source link. Times are shown in the [local time zone](#functions), such as `2026-10-19 10:15:00 AWST`.
- **Runbooks.** An alert's `runbook_url` annotation is shown so the service desk can follow it. A runbook shared by every alert is shown once, near the top of the description as `Runbook: <url>`; otherwise each alert's runbook is shown under that alert. Notes show the runbooks of newly firing alerts, unless it is the runbook every alert shares, and never for resolved or moved alerts. The value is shown as written, without checking that it is a valid URL.
- **Notes.** Titled with the alert name and the number of alerts that fired and resolved, for example `HostDown: 1 firing, 0 resolved`, or `HostDown resolved: 0 firing, 2 resolved` when the whole group has resolved, and listing each alert. When the ticket's priority follows severity, a note also says when the priority was raised or lowered, or that a person changed it.

- **System notices.** Explanations of something am-autotask did or detected come first in a description or note: a ticket raised against the catch-all company because several companies matched, the group's previous ticket, the orphaned tickets a ticket continues, alerts no longer recorded individually, priority changes, and the problem ticket a ticket is linked to or the tickets a problem ticket links. [Ticket templates](#ticket-templates) cannot remove them (see [ADR-0005](docs/adr/0005-system-notices-stay-outside-ticket-templates.md)).

Text longer than Autotask allows is cut short, ending with `… (truncated)`.

## Ticket templates

The wording of ticket titles, ticket descriptions, change notes (the notes recording alerts firing and resolving) and [problem tickets](#recurrences) can be replaced with [Go templates](https://pkg.go.dev/text/template). Set `templates` to a file that defines any of these blocks:

| Block | Data | Used for |
| --- | --- | --- |
| `ticket.title` | Ticket | Title of a new ticket. Made a single line. |
| `ticket.description` | Ticket | Description of a new ticket, after any system notices. |
| `note.title` | Note | Title of a change note. Made a single line. |
| `note.description` | Note | Description of a change note, after any system notices. |
| `problem.title` | Problem | Title of a problem ticket raised for a recurrence. Made a single line. |
| `problem.description` | Problem | Description of a problem ticket, after any system notices. |

A block the file does not define keeps its [built-in definition](internal/receiver/templates/default.tmpl), so a file can change just the title:

```gotemplate
{{define "ticket.title"}}[{{upper .GroupLabels.tenant_id}}] {{.CommonAnnotations.summary}}{{end}}
```

Other notes, such as the one on an orphaned ticket naming where its alerts moved, keep their fixed wording.

### Template data

These fields are a stable interface: renaming or removing one is a breaking change.

| Field | Data | Description |
| --- | --- | --- |
| `.Receiver` | All | Alertmanager receiver name. |
| `.Status` | All | `firing` or `resolved`, for the whole alert group. |
| `.GroupLabels`, `.CommonLabels`, `.CommonAnnotations` | All | Maps from the notification, e.g. `.GroupLabels.cluster`. |
| `.ExternalURL` | All | Alertmanager's external URL. |
| `.AlertmanagerURL` | All | Link to the receiver's alerts in Alertmanager, or empty without an external URL. |
| `.Severity` | All | The alert group's severity when the template is executed. For a ticket that is when it was raised; it does not change with the ticket's priority later. |
| `.Alerts` | Ticket, Problem | The firing alerts. For a problem ticket, those of the new ticket. |
| `.Fired`, `.Resolved` | Note | Alerts that fired and resolved since the last note. |
| `.GroupResolved` | Note | Set when the whole alert group has resolved. |
| `.TicketNumber` | Note, Problem | The ticket's number, e.g. `T20261019.0001`. For a problem ticket, the new ticket's. |
| `.TicketTitle` | Problem | The new ticket's title, as its template rendered it. |
| `.PreviousTicketNumber` | Problem | The number of the alert group's previous ticket. |

Each alert has `.Name` (its `alertname` label), `.Status`, `.Labels`, `.Annotations`, `.StartsAt`, `.EndsAt` (zero while firing; check with `.EndsAt.IsZero`), `.GeneratorURL` and `.Fingerprint`.

A label or annotation that is not set is an empty string.

### Functions

As well as Go's [built-in functions](https://pkg.go.dev/text/template#hdr-Functions):

| Function | Example | Description |
| --- | --- | --- |
| `labels MAP [EXCLUDE...]` | `{{labels .Labels $.GroupLabels "severity"}}` | Labels in Prometheus selector syntax, sorted, e.g. `{instance="web1", job="node"}`. Each exclusion is a label name or a map whose keys are left out. |
| `time TIME [LAYOUT]` | `{{time .StartsAt}}` | A time in the local time zone, laid out as `2006-01-02 15:04:05 MST` or the given [layout](https://pkg.go.dev/time#pkg-constants). |
| `default DEFAULT VALUE` | `{{default "unknown" .CommonLabels.team}}` | VALUE, or DEFAULT when VALUE is empty. |
| `truncate N STRING` | `{{truncate 80 .CommonAnnotations.summary}}` | STRING cut to N characters, ending with `… (truncated)` when anything was removed. |
| `trim STRING` | `{{trim .Annotations.runbook_url}}` | STRING without leading and trailing whitespace. |
| `upper STRING`, `lower STRING` | `{{upper .GroupLabels.tenant_id}}` | STRING in upper or lower case. |

The local time zone comes from the `TZ` environment variable, such as `TZ=Australia/Perth`, or else the host's time zone; the container image uses UTC unless `TZ` is set. The built-in wording and the fixed notes show times the same way. Time zone data is built in, so `TZ` works on hosts and images without it. An unknown `TZ` stops `serve` at startup, rather than silently falling back to UTC. On Windows `TZ` is ignored and the system time zone is used.

### Errors and previews

Every block in the templates file is executed against a sample notification at startup, and `serve` will not start if one fails. A block that still fails on a real notification, for example by indexing past the end of `.Alerts`, is replaced by its built-in definition for that ticket or note; the error is logged and counted in `am_autotask_template_errors_total`. A title that renders empty also falls back to the built-in definition. Descriptions lose trailing whitespace, and everything is cut to Autotask's limits after rendering.

The templates file is read at startup; restart to apply changes. To see what the templates produce without raising tickets, run:

```sh
am-autotask templates --templates templates.tmpl [--notification webhook.json]
```

This prints every block rendered for a built-in sample notification, or for an Alertmanager webhook payload saved as JSON, treating its firing alerts as a new ticket, all its alerts as a change note, and its ticket as a recurrence raising a problem ticket. System notices are not shown. `templates` is also read from `--config` and `AM_AUTOTASK_TEMPLATES`.

## Autotask setup

1. **Create an API user**, and note its user name, secret and API tracking identifier (integration code). Give it security level *API User (system)*, or an equivalent with access to companies, tickets and ticket notes. If you set `ticket.worktype` or run `am-autotask lookup worktypes`, it also needs read access to billing codes, as Autotask holds work types as billing codes.
2. **Create a Company UDF**, by default named `Alertmanager Selectors`. Use a text field (multi-line, if you want several selectors). This holds each company's company selectors.
3. **Create a Ticket UDF**, by default named `Alertmanager Recorded Alerts`. Use a text field. The receiver manages its contents; it should not be edited by hand.
4. **Choose the catch-all company**, for example your own company, and note its ID. Your own company always has ID `0`. `am-autotask lookup companies` finds it (see [Finding IDs](#finding-ids)).
5. **Optionally, create another Ticket UDF** for the applied priority, for example `Alertmanager Applied Priority`, if ticket priority should follow severity. Use a text field. The receiver manages its contents; it should not be edited by hand. See [Priority and severity](#priority-and-severity).
6. **Find the picklist IDs** to use: the priority for each severity, plus any queue, issue type, sub-issue type, category or work type you want. Configuration always uses numeric IDs, never labels, as labels vary by instance and language. The Autotask UI mostly shows labels, so use `am-autotask lookup picklists` to list the IDs, and `am-autotask lookup worktypes` for work types.

At startup the receiver checks every configured ID against Autotask, along with every configured UDF and the catch-all company, and refuses to start if anything is missing or inactive. A rejected picklist ID is reported with the setting it came from and the active values of that picklist, for example `ticket.queue: Tickets queueID has no value 99; active values are 8 (Service Desk), 29682833 (Monitoring)`. A rejected work type is reported with the active work types in the same way.

### Finding IDs

The `lookup` commands print the IDs that settings take, using the API user from step 1. They read the `autotask.*` settings from the same flags, environment variables and configuration file as `serve`, and ignore every other setting, so they can run before the rest of the configuration is filled in.

`am-autotask lookup picklists` lists the values of every picklist setting, labelled with the settings that use them. Sub-issue types are listed with the issue type they belong to, as `ticket.subissuetype` must belong to `ticket.issuetype`.

```console
$ am-autotask lookup picklists --config config.yaml
ticket.priorities, ticket.defaultpriority (Tickets priority)
  ID  LABEL
  4   Critical
  1   High
  2   Medium
  3   Low

ticket.queue (Tickets queueID)
  ID        LABEL
  8         Service Desk
  29682833  Monitoring
...
```

`am-autotask lookup companies NAME` lists the companies whose name contains `NAME`, ignoring case:

```console
$ am-autotask lookup companies --config config.yaml "xyz group"
ID   NAME
175  XYZ Group Ltd
```

`am-autotask lookup worktypes` lists the work types, for `ticket.worktype`. Work types aren't a picklist in Autotask, so `lookup picklists` doesn't show them:

```console
$ am-autotask lookup worktypes --config config.yaml
ID        NAME
29682805  After Hours Support
29682801  Remote Support
```

All three list only active values, companies and work types, as inactive ones are rejected at startup. `--all` includes inactive ones, with an `ACTIVE` column. With Docker, run the image with `lookup` in place of `serve`:

```sh
docker run --rm -v ./config.yaml:/etc/am-autotask/config.yaml \
  ghcr.io/andrewheberle/am-autotask:v0.10.0 lookup picklists --config /etc/am-autotask/config.yaml # x-release-please-version
```

### Company selectors

A company's selector UDF holds one [Alertmanager label selector](https://prometheus.io/docs/alerting/latest/configuration/#matcher) per line, using `=`, `!=`, `=~` and `!~`. A selector matches when all of its matchers match the alert group's labels; a company matches when any of its selectors does. For example:

```
{tenant_id="foo", site="bar"}
{tenant_id="foo", cluster="xyz"}
```

Only the notification's **group labels** are matched, so any label used in a selector must be in the Alertmanager route's `group_by`. This guarantees every alert in a group agrees on the routing labels, and a group can't change company as alerts join or leave it. The company is chosen once, when the ticket is created.

Selectors are loaded from Autotask at startup and about every `company.refresh` (default 15 minutes), varied by up to 10% so that replicas started together don't reload together. When a group matches no company, they're also reloaded straight away, at most once per `company.missrefresh` (default one minute), so a company tagged a moment ago is picked up. A company whose selectors don't parse is logged and skipped; the rest still load. If a reload fails, the previous selectors stay in use.

## Alertmanager configuration

```yaml
route:
  receiver: autotask
  # labels used in company selectors must be listed here
  group_by: ["alertname", "tenant_id", "site"]

receivers:
  - name: autotask
    webhook_configs:
      - url: http://am-autotask:8080/webhook
        # required: without resolved notifications nothing ever resolves
        send_resolved: true
        http_config:
          authorization:
            type: Bearer
            credentials_file: /etc/alertmanager/am-autotask-token
```

Only webhook payload version 4 is accepted; anything else is rejected with HTTP 400 and logged once.

> [!WARNING]
> A ticket belongs to an Alertmanager group key, which is built from the route's matchers (and its parents') plus the group labels. It does **not** include the receiver name or the route's position. The following configurations therefore cause different alert groups to share tickets:
>
> - **Overlapping routes.** Two routes with identical matchers that both reach am-autotask, for example sibling routes using `continue: true` or two receivers pointing at the same am-autotask, send the same group twice and share one ticket. Route each alert to am-autotask once.
> - **Several instances on one Autotask database.** Instances of am-autotask with different settings writing to the same Autotask database treat each other's tickets as their own. Use one instance per Autotask database.
>
> Sibling routes with identical matchers but different `active_time_intervals` (for example business hours and after hours), where only one is active at a time, are fine: one ongoing problem stays on one ticket across the handover.

## Configuration

Settings come from command line flags, a YAML file named by `--config`, and environment variables prefixed `AM_AUTOTASK_`, loaded with [configger](https://github.com/andrewheberle/configger). Later sources override earlier ones in this order: flag defaults, configuration file, environment variables, flags given on the command line.

Every setting has a flag on `serve`. The environment variable for a setting is its flag name in upper case with `.` replaced by `_`, and its value is read the same way as the flag's. For example, `webhook.token` is `AM_AUTOTASK_WEBHOOK_TOKEN`, and `ticket.priorities` is `AM_AUTOTASK_TICKET_PRIORITIES=critical=4,warning=2`. A map such as `ticket.priorities` set by a higher source replaces the map from a lower one; the two are not merged.

### Secrets

Each secret can be given directly or as a path to a file containing it, which suits Docker and Kubernetes secrets. Leading and trailing whitespace is trimmed from files. Setting both forms of the same secret is an error.

| Setting | File setting | Description |
| --- | --- | --- |
| `webhook.token` | `webhook.tokenfile` | Bearer token Alertmanager must send. Required: the receiver will not start without it. |
| `autotask.username` | `autotask.usernamefile` | Autotask API user name. Required. |
| `autotask.secret` | `autotask.secretfile` | Autotask API secret. Required. |
| `autotask.integrationcode` | `autotask.integrationcodefile` | Autotask API tracking identifier. Required. |

### Settings

| Setting | Default | Description |
| --- | --- | --- |
| `listen` | `:8080` | Address to listen on. |
| `log.level` | `info` | `debug`, `info`, `warn` or `error`. Logs are JSON on stderr. |
| `tracing.exporter` | `none` | `none`, `otlp` or `console`. See [Tracing](#tracing). |
| `autotask.zoneurl` | Autotask's zone information URL | The API zone for the user is discovered here at startup. |
| `company.catchall` | | ID of the catch-all company. Required; `0` is your own company. |
| `company.udf` | `Alertmanager Selectors` | Name of the Company UDF holding company selectors. |
| `company.refresh` | `15m` | How often company selectors are reloaded. Each reload is varied by up to 10%, and the first comes after a random part of the interval. |
| `company.missrefresh` | `1m` | Minimum time between reloads triggered by a group matching no company. |
| `ticket.udf` | `Alertmanager Recorded Alerts` | Name of the Ticket UDF holding recorded alerts. |
| `ticket.priorities` | | Map of severity to priority ID, e.g. `critical: 4`. As a flag or environment variable: `critical=4,warning=2`. |
| `ticket.defaultpriority` | | Priority ID for severities not in `ticket.priorities`. Required. |
| `ticket.severitylabel` | `severity` | Label whose value is looked up in `ticket.priorities`. |
| `ticket.severityorder` | | Severities from `ticket.priorities`, most serious first, e.g. `[critical, warning]`. As a flag or environment variable: `critical,warning`. Optional; when set it must list exactly the severities in `ticket.priorities`. See [Priority and severity](#priority-and-severity). |
| `ticket.appliedpriorityudf` | | Name of the Ticket UDF holding the priority last applied. Optional; setting it makes ticket priority follow severity, and requires `ticket.severityorder`. |
| `ticket.status` | `1` (*New*) | Status ID of new tickets. |
| `ticket.type` | `5` (*Alert*) | Ticket type ID of new tickets. |
| `ticket.source` | `8` (*Monitoring Alert*) | Source ID of new tickets. |
| `ticket.queue` | | Queue ID of new tickets. Optional. |
| `ticket.issuetype` | | Issue type ID of new tickets. Optional. |
| `ticket.subissuetype` | | Sub-issue type ID of new tickets. Optional; requires `ticket.issuetype`. |
| `ticket.category` | | Ticket category ID. Optional; Autotask's default category applies when unset. |
| `ticket.worktype` | | Work type ID of new tickets, sent as the ticket's `billingCodeID`. Optional; when unset none is sent, and Autotask or the ticket category decides. Set only when a ticket is created, so a work type changed by hand is kept. |
| `ticket.duein` | | Due date of new tickets relative to creation, e.g. `4h`. Optional, but Autotask requires a due date unless the ticket category supplies one. |
| `ticket.resolvedstatus` | `5` (*Complete*) | Status ID set when the whole group resolves. `0` leaves the status unchanged. |
| `problem.lookback` | | How soon after a group's previous ticket was completed a new ticket counts as a [recurrence](#recurrences), e.g. `72h`. Optional; `0` or unset disables recurrences. Requires `ticket.type` 2 (*Incident*). |
| `problem.company` | | ID of the company problem tickets are raised against. Optional; `0` is your own company. When unset, a problem ticket is raised against the new ticket's company. |
| `problem.queue` | | Queue ID of problem tickets. Optional; when unset, a problem ticket takes the new ticket's queue. |
| `note.type` | `1` (*Task Summary*) | Note type ID of ticket notes. |
| `note.publish` | `2` (*Internal Project Team*) | Publish (visibility) ID of ticket notes. |
| `templates` | | Path to a file of [ticket templates](#ticket-templates). Optional; the built-in wording applies when unset. |
| `leaderelection.enabled` | `false` | Run as one of several replicas in Kubernetes. See [Multiple replicas](#multiple-replicas). |
| `leaderelection.address` | | Address other replicas forward notifications to while this one leads, as `host:port`: the pod IP and the `listen` port. Required with `leaderelection.enabled`. |
| `leaderelection.lease` | `am-autotask` | Name of the Lease. |
| `leaderelection.namespace` | the pod's namespace | Namespace of the Lease. |
| `leaderelection.identity` | the host name | Name of this replica on the Lease. In Kubernetes the host name is the pod name. |

The defaults for status, type, source, note type and publish are Autotask system values, so they exist on every instance. Priorities, queues, issue types, categories and work types differ between instances, so they have no defaults.

### Example

```yaml
webhook:
  tokenfile: /run/secrets/webhook-token
autotask:
  usernamefile: /run/secrets/autotask-username
  secretfile: /run/secrets/autotask-secret
  integrationcodefile: /run/secrets/autotask-integrationcode
company:
  catchall: 175
ticket:
  priorities:
    critical: 4
    warning: 2
  defaultpriority: 3
  queue: 8
  duein: 4h
```

## Priority and severity

A ticket's priority comes from its alert group's severity: the value of the `ticket.severitylabel` label looked up in `ticket.priorities`, or `ticket.defaultpriority` for a severity that isn't listed.

**Without `ticket.severityorder`**, the severity is the label shared by every alert in the notification. A group mixing severities, such as one instance `critical` and another `warning`, gets `ticket.defaultpriority`. The priority is set once, when the ticket is created.

**With `ticket.severityorder`**, the severity is the highest among the group's firing alerts, ranked by that list; severities not in it rank lowest. A group mixing `critical` and `warning` alerts is raised at the `critical` priority.

**With `ticket.appliedpriorityudf` as well**, the priority also follows the severity after creation:

- Whenever a notification records an alert firing or resolving, the priority is set from the highest severity then firing, and the note says it was raised or lowered. Alerts that leave the notification without resolving, because they are silenced or inhibited, change nothing, so silencing a `critical` alert while working on it does not lower the ticket.
- This suits an inhibition rule that mutes the `warning` alert while the `critical` one for the same target fires. The ticket is raised when the `critical` fires and lowered again when it resolves and the `warning` returns. Both alerts must be in the same alert group, so `severity` must not be in the route's `group_by`; otherwise each severity gets its own ticket.
- Silencing the `critical` alert never lowers the ticket, with or without such a rule. Alertmanager still inhibits the `warning` while the `critical` is silenced, so the group sends nothing. Without the rule, Alertmanager only notifies when an alert joins the group, so nothing is sent until `repeat_interval`, and that notification changes nothing because no alert fired or resolved. The ticket is lowered when the `critical` resolves; to lower it sooner, change its priority in Autotask, which takes over from then on as below.
- When the whole group resolves, or once an alert group has too many alerts to track individually, the priority is left as it is.
- **A person changing the priority takes over for the rest of the ticket's life.** The receiver records the priority it last set in the UDF. If the ticket's priority no longer matches, it adds a note saying the priority was changed manually, sets the UDF to `override`, and never changes that ticket's priority again, even if it is later set back. See [ADR-0004](docs/adr/0004-applied-priority-udf-detects-overrides.md).
- Tickets opened before `ticket.appliedpriorityudf` was set have an empty UDF, and their priority is left alone.
- The due date set by `ticket.duein` is not changed. Autotask may recalculate service level targets itself when the priority changes, depending on how the instance's service level agreements are set up.

```yaml
ticket:
  priorities:
    critical: 4
    warning: 2
  severityorder: [critical, warning]
  appliedpriorityudf: Alertmanager Applied Priority
  defaultpriority: 3
```

## Recurrences

Closed tickets are never reopened. When an alert group fires again soon after its ticket was completed, set `problem.lookback` to link the new ticket and the previous one through an Autotask problem ticket, so the service desk can see they share a cause. The new ticket is a recurrence when the group's most recent completed ticket was completed within the lookback period. Only that ticket counts, and only tickets of the same alert group.

- **The previous ticket is linked to an open problem ticket.** The new ticket is created linked to the same problem ticket, and its description says so.
- **Otherwise**, a problem ticket is raised and both tickets are linked to it. The new ticket gets a note naming the problem ticket. The previous ticket stays complete; linking it does not reopen it.
- **The previous ticket's problem ticket is complete.** A new problem ticket is raised, but the previous ticket stays linked to the completed one, which the new problem ticket's description names.
- **The previous ticket is not an Incident**, for example because it was raised before `ticket.type` was changed. Only the new ticket is linked, and the problem ticket's description names the previous ticket.

A problem ticket is titled `Repeated alert:` followed by the new ticket's title, and its description says which tickets it links, then describes the alert group as the new ticket does. Both can be changed with the `problem.title` and `problem.description` [ticket templates](#ticket-templates). It takes the new ticket's priority, status, source, issue type, sub-issue type, category, work type and due date when it is raised, and its company and queue unless `problem.company` and `problem.queue` are set. After that am-autotask leaves it alone: it adds no notes to it and never changes its priority or status, and completing it does not complete the tickets linked to it.

Autotask only links Incident tickets to a problem ticket, so `problem.lookback` requires `ticket.type` 2 (*Incident*), and am-autotask never changes a ticket's type. A problem ticket can hold up to 500 incidents.

If linking fails, the new ticket is still raised, and gets a note titled `Not linked to a problem ticket` explaining what to link by hand. The failure is logged and counted in `am_autotask_recurrences_total`. am-autotask only links a recurrence when its ticket is raised, so it won't try again. See [ADR-0006](docs/adr/0006-recurrences-link-through-problem-tickets.md).

```yaml
ticket:
  type: 2
problem:
  lookback: 72h
  queue: 29
```

A recurrence costs a few more Autotask requests: one to read the previous ticket's problem ticket, if it has one, and five to raise a problem ticket (creating it and reading back its number, linking the two tickets, and the note on the new ticket).

## Tracing

am-autotask can trace each notification with [OpenTelemetry](https://opentelemetry.io/). Tracing is off until `tracing.exporter` is set (see [ADR-0003](docs/adr/0003-tracing-switch-in-configger-otel-settings-in-env.md)):

- `otlp` exports spans with OTLP over HTTP (`http/protobuf`). If `OTEL_EXPORTER_OTLP_PROTOCOL` or `OTEL_EXPORTER_OTLP_TRACES_PROTOCOL` is set to anything else, such as `grpc`, am-autotask refuses to start.
- `console` writes spans as JSON to stdout, for trying it out.

Everything else uses the standard [OpenTelemetry environment variables](https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/):

| Variable | Default | Description |
| --- | --- | --- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | `http://localhost:4318` | Collector to export to. |
| `OTEL_EXPORTER_OTLP_HEADERS` | | Headers to send, e.g. for authentication. |
| `OTEL_EXPORTER_OTLP_CERTIFICATE`, `OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE`, `OTEL_EXPORTER_OTLP_CLIENT_KEY` | | TLS settings for the collector. |
| `OTEL_TRACES_SAMPLER`, `OTEL_TRACES_SAMPLER_ARG` | `parentbased_always_on` | Sampling. By default each notification follows Alertmanager's sampling decision, and is sampled when Alertmanager sends none. |
| `OTEL_SERVICE_NAME` | `am-autotask` | Service name. |
| `OTEL_RESOURCE_ATTRIBUTES` | | Extra resource attributes, e.g. `deployment.environment.name=prod`. `service.version` is set to the am-autotask version. |

`OTEL_TRACES_EXPORTER`, `OTEL_PROPAGATORS` and `OTEL_SDK_DISABLED` are not used: `tracing.exporter` chooses the exporter, and W3C trace context is always used.

Alertmanager sends W3C trace context with each notification when its own [tracing](https://prometheus.io/docs/alerting/latest/configuration/#tracing_config) is enabled, so a notification's trace starts in Alertmanager and continues into am-autotask. Each trace has:

- a `POST /webhook` span, then a `handle notification` span recording the outcome (`created`, `updated`, `unchanged` or `ignored`), the ticket, the company and any catch-all reason, the numbers of firing, resolved and moved alerts, and whether a failure was temporary or permanent;
- spans for waiting for the alert group's lock, matching the company and handing alerts over from orphaned tickets;
- an `autotask <operation>` span for each Autotask operation, with a span for each HTTP request it makes.

Alert labels and annotations are never recorded, and Autotask URLs are recorded without their query strings, which carry the API user name and search filters. Trace context is not sent to Autotask. Company selector reloads are traced too: periodic reloads as their own traces, and a reload triggered by an alert group matching no company as part of that notification's trace.

Log lines written while handling a notification carry its `trace_id` and `span_id`. Export failures are logged at `warn`, at most once a minute; tracing never delays or fails a notification.

## Running

The receiver runs as the `serve` subcommand, which takes the flags under [Configuration](#configuration); the `lookup` commands are described under [Finding IDs](#finding-ids), and the `templates` command under [Ticket templates](#errors-and-previews). Before v0.5.0 the receiver ran without a subcommand, so add `serve` before any existing arguments when upgrading. `am-autotask --version` prints the version.

```sh
am-autotask serve --config config.yaml
```

### Docker

```sh
docker run -v ./config.yaml:/etc/am-autotask/config.yaml \
  ghcr.io/andrewheberle/am-autotask:v0.10.0 serve --config /etc/am-autotask/config.yaml # x-release-please-version
```

### Examples

- [Docker Compose](examples/compose/): am-autotask with Alertmanager, Prometheus and Grafana, including the self-monitoring rules and the dashboard.
- [Kubernetes](examples/kubernetes/): a Deployment, Service and optional ServiceMonitor, applied with Kustomize.
- [Grafana dashboard](examples/grafana/am-autotask.json): notifications, Autotask requests and tickets at a glance. See [Monitoring](docs/monitoring.md#dashboard).

### Multiple replicas

In Kubernetes, am-autotask can run as several replicas so that a pod failing, or a rollout, doesn't stop alerts becoming tickets. One replica, the leader, handles every notification; the others are standbys. Any replica can receive a notification from the Service, and a standby forwards it to the leader. The leader is chosen with a Kubernetes [Lease](https://kubernetes.io/docs/concepts/architecture/leases/), so nothing besides Autotask and the Kubernetes API is needed (see [ADR-0007](docs/adr/0007-replicas-elect-a-leader-through-a-kubernetes-lease.md)).

- Set `leaderelection.enabled` and `leaderelection.address`, the pod IP and listen port, which the [Kubernetes example](examples/kubernetes/) does with the downward API.
- The pod's service account needs a Role allowing it to `get`, `create` and `update` Leases in its namespace.
- am-autotask refuses to start with leader election enabled outside a Kubernetes pod.
- Every replica connects to Autotask, validates its configuration and loads company selectors before it is ready or campaigns for the Lease, so a standby can take over at once.
- The leader renews the Lease every 2 seconds. If it cannot for 10 seconds it stops handling notifications, cancelling any in progress so Alertmanager retries them, and becomes a standby. Standbys take over once the Lease has gone unrenewed for 15 seconds.
- On shutdown the leader finishes the notifications in progress, then releases the Lease so a standby takes over straight away.
- While no replica leads, notifications are answered with `503` and Alertmanager retries them.
- Standbys reload company selectors too, so each replica adds to Autotask's request count; see [Limitations](#limitations).

Without `leaderelection.enabled` am-autotask runs as a single instance, as with Docker Compose. Don't run more than one instance without it: they would handle the same alert groups at once.

### Endpoints

| Path | Description |
| --- | --- |
| `POST /webhook` | Alertmanager webhook receiver. Requires `Authorization: Bearer <webhook.token>`. |
| `GET /healthz` | Always `200` while the process is running. |
| `GET /readyz` | `200` once Autotask has been reached, configuration validated and company selectors loaded. Until then notifications are answered with `500`, so Alertmanager retries them. With leader election, `503` also while the Kubernetes API can't be reached. |
| `GET /metrics` | Prometheus metrics, prefixed `am_autotask_`: notifications by result, tickets and notes created, alerts moved off orphaned tickets and orphaned tickets completed, tickets raised against the catch-all company, Autotask errors and request durations by operation, ticket templates that failed, recurrences by outcome, readiness, leadership and notifications forwarded to the leader, and the age and validity of the company selectors. See [Monitoring](docs/monitoring.md) for what they mean, ready-made alerting rules and a Grafana dashboard. |

## Limitations

- Only one instance handles notifications at a time. Handling of each alert group is serialised in-process, and [multiple replicas](#multiple-replicas) are supported in Kubernetes only, as standbys that forward to a leader rather than sharing the work.
- Autotask cannot reject a write made from stale data, so a leader that stalls (for example a long pause or a network partition) just as another replica takes over may finish a notification it had already started. That can add a duplicate note, or lose one change to the recorded alerts so that the next notification's notes are off. Duplicate tickets are found and retired as below.
- If more than one open ticket is ever found for a group, the oldest is used and each other one gets a note naming it and has its `externalID` changed to `am:dup:…` so it is warned only once. Merging is left to a person.
- The Ticket UDF holds the fingerprints of about 450 alerts. Beyond that a group stops recording individual alerts: a note says so, further changes are not noted, and the group resolving still completes the ticket.
- Orphaned tickets are only handed over for alerts that fire again under a new group key. A ticket is left open, and must be completed by hand, if its alerts resolve within `group_wait` of the configuration change, are removed from Alertmanager, or it had too many alerts to record them individually.
- Each notification with firing alerts makes one extra Autotask query to look for orphaned tickets, returning every open am-autotask ticket.
- An alert only counts as resolved when Alertmanager sends it with `status: resolved`; an alert missing from a notification stays recorded until then, or until the whole group resolves.
- Autotask limits API use to 10,000 requests an hour per database, shared with every other integration, and three concurrent requests per endpoint. The receiver makes at most three concurrent requests.
