## am-autotask lookup picklists

Print the IDs of every picklist setting

### Synopsis

Prints the values of every picklist setting, labelled with the settings that
use them. Sub-issue types are listed with the issue type they belong to, as
ticket.subissuetype must belong to ticket.issuetype.

```
am-autotask lookup picklists [flags] [args]
```

### Examples

```
  am-autotask lookup picklists --config config.yaml
```

### Options

```
      --all    include inactive values
  -h, --help   help for picklists
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

