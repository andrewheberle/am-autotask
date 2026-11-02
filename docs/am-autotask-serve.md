## am-autotask serve

Run the webhook receiver

### Synopsis

Runs the webhook receiver, which takes Alertmanager notifications and raises
and maintains Autotask tickets for them.

At startup every configured ID, UDF and the catch-all company are checked
against Autotask, and the receiver refuses to start if anything is missing or
inactive. The Configuration section of the README describes each setting.

```
am-autotask serve [flags] [args]
```

### Examples

```
  am-autotask serve --config config.yaml
```

### Options

```
      --autotask.integrationcode string       Autotask API tracking identifier (required)
      --autotask.integrationcodefile string   file containing autotask.integrationcode
      --autotask.secret string                Autotask API secret (required)
      --autotask.secretfile string            file containing autotask.secret
      --autotask.username string              Autotask API user name (required)
      --autotask.usernamefile string          file containing autotask.username
      --autotask.zoneurl string               Autotask zone information URL (default "https://webservices.autotask.net/atservicesrest/v1.0/zoneInformation")
      --company.catchall id                   ID of the catch-all company, which may be 0 for your own company (required)
      --company.missrefresh duration          minimum time between reloads when an alert group matches no company (default 1m0s)
      --company.refresh duration              how often company selectors are reloaded (default 15m0s)
      --company.udf string                    name of the Company UDF holding company selectors (default "Alertmanager Selectors")
      --config string                         path to YAML configuration file
  -h, --help                                  help for serve
      --leaderelection.address string         host:port other replicas forward notifications to while this one leads, such as the pod IP and listen port (required with leaderelection.enabled)
      --leaderelection.enabled                run as one of several replicas in Kubernetes, handling notifications only while holding a Lease and otherwise forwarding them to the replica that does
      --leaderelection.identity string        name of this replica on the Lease; default the host name, which is the pod name
      --leaderelection.lease string           name of the Lease (default "am-autotask")
      --leaderelection.namespace string       namespace of the Lease; default the pod's own namespace
      --listen string                         address to listen on (default ":8080")
      --log.level string                      log level (debug, info, warn, error) (default "info")
      --note.publish int                      publish (visibility) ID of ticket notes (default 2)
      --note.type int                         note type ID of ticket notes (default 1)
      --problem.company id                    ID of the company problem tickets are raised against, which may be 0 for your own company; default the new ticket's company (optional)
      --problem.lookback duration             link a new ticket to a problem ticket when its alert group's previous ticket was completed within this long; 0 disables (optional, requires ticket.type 2, Incident)
      --problem.queue int                     queue ID of problem tickets; default the new ticket's queue (optional)
      --templates string                      path to a file of ticket templates overriding the built-in wording (optional)
      --ticket.appliedpriorityudf string      name of the Ticket UDF holding the priority last applied; when set, ticket priority follows severity (optional, requires ticket.severityorder)
      --ticket.category int                   ticket category ID of new tickets (optional)
      --ticket.defaultpriority int            ticket priority ID for severities not in ticket.priorities (required)
      --ticket.duein duration                 due date of new tickets relative to creation (optional)
      --ticket.issuetype int                  issue type ID of new tickets (optional)
      --ticket.priorities stringToInt         severity to ticket priority ID map, e.g. critical=4,warning=2 (default [])
      --ticket.queue int                      queue ID of new tickets (optional)
      --ticket.reopenstatus int               status ID set when an alert fires on a ticket awaiting closure in ticket.resolvedstatus; 0 disables (optional, requires a ticket.resolvedstatus other than 5, Complete)
      --ticket.resolvedstatus int             status ID set when the whole alert group resolves; 0 leaves the status unchanged (default 5)
      --ticket.severitylabel string           label whose value is mapped through ticket.priorities (default "severity")
      --ticket.severityorder strings          severities in ticket.priorities, most serious first; when set, an alert group's priority comes from its highest firing severity (optional)
      --ticket.source int                     source ID of new tickets (default 8)
      --ticket.status int                     status ID of new tickets (default 1)
      --ticket.subissuetype int               sub-issue type ID of new tickets (optional)
      --ticket.type int                       ticket type ID of new tickets (default 5)
      --ticket.udf string                     name of the Ticket UDF holding recorded alerts (default "Alertmanager Recorded Alerts")
      --ticket.worktype int                   work type (billing code) ID of new tickets (optional)
      --tracing.exporter string               trace exporter: none, otlp (OTLP over HTTP, configured by OTEL_* variables) or console (default "none")
      --webhook.token string                  bearer token Alertmanager must send (required)
      --webhook.tokenfile string              file containing webhook.token
```

### SEE ALSO

* [am-autotask](am-autotask.md)	 - Alertmanager webhook receiver that raises Autotask tickets

