# Fizzra in CI

Two ways to run Fizzra in GitHub Actions:

| Approach | Use when |
| --- | --- |
| **Composite action** (`action.yml`) | You want a drop-in step. Three lines in your existing workflow; deleting them removes Fizzra entirely. |
| **Template workflow** (`github-actions.yml`) | You want a complete reference workflow to copy and adapt. |

## The composite action

```yaml
permissions:
  contents: read
  security-events: write   # only needed for the SARIF upload

steps:
  - uses: actions/checkout@v4

  - uses: actions/setup-go@v5
    with: { go-version: '1.27' }

  - uses: rolingg/fizzra@v1
    with:
      analyzers: go            # optional; omit to run everything applicable
      fail-on: critical,error
```

That is the whole integration. Removing the `uses:` line removes Fizzra; it
writes nothing into your repository and does not change your workflow's
triggers.

**Fizzra does not install language toolchains.** Use the official setup
actions (`actions/setup-go`, `actions/setup-python`, `actions/setup-node`) as
you already do, then list the analyzers you want. This keeps tool versions under
your control and avoids an action that silently pins a compiler for you.

### Missing tools

By default a scan that skipped analyzers because their tool was absent **fails**
the step. That is deliberate: a green build that checked nothing is worse than a
red one. The error names the missing tools.

While adopting, set `allow-skipped: true` to let those runs pass with a warning
instead. Two things still fail even with it on:

- an analyzer that **crashed or timed out** — that is a broken run, not a
  missing tool
- a scan where **no analyzer completed at all** — nothing was measured, so there
  is nothing to pass

### Inputs

| Input | Default | Meaning |
| --- | --- | --- |
| `path` | `.` | Directory to scan |
| `analyzers` | (all applicable) | Comma separated analyzer names |
| `fail-on` | `critical,error` | Severities that fail the gate |
| `min-score` | from config | Override the minimum score |
| `allow-skipped` | `false` | Pass when the only gap is missing tools |
| `format` | `sarif` | `sarif`, `json`, `html` or `text` |
| `output` | by format | Report path |
| `version` | `latest` | Release to download — **pin a tag in CI** |
| `upload-sarif` | `true` | Upload with codeql-action |

### Outputs

| Output | Meaning |
| --- | --- |
| `score` | Overall score, 0–100 |
| `gate` | `passed` or `failed` |
| `report-path` | Where the report was written |

## The template workflow

`github-actions.yml` and `gitlab-ci.yml` drop into a repository. Copy the one you
need into `.github/workflows/` or use it as a `include:` target.

External analyzers are optional. An analyzer whose tool is not installed is
reported as **skipped**, not failed. Install the tools for the languages your
repository actually uses.

### Installing the tools

`github-actions.yml` installs the Go tools unconditionally and gates everything
else on the `with-analyzers` input, which takes a space-separated list. It is
only exposed through `workflow_dispatch`; on push and pull request it defaults
to `go`. Trigger a manual run to install more, or hardcode the value by passing
it to the reusable workflow.

`gitlab-ci.yml` pins the `golang` image and installs only the Go tools. Adding
another language there means switching to a base image that carries that
toolchain, or installing it in `before_script`; the matrix below is what to
install either way.

```yaml
with-analyzers: go                     # default: golangci-lint, gosec, govulncheck
with-analyzers: go js python           # add JavaScript and Python tools
with-analyzers: all                    # every language below
```

## Language matrix

| Language | Analyzer | Tool | Ubuntu install | Notes |
| --- | --- | --- | --- | --- |
| Go | golangci | `golangci-lint` | `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest` | |
| Go | gosec | `gosec` | `go install github.com/securego/gosec/v2/cmd/gosec@latest` | |
| Go | vulnerability | `govulncheck` | `go install golang.org/x/vuln/cmd/govulncheck@latest` | Needs network unless `--offline`. |
| Go | gofmt, govet | `gofmt`, `go` | part of the Go toolchain | |
| JS/TS | oxlint | `oxlint` | `npm i -g oxlint` | Or `npx` from the project's `node_modules`. |
| JS/TS | biome | `biome` | `npm i -g @biomejs/biome` | |
| JS/TS | tsc | `npx` (project's TypeScript) | `npm i -g typescript` | Also needs `vue-tsc` for `.vue` files. |
| Python | ruff | `ruff` | `pip install ruff` | Falls back to `python -m ruff`. |
| Rust | clippy, rustfmt | `cargo` | `rustup component add clippy rustfmt` | |
| Rust | cargo-audit | `cargo-audit` | `cargo install cargo-audit` | Needs network unless `--offline`. |
| Java | pmd | `pmd` | download `pmd-dist-*-bin.zip` from https://github.com/pmd/pmd/releases, add `bin/` to PATH | Or set `FIZZRA_PMD_HOME`. |
| Java | spotbugs | `spotbugs` | download `spotbugs-*.tgz` from https://github.com/spotbugs/spotbugs/releases, add `bin/` to PATH | Or set `FIZZRA_SPOTBUGS_HOME`. Uses the launcher, not the jar. |
| C# | roslynator | `roslynator` | `dotnet tool install --global Roslynator.DotNet.Cli` | Add `~/.dotnet/tools` to PATH. |
| C/C++ | cppcheck | `cppcheck` | `sudo apt-get install cppcheck`, or download from https://github.com/cppcheck-opensource/cppcheck/releases | |
| C/C++ | clang-tidy | `clang-tidy` | `sudo apt-get install clang-tidy`, or `pip install clang-tidy` | Several hundred MB via apt; the most expensive tool to install. |
| PHP | phpstan | `phpstan`, `phpstan.phar` | download `phpstan.phar` from https://github.com/phpstan/phpstan/releases, plus `sudo apt-get install php-cli` | Or set `FIZZRA_PHPSTAN_HOME`. |

The repository links above are the same ones the app shows in its missing-tool
hints, and the ones the CI template downloads from. Prefer the project's own
GitHub Releases page over a package-manager copy when both exist: it is the
version the analyzer is tested against.

Tools that are not language-specific (the `metrics` and `custom` rule engine
analyzers) need no installation.

## Checking what is available

Run `fizzra analyzers` on the runner to list each analyzer as `ready` or
`missing` along with the install hint for anything absent. A scan that skipped
an analyzer because its tool was missing marks the result as incomplete, so a
green gate with partial coverage is reported as such rather than as a clean
pass.
