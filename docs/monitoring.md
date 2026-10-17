# Monitoring am-autotask

am-autotask exposes Prometheus metrics at `/metrics`. This page covers what to scrape, what the metrics mean, the alerting rules in [examples/prometheus](../examples/prometheus/), and what to do when each alert fires.

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
| `am_autotask_tickets_created_total` | counter | | Autotask tickets created. |
| `am_autotask_notes_created_total` | counter | | Ticket notes created, including notes on duplicate and orphaned tickets. |
| `am_autotask_autotask_errors_total` | counter | `operation`, `kind` | Failed Autotask requests made while handling notifications. `operation` is one of `open_tickets`, `previous_ticket`, `open_alert_tickets`, `create_ticket`, `create_note` or `update_ticket`; `kind` is `temporary`, `permanent` or `rate_limited` (HTTP 429). Company selector reloads are not counted here; see `am_autotask_company_selectors_age_seconds`. |
| `am_autotask_alerts_moved_total` | counter | | Alerts moved off orphaned tickets onto their alert group's current ticket. |
| `am_autotask_orphaned_tickets_completed_total` | counter | | Orphaned tickets completed because all of their alerts moved. |
| `am_autotask_company_selectors_age_seconds` | gauge | | Time since company selectors were last loaded from Autotask. It drops back to zero on each successful reload, every `company.refresh` (default 15 minutes), and keeps growing while reloads fail. It is zero until the first load. |

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

# deliveries Alertmanager could not make to am-autotask
sum(rate(alertmanager_notifications_failed_total{integration="webhook", receiver="autotask"}[5m]))
```

## Alerting rules

[am-autotask.rules.yml](../examples/prometheus/am-autotask.rules.yml) holds ready-made rules. They assume:

- am-autotask is scraped with `job="am-autotask"`
- Alertmanager is scraped with `job="alertmanager"`
- the Alertmanager receiver pointing at am-autotask is named `autotask`

Adjust the selectors if yours differ. `AmAutotaskCompanySelectorsStale` assumes the default `company.refresh` of 15 minutes; set its threshold to about three refresh intervals if you change it.

The rules come with unit tests. Run them with [promtool](https://prometheus.io/docs/prometheus/latest/command-line/promtool/) after any change:

```sh
promtool test rules examples/prometheus/am-autotask.rules.test.yml
```

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

## Not covered by the metrics

Some things am-autotask cannot currently report as metrics:

- **How often the catch-all company is used.** Tickets raised against the catch-all company, because no company or more than one matched, are not counted separately. Ambiguous matches are logged with `alert group matches more than one company`.
- **Readiness.** `/readyz` reports it, but there is no metric. Probe `/readyz` with the blackbox exporter if you need to alert on it.
- **Autotask request latency.**
- **Companies with invalid company selectors.** These are only logged.
