# Docker Compose example

Runs am-autotask with Alertmanager sending it notifications, Prometheus scraping both and evaluating the [example alerting rules](../prometheus/am-autotask.rules.yml), and Grafana showing the [example dashboard](../grafana/am-autotask.json).

Complete the [Autotask setup](../../README.md#autotask-setup) first.

## Run

1. Create the secret files in `secrets/`. Compose mounts each one at `/run/secrets/<name>`:

   ```sh
   cd examples/compose
   openssl rand -hex 32 > secrets/webhook-token
   printf '%s' 'api-user@example.com' > secrets/autotask-username
   printf '%s' 'your-api-secret' > secrets/autotask-secret
   printf '%s' 'your-integration-code' > secrets/autotask-integrationcode
   openssl rand -hex 16 > secrets/grafana-admin-password
   chmod 0644 secrets/*
   ```

   Compose mounts the files with their permissions on the host, and the containers run as non-root users, so the files must be readable by others. [secrets/.gitignore](secrets/.gitignore) keeps them out of git.

2. Set your catch-all company and priority IDs in [am-autotask.yaml](am-autotask.yaml), and any other [settings](../../README.md#settings).

3. In [alertmanager.yml](alertmanager.yml), list the labels your company selectors use in `group_by`, and give the `ops` receiver an integration for am-autotask's own alerts.

4. Start the stack:

   ```sh
   docker compose up -d
   docker compose logs -f am-autotask
   ```

am-autotask is on port 8080, Alertmanager on 9093, Prometheus on 9090 and Grafana on 3000. Sign in to Grafana as `admin` with the password in `secrets/grafana-admin-password`; the **am-autotask** dashboard is provisioned with Prometheus as its data source.

## Try it

This sends a test alert to Alertmanager, which after its `group_wait` creates a **real ticket** in Autotask, against the catch-all company unless a company's selectors match `tenant_id="foo"`:

```sh
curl -X POST http://localhost:9093/api/v2/alerts \
  -H 'Content-Type: application/json' \
  -d '[{"labels": {"alertname": "TestAlert", "tenant_id": "foo", "severity": "warning"}, "annotations": {"summary": "Test alert from the am-autotask compose example"}}]'
```

The alert resolves by itself after Alertmanager's `resolve_timeout` (five minutes by default), which completes the ticket.

## Notes

- There is no Compose health check: the am-autotask image has no shell or HTTP client to run one. Prometheus scrapes it instead, and `AmAutotaskDown` fires if it stops answering.
- Prometheus mounts the alerting rules from [examples/prometheus](../prometheus/), so it evaluates the same rules the unit tests cover.
- Grafana mounts the dashboard from [examples/grafana](../grafana/). It is provisioned read-only: save a copy to change it.
