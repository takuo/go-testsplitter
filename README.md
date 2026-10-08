# Description

[![CI](https://github.com/takuo/go-testsplitter/actions/workflows/ci.yml/badge.svg)](https://github.com/takuo/go-testsplitter/actions/workflows/ci.yml)

Outputs scripts to distribute and run a large number of tests across multiple nodes based on the previous execution time.

### Usage

```bash
# Basic usage (import paths or relative directories from stdin)
go list ./... | testsplitter -n 4 -- -test.timeout=20m
# with auto scan packages
testsplitter -s -n 4 -- -test.timeout=20m
# with package exclusion (regex for import paths or directories)
testsplitter -s -x "/e2e$|/tools/" -n 4 -- -test.timeout=20m
# with custom script template
testsplitter -s -t custom.sh.tmpl -n 4 -- -test.timeout=20m
# preview the split without building binaries or writing scripts
testsplitter -s -n 4 --dry-run
# write the split plan as JSON
testsplitter -s -n 4 --plan plan.json -- -test.timeout=20m
# run only tests affected by changes since the merge base with origin/main (e.g. in pull requests)
testsplitter -s -n 4 --changed-since origin/main -- -test.timeout=20m
```

### Install

```bash
go install github.com/takuo/go-testsplitter/cmd/testsplitter@latest
```

Pre-built binaries for Linux and macOS are available on the [releases page](https://github.com/takuo/go-testsplitter/releases).

## Arguments

  | option                      | default             | description                                                                 | variable in template  |
  |-----------------------------|---------------------|-----------------------------------------------------------------------------|-----------------------|
  | -n, --nodes=INT             | 4                   | Number of test execution nodes                                              | {{.NodeIndex}}        |
  | -c, --concurrency=INT       | 4                   | Number of concurrency of test execution in a node                           | {{.Concurrency}}      |
  | -o, --scripts-dir=DIR       | ./test-scripts      | Output directory for scripts                                                |                       |
  | -s, --scan-packages         | (use stdin)         | Scan for package list; if not specified, receives from standard input        |                       |
  | -x, --exclude=PATTERN       | (none)              | Regular expression for packages to exclude (matched against import paths and directories) |      |
  | -j, --json-dir=DIR        | ./test-json      | Directory containing previous test results(JSONL)  (`go test -json` with package name)           | {{.JSONDir}}        |
  | -m, --max-functions         | 0  (unlimited)      | Maximum number of test functions per invoking a test process                 |                       |
  | -t, --template=FILE         | (built-in)          | Template file for test scripts                                               |                       |
  | --default-duration=DURATION | 0 (median)          | Duration assumed for tests without previous results. 0 uses the median of known durations (5s if none) |  |
  | --seed=UINT                 | 1                   | Random seed for splitting. The same input and seed produce the same scripts  |                       |
  | --changed-since=REV         | (none)              | Run only tests of packages affected by changes since the merge base of REV and HEAD |                |
  | --granularity=GRANULARITY   | package             | With `--changed-since`, select tests by affected packages (`package`) or by affected top-level declarations (`symbol`) |  |
  | --run-all-on=REGEX          | `(^\|/)go[.](mod\|sum\|work)$` | With `--changed-since`, run all tests if a changed file path (relative to the repository root) matches. `''` to disable |  |
  | --max-age=DURATION          | 0 (no limit)        | Ignore previous results older than this duration (e.g. `720h`)               |                       |
  | -p, --binaries-dir=DIR      | ./test-bin          | Path to test binaries, to output or pre-built                                | {{.BinariesDir}}      |
  | -b, --build-concurrency=INT | 4                   | Number of packages built in parallel (`go test -p`)                          |                       |
  | -d, --disable-build         | (build)             | Don't build test binaries, use pre-built by other way instead                |                       |
  | --dry-run                   | (off)               | Print the split plan without building test binaries or writing scripts       |                       |
  | --plan=FILE                 | (none)              | Write the split plan as JSON to FILE (`-` for stdout)                        |                       |
  | -q, --quiet                 | (off)               | Print only warnings and errors                                               |                       |
  | --verbose                   | (off)               | Print debug logs                                                             |                       |
  | -- ...                      | (none)              | Arguments to pass to the test binary (e.g., -test.v -test.timeout=20m)       | {{.Flags}}, {{.TestFlags}} |

### Overview

* **Packages**: receives a list of test packages from standard input (output of `go list ./...`)
  * Each line can be an import path (`github.com/foo/bar/api`) or a directory relative to the current directory (`api`, `./api`)
  * Packages are resolved with `go list`, and test files are parsed with AST (honoring build constraints) to obtain the top-level `TestXxx(t *testing.T)` functions
  * With `-s --scan-packages`, all packages under the current directory (`./...`) are targeted
  * Packages can be excluded using `-x --exclude PATTERN`
* **Previous results**: recursively reads all JSONL files (`go test -json` output with package names, e.g. `test2json -p "pkgname"`) under the directory specified by `-j`
  * If a test appears in multiple files, the newest result (by event time) wins. Results older than `--max-age` are ignored
  * Tests not found in previous results are assumed to take the median of known durations (or `--default-duration`)
  * The overhead of each package process (package elapsed time minus its tests, e.g. process startup and `TestMain`) is estimated from the results
* **Splitting**: tests are split at the function level to minimize the longest node time
  * The package overhead is added once to each node running the package, so tests of a costly package tend to stay on the same node
  * With `-m`, functions of a package are grouped into processes with balanced durations
  * Tests longer than the ideal time per node are reported as warnings, since they limit how evenly tests can be split
  * The split is deterministic: the same packages, previous results and `--seed` always produce the same scripts
* **Building**: test binaries are built with a single `go test -c` invocation (split into batches only when binary base names collide) into `-p` (`./test-bin`)
  * Binary names are the package directories joined with `.` (`api/foo` → `api.foo.test`), with `.` and `%` in path elements escaped (`a/b.c` → `a.b%2Ec.test`, `.` → `%2E.test`)
  * `test2json` is also built into the directory, so test nodes don't need the Go toolchain (`gotestsum` is still required)
* **Scripts**: `./test-scripts/test-node-[NODE INDEX].sh` are generated from the built-in template `internal/templates/test-node.sh.tmpl`, or your own with `-t`
  * Each process runs a test binary in the package directory with `-test.run "^(TestFoo|TestBar)$"`. The same package may run on multiple nodes, but each test runs only once
  * Processes run in parallel with `xargs -0 -P`, longest first
  * Tests are run via gotestsum, and JSONL files are output as `[JSON DIR]/test-[NODE INDEX]-[EXECUTE NUMBER].jsonl`, then merged into `test-[NODE INDEX].jsonl` even if some tests failed. The script exits with the failure status
  * Scripts of node indexes no longer generated (e.g. `test-node-7.sh` after reducing `-n` from 8 to 4) are removed
* Logs are written to stderr in `key=value` format. `--dry-run` and `--plan -` write to stdout

### Running only affected tests

With `--changed-since REV`, only tests of packages affected by the changes since the merge base of `REV` and `HEAD` are split into scripts, and only their test binaries are built.

* Changed files are committed and uncommitted changes since the merge base, including deleted, renamed and untracked files (`git diff --name-only --no-renames` and `git ls-files --others`)
* A file belongs to a package if it is in the package directory, under its `testdata` directory, or embedded with `//go:embed`
* A package is selected if its test binary depends on a changed package (including test-only imports), using `go list -deps -test`
* If a changed file matches `--run-all-on` (by default `go.mod`, `go.sum` and `go.work`), all packages are selected
* Dependencies not visible in the import graph (e.g. files read by relative paths outside the package, external services) are not detected. Running all tests on the main branch is recommended
* `--dry-run` and `--plan` show which packages are selected and why
* The base revision must be fetched, e.g. `fetch-depth: 0` with `actions/checkout`

#### `--granularity symbol`

By default, all tests of affected packages are selected, so adding a constant to a package imported by almost every package selects almost all tests. With `--granularity symbol`, tests are selected by top-level declarations (functions, methods, types, constants and variables):

* Changed declarations are found by comparing declarations in changed files at the merge base and now, ignoring comments and formatting. Moving a declaration between files is not a change
* Test functions transitively referencing a changed declaration are selected (type-checked with `golang.org/x/tools/go/packages`), e.g. adding a constant selects no tests, and changing a constant selects only tests reaching code using it
* A change of an exported method is treated as a change of its receiver type, since it may be called through interfaces of other packages or reflection. A change of an unexported method affects calls through interfaces of the same package
* All tests of a package are selected if its `TestMain`, or the `main` function of a command (whose tests often run the built command), depends on a change
* It falls back to selecting all tests of test binaries linking the package for changes which may affect a package beyond references: `init` functions, blank variables (`var _ = ...`), blank/dot imports, build constraints, cgo, `//go:linkname`, non-Go files (`testdata`, embedded files) and initialization depending on changed declarations
* Tests are run only through references: e.g. tests running another command built with `go build`, or calling methods by name via reflection, may be missed
* If the analysis fails (e.g. a type error), tests are selected by package

### Template variables

| variable | type | description |
|----------|------|-------------|
| `{{.NodeIndex}}` | int | Node index (0 origin) |
| `{{.Concurrency}}` | int | `-c` value |
| `{{.TestLines}}` | []TestLine | Test process invocations, longest first (see below) |
| `{{.JSONDir}}` | string | Absolute path of `-j` |
| `{{.BinariesDir}}` | string | Absolute path of `-p` |
| `{{.Flags}}` | string | Test flags joined with spaces (not quoted) |
| `{{.TestFlags}}` | []string | Test flags |

| TestLine field | type | description |
|----------------|------|-------------|
| `.Index` | int | Sequence number in the node (1 origin) |
| `.Package` | string | Package directory relative to the current directory |
| `.Binary` | string | File name of the test binary in `{{.BinariesDir}}` |
| `.TestPattern` | string | `-test.run` pattern, e.g. `^(TestA\|TestB)$` |
| `.Functions` | []string | Test functions in the process |
| `.Estimated` | time.Duration | Estimated duration including the package overhead |

The `shquote` function quotes a string for shells, e.g. `{{range .TestFlags}}{{shquote .}} {{end}}`.

## Examples

### circleci/config.yml

```yaml
parameters:
  test-parallelism:
    type: integer
    default: 6

jobs:
  build:
    environment:
      GOCACHE: /home/circleci/.cache/go-build
      GOPATH: /home/circleci/go

    working_directory: /home/circleci/project
    docker:
      - image: cimg/go:1.27
    resource_class: xlarge

    steps:
      - checkout
      - restore_cache:
          name: Restoring go module cache
          keys:
            - &mod-cache v1-go-mod-cache-{{ checksum "go.mod" }}
            - v1-go-mod-cache-
      - restore_cache:
          name: Restoring go build cache
          keys:
            - &build-cache v1-build-{{ .Branch }}-{{ .Revision }}
            - v1-build-{{ .Branch }}-
      - restore_cache:
          name: Restoring previous test results
          keys:
            - &test-results-cache v1-test-results-{{ .Branch }}-{{ epoch }}
            - v1-test-results-{{ .Branch }}-
            - v1-test-results-main-
            - v1-test-results-
      - run:
          name: Building test binaries
          command: |
            export GOGC=off CGO_ENABLED=0
            go install github.com/takuo/go-testsplitter/cmd/testsplitter@latest
            testsplitter -n << pipeline.parameters.test-parallelism >> -s -b 7 -c 4 -m 20 -- -test.timeout=10m
      - save_cache:
          name: Saving build cache
          key: *build-cache
          paths:
            - /home/circleci/.cache/go-build
      - save_cache:
          name: Saving go mod cache
          key: *mod-cache
          paths:
            - /home/circleci/go/pkg/mod
      - save_cache:
          name: Saving test binaries
          key: &test-bin-cache v1-test-bin-cache-{{ .Environment.CIRCLE_WORKFLOW_ID }}
          paths:
            - /home/circleci/project/test-bin
            - /home/circleci/project/test-scripts
  test:
    working_directory: /home/circleci/project
    docker:
      - image: cimg/base:current
    resource_class: medium
    parallelism: << pipeline.parameters.test-parallelism >>
    steps:
      - checkout
      - restore_cache:
          name: Restoring test binaries
          keys:
            - *test-bin-cache
      - run:
          name: Execute test
          command: |
            export GOGC=300
            bash ./test-scripts/test-node-$CIRCLE_NODE_INDEX.sh
          no_output_timeout: 10m
      - store_artifacts:
          path: test-reports
          when: always
      - store_test_results:
          path: test-reports
          when: always
      - persist_to_workspace:
          root: /home/circleci/project
          name: Saving test json output
          paths:
            - test-json
          when: always
  save-test-result:
    resource_class: small
    docker:
      - image: cimg/base:current
    steps:
      # previous results
      - restore_cache:
          name: Restoring previous test results
          keys:
            - v1-test-results-{{ .Branch }}-
            - v1-test-results-main-
            - v1-test-results-
      # results of this run (test-json/test-[NODE INDEX].jsonl)
      - attach_workspace:
          at: /home/circleci/project
      - run:
          name: Archiving test results of this run
          working_directory: /home/circleci/project/test-json
          command: |
            run="runs/$(date +%s)-${CIRCLE_WORKFLOW_ID}"
            mkdir -p "$run"
            for f in test-*.jsonl; do
              [ -e "$f" ] || continue
              # drop output events, which are not needed for splitting
              grep -v '"Action":"output"' "$f" > "$run/$f" || true
              rm "$f"
            done
            # keep the latest 20 runs
            ls -1d runs/*/ | sort -r | tail -n +21 | xargs -r rm -rf
      - save_cache:
          name: Saving JSON Test Reports
          key: *test-results-cache
          paths:
            - /home/circleci/project/test-json

workflows:
  build-test:
    jobs:
      - build
      - test:
          requires:
            - build
      - save-test-result:
          requires:
            - test:
              - success
              - failed
```

* Previous test results are restored only in the `build` job, where `testsplitter` reads them. The `test` job starts with an empty `test-json` and persists only the results of its node (`test-[NODE INDEX].jsonl`), so files of parallel nodes do not collide in the workspace
* The `save-test-result` job adds the results of this run to the previous results under `test-json/runs/`, instead of replacing them. Otherwise, when only some tests run (e.g. `--changed-since`), the results of the other tests are lost. `testsplitter` reads `-j` recursively and uses the newest result of each test
* The cache keys contain `{{ epoch }}` so that a new cache is saved every time, even when re-running a workflow of the same revision. New branches fall back to the results of `main`
* `when: always` saves test reports and results even if tests fail
* Use `--max-age` to ignore old results, and adjust the number of runs kept (`tail -n +21`)

### Running only affected tests in pull requests (CircleCI)

Replace the `Building test binaries` step above to run all tests on `main` and `release/*`, and only tests affected by the changes in other branches. The base branch of the pull request is fetched from the GitHub API, since CircleCI does not provide it.

```yaml
      - run:
          name: Building test binaries
          command: |
            export GOGC=off CGO_ENABLED=0
            go install github.com/takuo/go-testsplitter/cmd/testsplitter@latest

            SELECT=()
            case "$CIRCLE_BRANCH" in
              main|release/*)
                ;; # run all tests
              *)
                # the base branch of the pull request (empty on failure, then all tests run)
                BASE=""
                if [ -n "${CIRCLE_PULL_REQUEST:-}" ]; then
                  BASE=$(curl -fsSL \
                    ${GITHUB_TOKEN:+-H "Authorization: Bearer $GITHUB_TOKEN"} \
                    "https://api.github.com/repos/${CIRCLE_PROJECT_USERNAME}/${CIRCLE_PROJECT_REPONAME}/pulls/${CIRCLE_PULL_REQUEST##*/}" \
                    | jq -r '.base.ref // empty') || BASE=""
                fi
                if [ -n "$BASE" ]; then
                  git fetch --no-tags origin "+refs/heads/${BASE}:refs/remotes/origin/${BASE}"
                  SELECT=(--changed-since "origin/${BASE}" --granularity symbol)
                else
                  echo "Base branch is unknown, running all tests"
                fi
                ;;
            esac

            testsplitter -n << pipeline.parameters.test-parallelism >> -s -b 7 -c 4 -m 20 \
              "${SELECT[@]}" -- -test.timeout=10m
```

* To run all tests, omit `--changed-since` rather than using `--run-all-on '.*'`: `--run-all-on` matches changed files, so it selects no tests when nothing is changed (e.g. `--changed-since origin/main` on `main`)
* If the base branch is unknown (e.g. a branch without a pull request, or an API failure), all tests run
* Changes are compared with the merge base of the base branch and `HEAD`, so commits added to the base branch after the pull request was created are not included. For stacked pull requests, the base is the parent pull request's branch
* `GITHUB_TOKEN` (e.g. in a CircleCI context) with read access to the repository is required for private repositories. Without it, unauthenticated API requests are rate-limited
* `CIRCLE_PULL_REQUEST` is the first pull request of the branch (all of them are in `CIRCLE_PULL_REQUESTS`)
* With the GitHub CLI instead of `curl` and `jq`, get the base branch as follows. `gh` is not included in `cimg` images, and it requires `GH_TOKEN` even for public repositories. Keep the `CIRCLE_PULL_REQUEST` check, and `|| BASE=""` to run all tests when it fails
  ```bash
  BASE=""
  if [ -n "${CIRCLE_PULL_REQUEST:-}" ]; then
    BASE=$(gh pr view "$CIRCLE_PULL_REQUEST" --json baseRefName -q .baseRefName) || BASE=""
  fi
  ```

## Development

Tools are managed with [mise](https://mise.jdx.dev/) (`mise install`).

```bash
go test ./...        # the end-to-end test requires gotestsum
golangci-lint run
```

### Release

Push a `v*` tag, and the [Release workflow](.github/workflows/release.yml) publishes binaries with GoReleaser.

```bash
git tag v0.x.y
git push origin v0.x.y
```
