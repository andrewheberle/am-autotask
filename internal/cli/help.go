package cli

// Long descriptions and examples for each command. These are shown by --help
// and are used to generate the markdown documentation in docs/, so they are
// kept short and refer to the README for detail.
//
// Long descriptions are written as plain text that also renders as markdown.

const (
	rootLong = `Receives Alertmanager notifications and raises and maintains Autotask
tickets, one ticket per alert group, against the company the alerts belong to.

Settings come from command line flags, a YAML file named by --config, and
environment variables prefixed AM_AUTOTASK_. Later sources override earlier
ones in this order: flag defaults, configuration file, environment variables,
flags given on the command line. See the README for every setting.`

	serveLong = `Runs the webhook receiver, which takes Alertmanager notifications and raises
and maintains Autotask tickets for them.

At startup every configured ID, UDF and the catch-all company are checked
against Autotask, and the receiver refuses to start if anything is missing or
inactive. The Configuration section of the README describes each setting.`

	serveExample = `  am-autotask serve --config config.yaml`

	lookupLong = `Prints the Autotask IDs that settings take, using the API user given by the
autotask.* settings. Every other setting is ignored, so these commands can run
before the rest of the configuration is filled in.

Only active values are listed, as inactive ones are rejected at startup; --all
includes inactive ones, with an ACTIVE column.`

	lookupPicklistsLong = `Prints the values of every picklist setting, labelled with the settings that
use them. Sub-issue types are listed with the issue type they belong to, as
ticket.subissuetype must belong to ticket.issuetype.`

	lookupPicklistsExample = `  am-autotask lookup picklists --config config.yaml`

	lookupCompaniesLong = `Prints the IDs of companies whose name contains NAME, ignoring case.`

	lookupCompaniesExample = `  am-autotask lookup companies --config config.yaml "xyz group"`

	lookupWorktypesLong = `Prints the IDs of work types, for ticket.worktype. Work types are not a
picklist in Autotask, so lookup picklists does not show them. The API user
needs read access to billing codes.`

	lookupWorktypesExample = `  am-autotask lookup worktypes --config config.yaml`

	templatesLong = `Prints every ticket template block rendered for a built-in sample
notification, or for an Alertmanager webhook payload saved as JSON, without
raising tickets.

The payload's firing alerts are treated as a new ticket, all its alerts as a
change note, and its ticket as a recurrence raising a problem ticket. System
notices are not shown. The templates path is also read from --config and
AM_AUTOTASK_TEMPLATES.`

	templatesExample = `  am-autotask templates --templates templates.tmpl --notification webhook.json`
)
