## am-autotask lookup worktypes

Print the IDs of work types, for ticket.worktype

### Synopsis

Prints the IDs of work types, for ticket.worktype. Work types are not a
picklist in Autotask, so lookup picklists does not show them. The API user
needs read access to billing codes.

```
am-autotask lookup worktypes [flags] [args]
```

### Examples

```
  am-autotask lookup worktypes --config config.yaml
```

### Options

```
      --all    include inactive work types
  -h, --help   help for worktypes
```

### Options inherited from parent commands

```
      --autotask.integrationcode string       Autotask API tracking identifier (required)
      --autotask.integrationcodefile string   file containing autotask.integrationcode
      --autotask.secret string                Autotask API secret (required)
      --autotask.secretfile string            file containing autotask.secret
      --autotask.username string              Autotask API user name (required)
      --autotask.usernamefile string          file containing autotask.username
      --autotask.zoneurl string               Autotask zone information URL (default "https://webservices.autotask.net/atservicesrest/v1.0/zoneInformation")
      --config string                         path to YAML configuration file
```

### SEE ALSO

* [am-autotask lookup](am-autotask-lookup.md)	 - Print the Autotask IDs that settings take

