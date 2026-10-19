# Kubernetes example

Runs am-autotask as a Deployment of two replicas in the `monitoring` namespace, with its settings in a ConfigMap generated from [config.yaml](config.yaml) and its secrets in a Secret mounted as files.

Complete the [Autotask setup](../../README.md#autotask-setup) first.

## Deploy

1. Create the namespace and the Secret. Each key becomes a file under `/run/secrets/am-autotask`, which [config.yaml](config.yaml) points at:

   ```sh
   kubectl create namespace monitoring
   kubectl -n monitoring create secret generic am-autotask \
     --from-file=webhook-token=./webhook-token \
     --from-file=autotask-username=./autotask-username \
     --from-file=autotask-secret=./autotask-secret \
     --from-file=autotask-integrationcode=./autotask-integrationcode
   ```

   Generate the webhook token with, for example, `openssl rand -hex 32 > webhook-token`.

2. Set your catch-all company and priority IDs in [config.yaml](config.yaml), and any other [settings](../../README.md#settings).

3. Apply:

   ```sh
   kubectl apply -k examples/kubernetes
   ```

Each pod becomes ready once it has reached Autotask, validated its configuration, loaded company selectors and read the Lease. If the configuration does not match Autotask it exits, and its logs say why.

## Alertmanager

Point a receiver at the Service, using the same token:

```yaml
receivers:
  - name: autotask
    webhook_configs:
      - url: http://am-autotask.monitoring.svc:8080/webhook
        send_resolved: true
        http_config:
          authorization:
            type: Bearer
            credentials_file: /etc/alertmanager/secrets/am-autotask/webhook-token
```

Mount the token into Alertmanager at that path. With the Prometheus Operator, list the `am-autotask` Secret under the Alertmanager resource's `spec.secrets` and it is mounted at `/etc/alertmanager/secrets/am-autotask/`; the Secret must be in the Alertmanager's namespace, so create a copy there if Alertmanager runs elsewhere.

## Monitoring

With the Prometheus Operator, add [servicemonitor.yaml](servicemonitor.yaml) to the resources in [kustomization.yaml](kustomization.yaml) to scrape `/metrics`. To use the [example alerting rules](../prometheus/am-autotask.rules.yml), put their `groups` in the `spec` of a `PrometheusRule`. See [Monitoring](../../docs/monitoring.md), including how to route am-autotask's own alerts away from am-autotask.

To load the [example dashboard](../grafana/am-autotask.json) with the Grafana sidecar, as in the `kube-prometheus-stack` chart, create a ConfigMap with the sidecar's label (`grafana_dashboard` by default) in a namespace it watches:

```sh
kubectl -n monitoring create configmap am-autotask-dashboard --from-file=examples/grafana/am-autotask.json
kubectl -n monitoring label configmap am-autotask-dashboard grafana_dashboard=1
```

Kustomize can't generate it from [kustomization.yaml](kustomization.yaml), because the dashboard is outside this directory.

## Notes

- **Two replicas with leader election.** One pod, the leader, handles notifications and the other forwards them to it; see [Multiple replicas](../../README.md#multiple-replicas). [rbac.yaml](rbac.yaml) lets the pods' service account manage the `am-autotask` Lease, the pod IP reaches `leaderelection.address` through the downward API, and [pdb.yaml](pdb.yaml) keeps one pod running through voluntary disruptions. Check which pod leads with `kubectl -n monitoring get lease am-autotask`.
- **Rolling updates.** Each new pod starts before an old one stops, and the leader releases the Lease on shutdown, so a standby takes over at once.
- **One replica instead.** To run a single pod, set `replicas: 1`, remove the `AM_AUTOTASK_LEADERELECTION_*` variables and use the `Recreate` strategy, as without leader election two pods must never run at once.
- **Probes.** The startup and readiness probes use `/readyz` and the liveness probe uses `/healthz`. The startup probe allows two minutes to reach Autotask and load company selectors.
- **Locked down.** The pod runs as the image's non-root user with a read-only root filesystem and no capabilities.
