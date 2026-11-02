## am-autotask templates

Preview ticket templates rendered for a sample or given notification

### Synopsis

Prints every ticket template block rendered for a built-in sample
notification, or for an Alertmanager webhook payload saved as JSON, without
raising tickets.

The payload's firing alerts are treated as a new ticket, all its alerts as a
change note, and its ticket as a recurrence raising a problem ticket. System
notices are not shown. The templates path is also read from --config and
AM_AUTOTASK_TEMPLATES.

```
am-autotask templates [flags] [args]
```

### Examples

```
  am-autotask templates --templates templates.tmpl --notification webhook.json
```

### Options

```
      --config string         path to YAML configuration file
  -h, --help                  help for templates
      --notification string   path to an Alertmanager webhook payload (JSON) to render instead of the built-in sample
      --templates string      path to a file of ticket templates overriding the built-in wording (optional)
```

### SEE ALSO

* [am-autotask](am-autotask.md)	 - Alertmanager webhook receiver that raises Autotask tickets

