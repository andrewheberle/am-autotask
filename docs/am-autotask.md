## am-autotask

Alertmanager webhook receiver that raises Autotask tickets

### Synopsis

Receives Alertmanager notifications and raises and maintains Autotask
tickets, one ticket per alert group, against the company the alerts belong to.

Settings come from command line flags, a YAML file named by --config, and
environment variables prefixed AM_AUTOTASK_. Later sources override earlier
ones in this order: flag defaults, configuration file, environment variables,
flags given on the command line. See the README for every setting.

```
am-autotask [command] [flags]
```

### Options

```
  -h, --help   help for am-autotask
```

### SEE ALSO

* [am-autotask lookup](am-autotask-lookup.md)	 - Print the Autotask IDs that settings take
* [am-autotask serve](am-autotask-serve.md)	 - Run the webhook receiver
* [am-autotask templates](am-autotask-templates.md)	 - Preview ticket templates rendered for a sample or given notification

