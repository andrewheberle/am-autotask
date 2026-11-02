## am-autotask lookup

Print the Autotask IDs that settings take

### Synopsis

Prints the Autotask IDs that settings take, using the API user given by the
autotask.* settings. Every other setting is ignored, so these commands can run
before the rest of the configuration is filled in.

Only active values are listed, as inactive ones are rejected at startup; --all
includes inactive ones, with an ACTIVE column.

```
am-autotask lookup [command] [flags]
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
      --config string                         path to YAML configuration file
  -h, --help                                  help for lookup
```

### SEE ALSO

* [am-autotask](am-autotask.md)	 - Alertmanager webhook receiver that raises Autotask tickets
* [am-autotask lookup companies](am-autotask-lookup-companies.md)	 - Print the IDs of companies whose name contains NAME
* [am-autotask lookup picklists](am-autotask-lookup-picklists.md)	 - Print the IDs of every picklist setting
* [am-autotask lookup worktypes](am-autotask-lookup-worktypes.md)	 - Print the IDs of work types, for ticket.worktype

