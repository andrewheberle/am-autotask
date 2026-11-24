## am-autotask templates

Preview ticket templates rendered for a sample or given notification

### Synopsis

Prints every ticket template block rendered for a built-in sample
notification, for an Alertmanager webhook payload saved as JSON
(--notification), or for each alert group in the output of Alertmanager's
/api/v2/alerts/groups (--alert-groups), without raising tickets. Either flag
takes - to read standard input.

The payload's firing alerts are treated as a new ticket, all its alerts as a
change note, and its ticket as a recurrence raising a problem ticket. Output
is tidied and cut to Autotask's limits as on real tickets, and .Severity is
found as serve finds it, from ticket.severitylabel and ticket.severityorder.
System notices are not shown. The templates path and the severity settings
are also read from --config and AM_AUTOTASK_* environment variables, so
passing serve's configuration file previews what serve would write.

Alert groups from /api/v2/alerts/groups are previewed one after another, each
under a heading naming its receiver and group labels. Silenced and inhibited
alerts are left out, as Alertmanager does not send them, and a group with no
others is skipped. The API returns no resolved alerts, so change notes show
every alert as fired; use --notification to preview resolved alerts. The API
does not give Alertmanager's external URL, so .ExternalURL is the built-in
sample's unless --external-url is given. A group whose templates fail shows
the error, and the command fails once every group is printed.

```
am-autotask templates [flags] [args]
```

### Examples

```
  am-autotask templates --templates templates.tmpl --notification webhook.json
  curl -s 'http://alertmanager:9093/api/v2/alerts/groups?receiver=autotask' | am-autotask templates --config config.yaml --alert-groups -
```

### Options

```
      --alert-groups string            path to the output of Alertmanager's /api/v2/alerts/groups (JSON) to render each alert group of instead of the built-in sample, or - for standard input
      --config string                  path to YAML configuration file
      --external-url string            Alertmanager's external URL for alert groups given by --alert-groups (default the built-in sample's)
  -h, --help[=true|false]              help for templates
      --notification string            path to an Alertmanager webhook payload (JSON) to render instead of the built-in sample, or - for standard input
      --templates string               path to a file of ticket templates overriding the built-in wording (optional)
      --ticket.severitylabel string    label whose value is the alert group's severity (default "severity")
      --ticket.severityorder strings   severities, most serious first; when set, an alert group's severity is its highest firing severity (optional)
```

### SEE ALSO

* [am-autotask](am-autotask.md)	 - Alertmanager webhook receiver that raises Autotask tickets

