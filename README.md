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
- **Closed tickets are never reopened.** If a group fires again after its ticket was completed, including when a technician completed it while alerts were still firing, a new ticket is created that references the previous one.
- **Orphaned tickets are handed over.** Changing route matchers, `group_by` or how routes nest gives the affected groups new group keys, leaving their open tickets behind. Alertmanager then sends the alerts again under the new keys. When a firing alert is still recorded on another group's open ticket, that ticket is orphaned: the alert is noted there as moved, naming its new ticket, and is no longer recorded on it. Once all its alerts have moved, the orphaned ticket is set to `ticket.resolvedstatus`. A new ticket that takes over alerts says which ticket it continues.
- **Retries.** Temporary Autotask failures (timeouts, 5xx, 429) return HTTP 500 so Alertmanager retries the notification. Permanent failures (Autotask rejecting the request) are logged with the payload and acknowledged so they are not retried forever.

See [GLOSSARY.md](GLOSSARY.md) for the terms used here.

## Autotask setup

1. **Create an API user** (security level *API User (system)* or equivalent with access to companies, tickets and ticket notes), and note its user name, secret and API tracking identifier (integration code).
2. **Create a Company UDF**, by default named `Alertmanager Selectors`. Use a text field (multi-line, if you want several selectors). This holds each company's company selectors.
3. **Create a Ticket UDF**, by default named `Alertmanager Recorded Alerts`. Use a text field. The receiver manages its contents; it should not be edited by hand.
4. **Choose the catch-all company**, for example your own company, and note its ID. Your own company always has ID `0`. `am-autotask lookup companies` finds it (see [Finding IDs](#finding-ids)).
5. **Find the picklist IDs** to use: the priority for each severity, plus any queue, issue type, sub-issue type or category you want. Configuration always uses numeric IDs, never labels, as labels vary by instance and language. The Autotask UI mostly shows labels, so use `am-autotask lookup picklists` to list the IDs.

At startup the receiver checks every configured ID against Autotask, along with both UDFs and the catch-all company, and refuses to start if anything is missing or inactive. A rejected picklist ID is reported with the setting it came from and the active values of that picklist, for example `ticket.queue: Tickets queueID has no value 99; active values are 8 (Service Desk), 29682833 (Monitoring)`.

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
$ am-autotask lookup companies --config config.yaml "r-group"
ID   NAME
175  R-Group International
```

Both list only active values and companies, as inactive ones are rejected at startup. `--all` includes inactive ones, with an `ACTIVE` column. With Docker, run the image with `lookup` in place of `serve`:

```sh
docker run --rm -v ./config.yaml:/etc/am-autotask/config.yaml \
  ghcr.io/andrewheberle/am-autotask:v0.4.0 lookup picklists --config /etc/am-autotask/config.yaml # x-release-please-version
```

### Company selectors

A company's selector UDF holds one [Alertmanager label selector](https://prometheus.io/docs/alerting/latest/configuration/#matcher) per line, using `=`, `!=`, `=~` and `!~`. A selector matches when all of its matchers match the alert group's labels; a company matches when any of its selectors does. For example:

```
{tenant_id="foo", site="bar"}
{tenant_id="foo", cluster="xyz"}
```

Only the notification's **group labels** are matched, so any label used in a selector must be in the Alertmanager route's `group_by`. This guarantees every alert in a group agrees on the routing labels, and a group can't change company as alerts join or leave it. The company is chosen once, when the ticket is created.

Selectors are loaded from Autotask at startup and every `company.refresh` (default 15 minutes). When a group matches no company, they're also reloaded straight away, at most once per `company.missrefresh` (default one minute), so a company tagged a moment ago is picked up. A company whose selectors don't parse is logged and skipped; the rest still load. If a reload fails, the previous selectors stay in use.

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
| `company.refresh` | `15m` | How often company selectors are reloaded. |
| `company.missrefresh` | `1m` | Minimum time between reloads triggered by a group matching no company. |
| `ticket.udf` | `Alertmanager Recorded Alerts` | Name of the Ticket UDF holding recorded alerts. |
| `ticket.priorities` | | Map of severity to priority ID, e.g. `critical: 4`. As a flag or environment variable: `critical=4,warning=2`. |
| `ticket.defaultpriority` | | Priority ID for severities not in `ticket.priorities`. Required. |
| `ticket.severitylabel` | `severity` | Common label whose value is looked up in `ticket.priorities`. |
| `ticket.status` | `1` (*New*) | Status ID of new tickets. |
| `ticket.type` | `5` (*Alert*) | Ticket type ID of new tickets. |
| `ticket.source` | `8` (*Monitoring Alert*) | Source ID of new tickets. |
| `ticket.queue` | | Queue ID of new tickets. Optional. |
| `ticket.issuetype` | | Issue type ID of new tickets. Optional. |
| `ticket.subissuetype` | | Sub-issue type ID of new tickets. Optional; requires `ticket.issuetype`. |
| `ticket.category` | | Ticket category ID. Optional; Autotask's default category applies when unset. |
| `ticket.duein` | | Due date of new tickets relative to creation, e.g. `4h`. Optional, but Autotask requires a due date unless the ticket category supplies one. |
| `ticket.resolvedstatus` | `5` (*Complete*) | Status ID set when the whole group resolves. `0` leaves the status unchanged. |
| `note.type` | `1` (*Task Summary*) | Note type ID of ticket notes. |
| `note.publish` | `2` (*Internal Project Team*) | Publish (visibility) ID of ticket notes. |

The defaults for status, type, source, note type and publish are Autotask system values, so they exist on every instance. Priorities, queues, issue types and categories differ between instances, so they have no defaults.

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

The receiver runs as the `serve` subcommand, which takes the flags under [Configuration](#configuration); the `lookup` commands are described under [Finding IDs](#finding-ids). Before v0.5.0 the receiver ran without a subcommand, so add `serve` before any existing arguments when upgrading. `am-autotask --version` prints the version.

```sh
am-autotask serve --config config.yaml
```

### Docker

```sh
docker run -v ./config.yaml:/etc/am-autotask/config.yaml \
  ghcr.io/andrewheberle/am-autotask:v0.4.0 serve --config /etc/am-autotask/config.yaml # x-release-please-version
```

### Examples

- [Docker Compose](examples/compose/): am-autotask with Alertmanager and Prometheus, including the self-monitoring rules.
- [Kubernetes](examples/kubernetes/): a Deployment, Service and optional ServiceMonitor, applied with Kustomize.

### Endpoints

| Path | Description |
| --- | --- |
| `POST /webhook` | Alertmanager webhook receiver. Requires `Authorization: Bearer <webhook.token>`. |
| `GET /healthz` | Always `200` while the process is running. |
| `GET /readyz` | `200` once Autotask has been reached, configuration validated and company selectors loaded. Until then notifications are answered with `500`, so Alertmanager retries them. |
| `GET /metrics` | Prometheus metrics, prefixed `am_autotask_`: notifications by result, tickets and notes created, alerts moved off orphaned tickets and orphaned tickets completed, tickets raised against the catch-all company, Autotask errors and request durations by operation, readiness, and the age and validity of the company selectors. See [Monitoring](docs/monitoring.md) for what they mean and ready-made alerting rules. |

## Limitations

- The receiver runs as a single instance. Handling of each alert group is serialised in-process, behind an interface so a shared lock can be added to support replicas.
- If more than one open ticket is ever found for a group, the oldest is used and each other one gets a note naming it and has its `externalID` changed to `am:dup:…` so it is warned only once. Merging is left to a person.
- The Ticket UDF holds the fingerprints of about 450 alerts. Beyond that a group stops recording individual alerts: a note says so, further changes are not noted, and the group resolving still completes the ticket.
- Orphaned tickets are only handed over for alerts that fire again under a new group key. A ticket is left open, and must be completed by hand, if its alerts resolve within `group_wait` of the configuration change, are removed from Alertmanager, or it had too many alerts to record them individually.
- Each notification with firing alerts makes one extra Autotask query to look for orphaned tickets, returning every open am-autotask ticket.
- An alert only counts as resolved when Alertmanager sends it with `status: resolved`; an alert missing from a notification stays recorded until then, or until the whole group resolves.
- Autotask limits API use to 10,000 requests an hour per database, shared with every other integration, and three concurrent requests per endpoint. The receiver makes at most three concurrent requests.
