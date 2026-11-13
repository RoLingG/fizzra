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

jobs:
  quality:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6

      - uses: actions/setup-go@v6
        with:
          go-version: '1.27.3'   # must satisfy go.mod

      - uses: RoLingG/fizzra@v0.1.3
        with:
          fail-on: critical
```

That is the whole integration. Removing the `uses:` line removes Fizzra; it
writes nothing into your repository and does not change your workflow's
triggers.

Pin the action to a release tag rather than to a branch, so an upstream change
cannot alter your pipeline without you deciding to take it.

**Fizzra does not install language toolchains.** Use the official setup actions
(`actions/setup-go`, `actions/setup-python`, `actions/setup-node`) as you
already do, then list the analyzers you want. This keeps tool versions under
your control and avoids an action that silently pins a compiler for you.

The Go toolchain version must satisfy the `go` directive in your `go.mod`.
Fizzra runs `go vet` against your module, and that command refuses to run when
the installed toolchain is older than the module declares.

### What a skipped analyzer does

An analyzer whose tool is not installed is **skipped**, not failed. The scan
carries on with the analyzers that can run, and the summary lists the skipped
ones along with the missing tools:

```
Note  skipped analyzers (tool not installed): golangci, gosec, staticcheck
```

This does **not** fail the step. A repository with only the Go toolchain
installed still gets a score, just from fewer checks.

Two cases do fail, because in both the result cannot be trusted:

- **every** analyzer was skipped — nothing inspected the code, so the score
  means nothing
- an analyzer **crashed or timed out** — a broken run, not a missing tool

Set `allow-skipped: true` to relax the first case into a pass. It does not
relax the second: a crash stays a failure no matter what.

### Inputs

| Input | Default | Meaning |
| --- | --- | --- |
| `path` | `.` | Directory to scan, relative to the workspace |
| `analyzers` | (all applicable) | Comma separated analyzer names, e.g. `custom,metrics,wails` |
| `fail-on` | (from config) | Severities that fail the gate: `critical`, `error`, `warning`. Empty keeps the project's own thresholds |
| `min-score` | from config (70) | Override the minimum score. Below it the gate fails |
| `allow-skipped` | `false` | Pass when every analyzer was skipped for missing tools |
| `reports` | `sarif` | Comma separated formats to produce: `sarif`, `json`, `html` |
| `version` | `latest` | Release to download — pin a tag in CI |
| `upload-sarif` | `true` | Upload with codeql-action. Needs `sarif` in `reports` and `security-events: write` |

#### `fail-on` and `min-score`

Two independent ways to fail a build. `fail-on` counts findings by severity;
`min-score` compares the overall score. Either one can fail the step.

```yaml
# Fail on any critical or error finding, and also below 80 points.
- uses: RoLingG/fizzra@v0.1.3
  with:
    fail-on: critical,error
    min-score: '80'
```

Warnings never fail on their own. Raise `fail-on` to include them only if you
want style-level findings to block merges; the score already reflects them.

#### `reports`

One list of formats. Every file it names is written from the **same analysis**
— no second scan — and attached to a single artifact called `fizzra-reports`.

```yaml
# SARIF for code scanning plus JSON and HTML for humans.
- uses: RoLingG/fizzra@v0.1.3
  with:
    reports: sarif,json,html
```

| Value | File in the artifact |
| --- | --- |
| `sarif` | `fizzra.sarif` |
| `json` | `fizzra.json` |
| `html` | `fizzra-report.html` |

The artifact is uploaded even when the gate fails, so a red build still carries
the evidence. An empty `reports` writes nothing and only prints the summary
into the job log.

To download the artifact from another job:

```yaml
- uses: actions/download-artifact@v6
  with:
    name: fizzra-reports
```

`upload-sarif` is separate from `reports`: it decides whether the SARIF file is
pushed to code scanning, not whether it is produced. Asking for `sarif` in
`reports` while `upload-sarif: false` gives you the file without the annotations.

#### `analyzers`

Empty runs every analyzer applicable to the detected language. Naming a subset
is how you trade coverage for speed:

```yaml
- uses: RoLingG/fizzra@v0.1.3
  with:
    analyzers: custom,metrics      # no external tools needed, runs in seconds
```

#### `path`

Scans a subdirectory instead of the repository root. Useful in a monorepo
where each package has its own workflow:

```yaml
- uses: RoLingG/fizzra@v0.1.3
  with:
    path: services/api
```

### Outputs

| Output | Meaning |
| --- | --- |
| `score` | Overall score, 0–100 |
| `gate` | `passed` or `failed` |
| `project` | The name the scanned directory was registered under |

Consume them in later steps:

```yaml
- uses: RoLingG/fizzra@v0.1.3
  id: scan

- run: echo "Scored ${{ steps.scan.outputs.score }} (${{ steps.scan.outputs.gate }})"
```

### Reading the result

The step's exit code is the gate. The summary in the log carries the score and
the per-file findings, and the SARIF report lands under **Security → Code
scanning**. Severity levels work like this:

| Level | Fails the gate by default | Meaning |
| --- | --- | --- |
| `critical` | yes | A definite vulnerability or crash |
| `error` | yes | A definite defect |
| `warning` | no | Suspicious code; affects the score |
| `info` | no | Advisory |

A warning-level finding lowers the score without failing the build. To see
lower scores enforced, set `min-score`; to make warnings fail, widen `fail-on`.

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
| JS/TS | tsc | `npx` (project's TypeScript) | `npm install --save-dev typescript` in the project | Also needs `vue-tsc` for `.vue` files. Installing globally has no effect: the analyzer runs the project's own compiler. |
| Python | ruff | `ruff` | `pip install ruff` | Falls back to `python -m ruff`. |
| Rust | clippy, rustfmt | `cargo` | `rustup component add clippy rustfmt` | |
| Rust | cargo-audit | `cargo-audit` | `cargo install cargo-audit --locked` | Needs network unless `--offline`. |
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
