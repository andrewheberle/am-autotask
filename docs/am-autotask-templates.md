## am-autotask templates

Preview ticket templates rendered for a sample or given notification

### Synopsis

Prints every ticket template block rendered for a built-in sample
notification, or for an Alertmanager webhook payload saved as JSON, without
raising tickets.

The payload's firing alerts are treated as a new ticket, all its alerts as a
change note, and its ticket as a recurrence raising a problem ticket. Output
is tidied and cut to Autotask's limits as on real tickets, and .Severity is
found as serve finds it, from ticket.severitylabel and ticket.severityorder.
System notices are not shown. The templates path and the severity settings
are also read from --config and AM_AUTOTASK_* environment variables, so
passing serve's configuration file previews what serve would write.

```
am-autotask templates [flags] [args]
```

### Examples

```
  am-autotask templates --templates templates.tmpl --notification webhook.json
```

### Options

```
      --config string                  path to YAML configuration file
  -h, --help                           help for templates
      --notification string            path to an Alertmanager webhook payload (JSON) to render instead of the built-in sample
      --templates string               path to a file of ticket templates overriding the built-in wording (optional)
      --ticket.severitylabel string    label whose value is the alert group's severity (default "severity")
      --ticket.severityorder strings   severities, most serious first; when set, an alert group's severity is its highest firing severity (optional)
```

### SEE ALSO

* [am-autotask](am-autotask.md)	 - Alertmanager webhook receiver that raises Autotask tickets

