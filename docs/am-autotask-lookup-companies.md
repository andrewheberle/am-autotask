## am-autotask lookup companies

Print the IDs of companies whose name contains NAME

### Synopsis

Prints the IDs of companies whose name contains NAME, ignoring case.

```
am-autotask lookup companies [flags] [args]
```

### Examples

```
  am-autotask lookup companies --config config.yaml "xyz group"
```

### Options

```
      --all    include inactive companies
  -h, --help   help for companies
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

