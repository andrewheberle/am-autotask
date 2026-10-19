# Replicas elect a leader through a Kubernetes Lease

Running more than one replica is for availability, not throughput: the Autotask request limit (10,000 an hour per database, shared with every integration) caps throughput whatever the replica count, and more replicas only spend it faster. So replicas run active/standby. With `leaderelection.enabled`, every replica campaigns for one Kubernetes Lease; the holder, the leader, handles every notification with the existing in-process lock, and the others are standbys that forward the notifications they receive to it. The Lease is the only shared state besides Autotask, keeping ADR-0001's promise that replicas need no new infrastructure. The feature is Kubernetes only and off by default.

## Considered Options

- **A distributed lock per alert group in Redis/Valkey**, so every replica handles notifications: true scale-out, but needs a store to run and keep available, and buys no throughput past Autotask's limit.
- **A Lease per alert group**: no extra store, but a Kubernetes API write per notification, and per group key, which isn't a valid object name without hashing.
- **Consistent-hash forwarding between replicas**, each owning some alert groups: no store, but two replicas own a group during membership changes, so it needs the same fencing for much more machinery.
- **client-go's leaderelection package**: proven, but pulls in a large dependency tree for three HTTP calls. The Lease protocol is small and the API server's `resourceVersion` check does the hard part, so it is written with the standard library.
- **Standbys reporting not ready**, so the Service only routes to the leader: no forwarding, but a rolling update never finishes, because the new pods can't become ready while the old leader holds the Lease. Labelling the leader's pod for the Service to select instead needs `pods/patch` and can point at the wrong pod after a failover.

## Consequences

- Timings are fixed at Kubernetes' defaults: the leader renews every 2 seconds and steps down if it can't renew within 10, and standbys take over a Lease unrenewed for 15. Expiry is judged by when each replica last saw the Lease change, using its own clock, so clock skew between nodes doesn't matter.
- Autotask can't reject writes made from stale data, so leadership can't be fenced completely. Each notification is handled under a context cancelled when leadership is lost, and the leader stops before the Lease can look expired to anyone else, but a leader that stalls mid-request can still finish a write after a standby has taken over. That rare overlap is accepted. It can add a duplicate note or lose one recorded-alerts update, and duplicate tickets are still retired as ADR-0001 describes.
- Standbys stay warm: they validate the configuration and load company selectors, so takeover is immediate, at the cost of each replica's selector reloads. Reloads are jittered so replicas started together don't reload together.
- Every replica is ready once warm, so rolling updates proceed. A standby forwards each notification to the address the leader publishes on the Lease, marked so that a replica that has just lost the Lease answers 503 rather than forwarding again. With no leader it answers 503, and Alertmanager retries.
- A replica that loses the Lease waits for the notifications it is handling to stop, then campaigns again as a standby, rather than exiting.
- On shutdown the leader drains in-flight requests before releasing the Lease, so a standby takes over straight away without overlapping it.
- The service account needs a Role to get, create and update Leases, and the pod's IP is passed in through the downward API. Leader election refuses to start outside a pod.
