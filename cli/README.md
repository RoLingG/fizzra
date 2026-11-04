# The Fizzra CLI

The CLI is **not** a separate binary. `fizzra serve`, `fizzra scan`,
`fizzra ci` and `fizzra report` are subcommands of the same executable that
serves the web dashboard.

This file explains why, and documents the command surface.

---

## Why one binary

A separate CLI would mean two artefacts to build, version, install and keep in
sync — and the most common failure mode of that arrangement is a CLI that
disagrees with the server about the database schema or the rule format.

Everything the CLI needs is already in the binary: the analyzer registry, the
rule engine, the scoring model, the report renderers. `serve` adds an HTTP
server on top; `scan` does not.

The practical consequences:

- **CI needs no server.** `fizzra ci` runs a full scan and gate evaluation
  locally, with no daemon and no network.
- **One version number.** `fizzra version` describes the whole system.
- **One install.** Copy the binary; that is the entire deployment.

---

## Command surface

```
fizzra [global flags] <command> [flags]
```

| Command | Purpose |
|---|---|
| `serve` | Start the dashboard and JSON API, plus the background worker pool |
| `scan` | Run a scan and print a summary. Always exits 0 on a completed scan |
| `ci` | Run a scan and exit non-zero when the quality gate fails |
| `report` | Render JSON, HTML or SARIF for an existing or new scan |
| `rules` | `list`, `export`, `check` |
| `projects` | List registered projects with their latest score |
| `analyzers` | Show which analyzers are ready and which tools are missing |
| `version` | Print the build version |

### Global flags

| Flag | Meaning |
|---|---|
| `-c`, `--config <file>` | Configuration file. Defaults to `./fizzra.yaml`, then `~/.fizzra/config.yaml` |
| `-v`, `--verbose` | Enable debug logging |
| `-h`, `--help` | Print usage |

### Scan-family flags

`scan`, `ci` and `report` share these:

| Flag | Meaning |
|---|---|
| `--project <name\|id>` | Target a registered project instead of a path |
| `-f`, `--format <fmt>` | `text` (default), `json`, `html`, `sarif` |
| `-o`, `--output <file>` | Write to a file instead of stdout |
| `--analyzers <list>` | Comma separated analyzer names |
| `--min-score <n>` | Override the gate's minimum score |
| `--fail-on <levels>` | Comma separated levels that fail the gate |
| `--timeout <dur>` | Overall budget, e.g. `10m` (default `15m`) |
| `-q`, `--quiet` | Only print the summary line |

`ci` additionally accepts `--no-gate`, which always exits 0 and is useful for a
first, informational run.

---

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success. For `ci`, the gate passed |
| 1 | The gate failed, or the scan could not complete |
| 2 | Invalid usage — unknown command, bad flag, unknown format |

This is the whole CI contract. `scan` deliberately does not use exit code 1 for
findings, so it can be used in a pipeline step that reports without blocking.

---

## How the target is resolved

The positional argument is deliberately flexible, because the same command is
typed by a human and by a CI runner:

1. **A registered project** — matched by ID or name.
2. **A local directory** — used in place. If the directory is not yet
   registered, Fizzra registers it automatically and reuses that record on
   later runs, so a CI job with a changing checkout path does not create
   duplicate projects.
3. **Nothing** — the current directory.

```bash
fizzra scan                    # scan the current directory
fizzra scan ./my-app           # scan a path
fizzra scan --project my-app   # scan a registered project
fizzra scan my-app             # also works: name resolution
```

The Wails/Go distinction is inferred from the presence of `wails.json`, and can
be overridden in the dashboard.

---

## Logging and output

Logs go to **stderr**; report output goes to **stdout**. That is what makes this
work without any extra flags:

```bash
fizzra scan . --format json > report.json     # a clean JSON document
fizzra ci . --format sarif --output out.sarif # findings still on the console
```

---

## CI examples

### GitHub Actions

```yaml
- name: Fizzra
  run: fizzra ci . --format sarif --output fizzra.sarif --fail-on critical,error

- name: Upload SARIF
  if: always()
  uses: github/codeql-action/upload-sarif@v3
  with:
    sarif_file: fizzra.sarif
```

Upload the report before failing the build: SARIF is most useful precisely when
the gate fails. A complete workflow is in
[`deploy/github-actions.yml`](../deploy/github-actions.yml).

### GitLab CI

```yaml
fizzra:
  script:
    - fizzra ci . --format sarif --output fizzra.sarif
  artifacts:
    when: always
    reports:
      sarif: fizzra.sarif
```

### Local pre-commit hook

```bash
#!/bin/sh
# .git/hooks/pre-commit
fizzra scan . --quiet --analyzers custom,metrics --fail-on critical || {
  echo "Critical findings present. Run 'fizzra scan .' for detail."
  exit 1
}
```

A fast local check: only the always-available analyzers, so it does not depend
on any external tool being installed.

---

## Rules management

```bash
fizzra rules list                     # every rule with severity and state
fizzra rules export > fizzra.yaml   # dump the set for editing
fizzra rules check                    # exit 1 if any pattern fails to compile
```

`rules check` is worth running in CI for a repository that keeps its rules in
version control — a typo in a regular expression would otherwise silently
disable a rule.
