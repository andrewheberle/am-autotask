# The .deb service runs as a dynamic user with systemd credentials

The `.deb` package's `am-autotask.service` runs with `DynamicUser=yes`, so systemd allocates a throwaway UID on each start and the package creates no user. Because Autotask holds all state (ADR-0001), the service needs nothing writable that would outlive that UID. Secrets can't sit in files the service owns, so systemd reads them with `LoadCredential=` from root-only files in `/etc/am-autotask/credentials/` and the unit points the existing `*file` flags at `$CREDENTIALS_DIRECTORY`. The configuration file holds no secrets and stays world-readable. Those credential file names are now an interface admins write to, so renaming them breaks existing installs.

## Considered Options

- **A dedicated `am-autotask` system user with an `EnvironmentFile`** holding `AM_AUTOTASK_*` secrets: familiar, but needs `adduser` and cleanup in the maintainer scripts, and puts secrets in the process environment, where `/proc/<pid>/environ` and crash reports can expose them.
- **A dedicated user owning a secrets file named in the configuration**: no environment exposure, but the same user management, and secrets stay readable by a long-lived UID rather than only for the life of the service.
- **Shipping `/etc/am-autotask/config.yaml` as a conffile**: rejected separately; placeholder IDs fail startup anyway, and weekly releases (ADR-0009) would make any change to it prompt on every upgrade.

## Consequences

The unit requires systemd 247 or later, so Debian 12 and Ubuntu 22.04 onwards. Missing credential files fail the start with a clear error rather than a confusing configuration message. Admins can swap to `LoadCredentialEncrypted=` in a drop-in without any change to the binary.
