# git-tools

Small, focused command-line tools for working with git.

## Claude Code setup

This repo's `.claude/settings.json` enables plugins from the `jr-claude-plugins` marketplace. Register it once at the Claude user level — repo settings carry no machine-specific paths:

```sh
claude plugin marketplace add git@github.com:johnrichter/claude-marketplace.git
# or, with the psa-platform repos checked out as siblings:
claude plugin marketplace add ../marketplace-public
```

Knowledge bases are configured at the Claude user level, not per repo.

## Known limitations

- `plugin/routing-rules.json`'s `branch-create` raw route matches on the bare `git branch` prefix, so a non-create/-delete invocation with a long-form flag (e.g. `git branch --delete`, `git branch -m`) redirects to `git-tools branch create` instead of failing open or naming the matching git-tools subcommand. `branch-delete`'s `-d`/`-D` prefixes are checked first and match correctly; only other flag forms are affected. Acceptable for MVP.
