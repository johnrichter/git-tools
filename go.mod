module github.com/johnrichter/git-tools

go 1.26

require (
	github.com/gofrs/flock v0.13.0
	github.com/johnrichter/claude-shared-tooling/go/clikit v0.0.0
	github.com/johnrichter/claude-shared-tooling/go/fsx v0.0.0
	github.com/johnrichter/claude-shared-tooling/go/git v0.0.0
	github.com/johnrichter/claude-shared-tooling/go/githooks v0.0.0
	github.com/johnrichter/claude-shared-tooling/go/sysops v0.0.0
	github.com/knadh/koanf/parsers/yaml v1.1.0
	github.com/knadh/koanf/providers/confmap v1.0.0
	github.com/knadh/koanf/providers/env v1.1.0
	github.com/knadh/koanf/providers/file v1.2.1
	github.com/knadh/koanf/providers/posflag v1.0.1
	github.com/knadh/koanf/v2 v2.3.5
	github.com/spf13/cobra v1.10.2
	github.com/spf13/pflag v1.0.9
)

require (
	github.com/bmatcuk/doublestar/v4 v4.10.0 // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.4.0 // indirect
	github.com/google/renameio/v2 v2.0.2 // indirect
	github.com/gowebpki/jcs v1.0.1 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/johnrichter/claude-shared-tooling/go/logkit v0.0.0 // indirect
	github.com/knadh/koanf/maps v0.1.2 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/rogpeppe/go-internal v1.15.0 // indirect
	github.com/rs/zerolog v1.35.1 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/sys v0.37.0 // indirect
)

// git, githooks, clikit, fsx and sysops are ai-shared-lib sibling-repo modules
// (../ai-shared-lib/go/*), not yet independently tagged -- this placeholder
// version + local replace is a monorepo-development stand-in a future release
// transaction resolves by cutting real tags and pointing these requires at
// them. A `replace` directive is only honored in the MAIN module's own
// go.mod, so the full transitive closure (including jsondoc, state and
// logkit, which githooks and clikit depend on but don't replace themselves)
// is replaced here too.
replace github.com/johnrichter/claude-shared-tooling/go/clikit => ../ai-shared-lib/go/clikit

replace github.com/johnrichter/claude-shared-tooling/go/fsx => ../ai-shared-lib/go/fsx

replace github.com/johnrichter/claude-shared-tooling/go/git => ../ai-shared-lib/go/git

replace github.com/johnrichter/claude-shared-tooling/go/githooks => ../ai-shared-lib/go/githooks

replace github.com/johnrichter/claude-shared-tooling/go/jsondoc => ../ai-shared-lib/go/jsondoc

replace github.com/johnrichter/claude-shared-tooling/go/logkit => ../ai-shared-lib/go/logkit

replace github.com/johnrichter/claude-shared-tooling/go/state => ../ai-shared-lib/go/state

replace github.com/johnrichter/claude-shared-tooling/go/sysops => ../ai-shared-lib/go/sysops
