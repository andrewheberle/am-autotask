# Monitoring am-autotask

am-autotask exposes Prometheus metrics at `/metrics`. This page covers what to scrape, what the metrics mean, the Grafana dashboard in [examples/grafana](../examples/grafana/), the alerting rules in [examples/prometheus](../examples/prometheus/), and what to do when each alert fires.

## Scraping

```yaml
scrape_configs:
  - job_name: am-autotask
    static_configs:
      - targets: ["am-autotask:8080"]
```

`/metrics` needs no authentication. Also scrape Alertmanager: its `alertmanager_notifications_failed_total` is the only way to see deliveries that never reached am-autotask, for example while it is down.

## Metrics

All am-autotask metrics are prefixed `am_autotask_`. The standard Go runtime (`go_*`) and process (`process_*`) metrics are also exported.

| Metric | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `am_autotask_notifications_total` | counter | `result` | Webhook requests by result: `handled`, `unauthorized` (missing or wrong bearer token), `invalid` (malformed or unsupported webhook version), `temporary_failure` (answered 500 so Alertmanager retries, including while still starting up) and `permanent_failure` (acknowledged without reaching Autotask). |
| `am_autotask_tickets_created_total` | counter | | Autotask tickets created, including problem tickets. |
| `am_autotask_notes_created_total` | counter | | Ticket notes created, including notes on duplicate and orphaned tickets. |
| `am_autotask_autotask_errors_total` | counter | `operation`, `kind` | Failed Autotask requests made while handling notifications. `operation` is one of `open_tickets`, `previous_ticket`, `get_ticket`, `open_alert_tickets`, `create_ticket`, `create_note` or `update_ticket`; `kind` is `temporary`, `permanent` or `rate_limited` (HTTP 429). Company selector reloads are not counted here; see `am_autotask_company_selectors_age_seconds`. |
| `am_autotask_alerts_moved_total` | counter | | Alerts moved off orphaned tickets onto their alert group's current ticket. |
| `am_autotask_orphaned_tickets_completed_total` | counter | | Orphaned tickets completed because all of their alerts moved. |
| `am_autotask_company_selectors_age_seconds` | gauge | | Time since company selectors were last loaded from Autotask. It drops back to zero on each successful reload, every `company.refresh` (default 15 minutes), and keeps growing while reloads fail. It is zero until the first load. |
| `am_autotask_company_selectors_companies` | gauge | `state` | Companies with company selectors in the last successful load: `valid`, or `invalid` (their selectors do not parse, so they are skipped and no alert group can match them). |
| `am_autotask_catchall_tickets_total` | counter | `reason` | Tickets raised against the catch-all company: `unmatched` (no company's selectors matched the alert group) or `ambiguous` (more than one company's did). |
| `am_autotask_ticket_priority_changes_total` | counter | `direction` | Ticket priorities changed to follow their alert group's severity, `raised` or `lowered`. Only counted when `ticket.appliedpriorityudf` is set. |
| `am_autotask_ticket_priority_overrides_total` | counter | | Tickets found with a priority a person changed, which then no longer follows severity. Counted once per ticket. A high rate compared with `am_autotask_ticket_priority_changes_total` suggests `ticket.priorities` or `ticket.severityorder` doesn't match how the service desk prioritises. |
| `am_autotask_template_errors_total` | counter | `template` | [Ticket templates](../README.md#ticket-templates) from the templates file that failed on a notification, so the built-in wording was used instead: `ticket.title`, `ticket.description`, `note.title`, `note.description`, `problem.title` or `problem.description`. Always zero without a templates file. |
| `am_autotask_recurrences_total` | counter | `outcome` | [Recurrences](../README.md#recurrences): tickets raised soon after their alert group's previous ticket was completed, by outcome: `followed` (linked to the previous ticket's open problem ticket), `raised` (a new problem ticket was raised) or `failed` (left for a person to link; see [AmAutotaskRecurrenceLinkFailed](#amautotaskrecurrencelinkfailed)). Always zero unless `problem.lookback` is set. |
| `am_autotask_ready` | gauge | | `1` once startup has completed and notifications are being handled, otherwise `0`. |
| `am_autotask_autotask_request_duration_seconds` | histogram | `operation` | Duration of Autotask operations made while handling notifications, with the same `operation` values as `am_autotask_autotask_errors_total`. An operation includes all of its pages and any read back, such as `create_ticket` reading the new ticket's number. |

### Useful queries

```promql
# notifications per second by result
sum by (result) (rate(am_autotask_notifications_total[5m]))

# share of notifications failing, temporarily or permanently
sum(rate(am_autotask_notifications_total{result=~".*_failure"}[5m]))
  / sum(rate(am_autotask_notifications_total[5m]))

# tickets and notes created per hour
sum(increase(am_autotask_tickets_created_total[1h]))
sum(increase(am_autotask_notes_created_total[1h]))

# Autotask errors by operation and kind
sum by (operation, kind) (rate(am_autotask_autotask_errors_total[5m]))

# 95th percentile Autotask request duration by operation
histogram_quantile(0.95, sum by (operation, le) (rate(am_autotask_autotask_request_duration_seconds_bucket[5m])))

# tickets raised against the catch-all company per day, by reason
sum by (reason) (increase(am_autotask_catchall_tickets_total[1d]))

# ticket priorities changed to follow severity, and manual overrides, per day
sum by (direction) (increase(am_autotask_ticket_priority_changes_total[1d]))
increase(am_autotask_ticket_priority_overrides_total[1d])

# recurrences per day, by outcome
sum by (outcome) (increase(am_autotask_recurrences_total[1d]))

# deliveries Alertmanager could not make to webhook receivers, including am-autotask
sum(rate(alertmanager_notifications_failed_total{integration="webhook"}[5m]))
```

## Dashboard

[am-autotask.json](../examples/grafana/am-autotask.json) is a Grafana dashboard for Grafana 11.0 or later. Import it from **Dashboards → New → Import**, or provision it from a file as the [Compose example](../examples/compose/) does. It has three variables:

- **Data source**: the Prometheus data source that scrapes am-autotask.
- **Job**: the `job` label am-autotask is scraped with.
- **Instance**: one or more of that job's instances.

Its rows are:

- **Status**: am-autotask's own firing and pending alerts from the [alerting rules](#alerting-rules), readiness, the age of the company selectors and how many companies' selectors are valid. The alert list needs Grafana to read the rules from the data source, which it does for Prometheus and Mimir.
- **Notifications**: notifications by result, the share failing, and Alertmanager's failed webhook deliveries. Like the query above, the last counts every webhook receiver.
- **Autotask**: request duration and errors by operation.
- **Tickets** (collapsed): tickets and notes created, catch-all tickets, priority changes compared with overrides, orphaned tickets, recurrences by outcome, and ticket templates that failed. These don't need anyone to act on them straight away; use them to tune company selectors and `ticket.priorities`.

When an alert fires, open its runbook below and use the dashboard to see when the problem started and how far it reaches.

## Traces

With [tracing](../README.md#tracing) enabled, each notification is a trace continuing Alertmanager's, and log lines carry its `trace_id`. To find out why a notification was slow or failed:

- Search the logs for the warning or error, and open its `trace_id` in your tracing backend.
- The `handle notification` span's `am_autotask.outcome` and `am_autotask.failure` attributes say what happened; its children show where the time went, for example waiting on another notification for the same alert group (`lock alert group`) or a slow Autotask operation.
- `am_autotask.external_id` on the span is the ticket's `externalID`, so the trace and the Autotask ticket can be matched either way.

## Alerting rules

[am-autotask.rules.yml](../examples/prometheus/am-autotask.rules.yml) holds ready-made rules. They assume:

- am-autotask is scraped with `job="am-autotask"`
- Alertmanager is scraped with `job="alertmanager"`
- am-autotask is Alertmanager's only webhook receiver

Adjust the selectors if yours differ. `AmAutotaskCompanySelectorsStale` assumes the default `company.refresh` of 15 minutes; set its threshold to about three refresh intervals if you change it.

By default Alertmanager's `alertmanager_notifications_failed_total` has no receiver label, so `AlertmanagerAutotaskNotificationsFailing` counts failed deliveries to every webhook receiver. With Alertmanager v0.27.0 or later and `--enable-feature=receiver-name-in-metrics`, it gains a `receiver_name` label: add `receiver_name="autotask"` (your receiver's name) to the rule and the query above to exclude other webhook receivers.

The rules come with unit tests. Run them with [promtool](https://prometheus.io/docs/prometheus/latest/command-line/promtool/) after any change:

```sh
promtool test rules examples/prometheus/am-autotask.rules.test.yml
```

The [alerting rules workflow](../.github/workflows/rules.yml) runs these checks on pull requests that change the rules.

| Alert | Severity | Fires when |
| --- | --- | --- |
| [AmAutotaskDown](#amautotaskdown) | critical | am-autotask cannot be scraped for 5 minutes. |
| [AmAutotaskAbsent](#amautotaskabsent) | critical | No am-autotask target exists for 15 minutes. |
| [AlertmanagerAutotaskNotificationsFailing](#alertmanagerautotasknotificationsfailing) | critical | Alertmanager has been failing to deliver to am-autotask for 15 minutes. |
| [AmAutotaskPermanentFailures](#amautotaskpermanentfailures) | warning | Any notification failed permanently in the last 15 minutes. |
| [AmAutotaskTemporaryFailures](#amautotasktemporaryfailures) | warning | More than a quarter of notifications have been failing temporarily for 15 minutes. |
| [AmAutotaskRateLimited](#amautotaskratelimited) | warning | Autotask returned HTTP 429 in the last 15 minutes. |
| [AmAutotaskUnauthorizedRequests](#amautotaskunauthorizedrequests) | warning | Any request had a missing or wrong bearer token in the last 15 minutes. |
| [AmAutotaskInvalidNotifications](#amautotaskinvalidnotifications) | warning | Any notification was malformed or an unsupported version in the last 15 minutes. |
| [AmAutotaskCompanySelectorsStale](#amautotaskcompanyselectorsstale) | warning | Company selectors have not been reloaded for 45 minutes. |
| [AmAutotaskRestarting](#amautotaskrestarting) | warning | am-autotask restarted more than three times in an hour. |
| [AmAutotaskAlertsMoved](#amautotaskalertsmoved) | info | Alerts moved off orphaned tickets in the last hour. |
| [AmAutotaskNotReady](#amautotasknotready) | warning | am-autotask has been running for 10 minutes without becoming ready. |
| [AmAutotaskSlowAutotask](#amautotaskslowautotask) | warning | The 95th percentile Autotask request has taken over 5 seconds for 15 minutes. |
| [AmAutotaskAmbiguousCompanyMatch](#amautotaskambiguouscompanymatch) | warning | An alert group matched more than one company in the last hour. |
| [AmAutotaskUnmatchedAlertGroups](#amautotaskunmatchedalertgroups) | info | An alert group matched no company in the last hour. |
| [AmAutotaskInvalidCompanySelectors](#amautotaskinvalidcompanyselectors) | warning | A company's selectors have failed to parse for 15 minutes. |
| [AmAutotaskTemplateErrors](#amautotasktemplateerrors) | warning | A ticket template from the templates file failed in the last hour. |
| [AmAutotaskRecurrenceLinkFailed](#amautotaskrecurrencelinkfailed) | warning | A recurrence could not be linked to a problem ticket in the last hour. |

### Routing these alerts

**Do not send am-autotask's own alerts to am-autotask.** When it is down or cannot reach Autotask, it cannot raise a ticket about that. Route them to another receiver, such as email or chat:

```yaml
route:
  receiver: autotask
  routes:
    - receiver: ops-email
      matchers:
        - alertname=~"AmAutotask.*|AlertmanagerAutotaskNotificationsFailing"
```

This route sends the alerts to `ops-email` only, so it does not create the overlapping routes the [README](../README.md#alertmanager-configuration) warns about.

These rules cannot fire if Prometheus or Alertmanager is itself down. For that, use an always-firing "dead man's switch" alert sent to an external service that alerts when it stops arriving.

## Runbook

### AmAutotaskDown

Prometheus cannot scrape am-autotask, so alerts are not becoming tickets. Alertmanager retries failed deliveries, but gives up on each one by the next `group_interval`; the next re-send of each group delivers its current state once am-autotask is back.

- Check the container or pod is running, and its logs. am-autotask exits at startup with an `exiting` log line when its configuration is invalid or does not match Autotask (a missing UDF, an inactive picklist ID or catch-all company), or when the first company selector load fails.
- Check the scrape target address and network path.

### AmAutotaskAbsent

No am-autotask target has been discovered, so none of the other am-autotask alerts can fire. Check the scrape configuration or service discovery, and that the `job` label matches the rules.

### AlertmanagerAutotaskNotificationsFailing

Alertmanager is failing to deliver notifications to am-autotask. This catches problems am-autotask cannot report itself, such as being unreachable.

- Unless the rule is narrowed with `receiver_name` (see [Alerting rules](#alerting-rules)), it also fires when another webhook receiver is failing. Alertmanager's logs name the receiver.
- If `AmAutotaskDown` is also firing, start there.
- If am-autotask is up, check Alertmanager's logs for the status code. HTTP 401 means the bearer token Alertmanager sends does not match `webhook.token`. HTTP 500 means am-autotask is failing temporarily: see [AmAutotaskTemporaryFailures](#amautotasktemporaryfailures).
- Check the webhook URL in Alertmanager's configuration.

### AmAutotaskPermanentFailures

Autotask rejected a request in a way retrying cannot fix, so the notification was acknowledged without reaching Autotask. The group's next notification (at the latest its next `repeat_interval` re-send) is tried afresh, so the ticket catches up once the cause is fixed.

- Search am-autotask's logs for `notification failed permanently`; each entry has Autotask's error message and the notification payload.
- `am_autotask_autotask_errors_total{kind="permanent"}` shows which operation failed.
- Common causes are configuration that has become invalid since startup, such as a deactivated picklist value or catch-all company, or a ticket category that now requires fields am-autotask does not set. Startup validation catches these on the next restart.

### AmAutotaskTemporaryFailures

Many notifications are failing in ways that can be retried: timeouts, Autotask 5xx responses or rate limiting. Alertmanager retries them, but tickets fall behind.

- Check `sum by (operation, kind) (rate(am_autotask_autotask_errors_total[5m]))` for which requests are failing.
- Check Autotask's status for an outage, and am-autotask's logs for `notification failed; Alertmanager will retry`.
- If `AmAutotaskRateLimited` is also firing, start there.
- am-autotask answers notifications with HTTP 500 while still starting up, so a short burst after a restart is expected.

### AmAutotaskRateLimited

Autotask is rejecting requests with HTTP 429. Autotask allows 10,000 requests an hour per database, shared by every integration, and three concurrent requests per endpoint. am-autotask makes at most three concurrent requests itself, so other integrations are usually involved.

- Check the database's current usage through the Autotask API's `ThresholdInformation` endpoint.
- Reduce am-autotask's request volume: each notification costs at least one query, and one more when it has firing alerts. A longer Alertmanager `repeat_interval` cuts unchanged re-sends, and grouping noisy alerts reduces notifications.
- Review other integrations on the same database.

### AmAutotaskUnauthorizedRequests

Requests reached `/webhook` without a valid bearer token.

- After rotating the token, check Alertmanager and am-autotask use the same one.
- If Alertmanager deliveries are succeeding, something else is calling the endpoint. Restrict network access to it.

### AmAutotaskInvalidNotifications

Notifications were rejected as malformed or as an unsupported webhook payload version. Only version 4 is accepted. Check whether something other than Alertmanager is posting to `/webhook`, or whether an Alertmanager upgrade changed the payload version.

### AmAutotaskCompanySelectorsStale

Company selectors have not been reloaded from Autotask for several refresh intervals, so selector changes are not being picked up. Alert groups are still routed using the last selectors loaded.

- Search am-autotask's logs for `company selector refresh failed`.
- Check Autotask is reachable and the API user's credentials are still valid.
- Companies whose selectors do not parse are skipped with a `skipping company with invalid company selectors` warning, but do not stop a reload or trigger this alert.

### AmAutotaskRestarting

am-autotask keeps restarting. Check its logs for the reason; a configuration error exits at startup with an `exiting` log line, and a container memory limit that is too low shows as the container being killed.

### AmAutotaskAlertsMoved

Informational: alerts moved off orphaned tickets, usually because Alertmanager's routes or `group_by` changed. Each orphaned ticket has a note naming the ticket its alerts moved to, and is completed once all have moved.

Tickets whose alerts never fire again under a new group key are left open. After a routing change, check for open tickets whose `externalID` begins with `am:` that are no longer being updated, and complete them by hand. See the README's [limitations](../README.md#limitations).

### AmAutotaskNotReady

am-autotask is running but has not finished starting, so it answers every notification with HTTP 500. Startup discovers the Autotask zone, validates the configuration against Autotask and loads company selectors; each step either succeeds or makes the process exit, so staying unready usually means one is hanging.

- Check am-autotask's logs for the last startup step reached.
- Check Autotask, and the zone information URL, are reachable from am-autotask.

### AmAutotaskSlowAutotask

Autotask requests are slow, so notifications take longer to handle and Alertmanager may time out deliveries.

- Check `histogram_quantile(0.95, sum by (operation, le) (rate(am_autotask_autotask_request_duration_seconds_bucket[5m])))` for which operations are slow. `open_alert_tickets` returns every open am-autotask ticket, so it grows with the number of open tickets.
- Autotask adds delay as a database approaches its hourly request limit, and as concurrent requests to one endpoint pass three; check usage with the API's `ThresholdInformation` endpoint, and see [AmAutotaskRateLimited](#amautotaskratelimited).
- Check Autotask's status for degraded performance.

### AmAutotaskAmbiguousCompanyMatch

An alert group matched the company selectors of more than one company, so its ticket was raised against the catch-all company. The ticket description names the companies, and am-autotask's logs have an `alert group matches more than one company` warning with the group labels.

- Change the selectors of those companies so only one matches the group.
- Move the ticket to the right company by hand; am-autotask chooses a group's company only when the ticket is created.

### AmAutotaskUnmatchedAlertGroups

Informational: an alert group matched no company's selectors, so its ticket was raised against the catch-all company. That can be intended, for alerts about your own infrastructure. Otherwise:

- Add a selector to the company the group belongs to. The group labels are on the ticket.
- Check the labels used in selectors are in the Alertmanager route's `group_by`; only group labels are matched.

### AmAutotaskInvalidCompanySelectors

Some companies' company selectors do not parse, so those companies are skipped and no alert group can match them. Their alerts go to the catch-all company instead.

- Search am-autotask's logs for `skipping company with invalid company selectors`; each entry names the company and the parse error.
- Correct the selectors in the company's UDF: one Alertmanager label selector per line, such as `{tenant_id="foo", site="bar"}`. The change is picked up at the next reload.

### AmAutotaskTemplateErrors

A block in the templates file failed on a real notification, although it passed against the sample notification at startup. The ticket or note was still written, using the built-in wording for that block, so nothing was lost, but it doesn't look the way the templates file intends.

- Search am-autotask's logs for `ticket template failed`; each entry names the template and the error, such as an index past the end of `.Alerts` or a function given the wrong type.
- Save the notification that failed (Alertmanager's webhook payload) as JSON and reproduce the error with `am-autotask templates --templates <file> --notification <payload>`.
- Fix the block so it handles that notification, for example by checking a list's length before indexing it, and restart am-autotask.

### AmAutotaskRecurrenceLinkFailed

A ticket was raised soon after its alert group's previous ticket was completed, but linking it to a problem ticket failed. The ticket itself was raised, and has a note titled `Not linked to a problem ticket` naming the previous ticket, any problem ticket already raised, and the error. am-autotask only links a recurrence when its ticket is raised, so it won't try again.

- Search am-autotask's logs for `linking recurrence to a problem ticket failed`; each entry names the ticket, the previous ticket and the error.
- Link the ticket to the problem ticket by hand in Autotask, raising one first if the note names none. Link the previous ticket too if it is an Incident and not already linked to a problem ticket.
- If Autotask rejected the link, check the problem ticket: a problem ticket holds at most 500 incidents, and only Incident tickets can be linked, so `ticket.type` must be 2.
- If the error is temporary, such as a timeout or rate limiting, see [AmAutotaskTemporaryFailures](#amautotasktemporaryfailures) and [AmAutotaskRateLimited](#amautotaskratelimited).
