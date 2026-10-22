# Description

[![CI](https://github.com/takuo/go-testsplitter/actions/workflows/ci.yml/badge.svg)](https://github.com/takuo/go-testsplitter/actions/workflows/ci.yml)

Outputs scripts to distribute and run a large number of tests across multiple nodes based on the previous execution time.

### Assumptions

This tool is for large monorepos, where tests are spread over many packages and the wall clock time of `go test ./...` is the problem. It assumes that **test functions are independent of each other**, since splitting breaks the order and the grouping they had.

* Tests of a package are split across nodes and processes. A test cannot rely on another test having run before it, or on running in the same process: every run gives a package a different subset of its tests (each test still runs exactly once per run)
* Within a process, tests run in the order of `-test.run "^(TestA|TestB)$"`, and processes run in parallel (`-c`) on all nodes at the same time. Tests sharing an external resource (a database, a fixed port, a file outside the package) can therefore collide
* `TestMain` runs once per process, not once per package, so the setup and teardown of a package split into several processes run several times (this is the package overhead the splitter estimates and accounts for when splitting)
* **Subtests are never split**: the `t.Run` subtests of a top-level test always run together, in the same process, in their own order. Tests which must run in a given order, or which share state, belong in subtests of one top-level test function
* Tests which cannot be made independent can be excluded with `-x --exclude` and run in a separate job

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
# keep the results of this run as previous results of the next split (in CI, after all nodes finished)
testsplitter archive
```

`split` is the default command, so `testsplitter -s -n 4` is `testsplitter split -s -n 4`.
The other command is [`testsplitter archive`](#archiving-previous-results).

### Install

```bash
go install github.com/takuo/go-testsplitter/cmd/testsplitter@latest
```

Go 1.26 or later is required to build from source. Pre-built binaries for Linux and macOS, which need no toolchain, are available on the [releases page](https://github.com/takuo/go-testsplitter/releases).

## Arguments

Arguments of the `split` (default) command. See [Archiving previous results](#archiving-previous-results) for `testsplitter archive`.

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
  | --rerun-fails=INT           | 0 (no rerun)        | Rerun failed tests up to this many times in the generated scripts (`gotestsum --rerun-fails`) | {{.RerunFails}} |
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
  * Packages are resolved with `go list`, and test files are parsed with AST (honoring build constraints) to obtain the top-level functions `go test` runs: `TestXxx(t *testing.T)`, `FuzzXxx(f *testing.F)` (its seed corpus is run as a test) and `ExampleXxx()` with an output comment (`// Output:`)
  * Benchmarks, examples without an output comment and `TestMain` are not run by `go test` and are ignored
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
  * Failed tests are not rerun. With `--rerun-fails=N`, gotestsum reruns each failed test up to N times and reports them in `test-reports/rerun-[NODE INDEX]-[EXECUTE NUMBER].txt`. Note that this hides flaky tests, and the previous results then hold the duration of the last attempt
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
* The base revision must be fetched. In a shallow clone, where the merge base cannot be found, changes are compared with `REV` directly with a warning: changes on the `REV` side after the branch point are also included, and extra tests may be selected. To get exact changes, fetch the merge base commit (e.g. `merge_base_commit.sha` of the GitHub compare API) with `git fetch --depth=1 origin <SHA>` and pass it as `REV`

#### `--granularity symbol`

By default, all tests of affected packages are selected, so adding a constant to a package imported by almost every package selects almost all tests. With `--granularity symbol`, tests are selected by top-level declarations (functions, methods, types, constants and variables):

* Changed declarations are found by comparing declarations in changed files at the merge base and now, ignoring comments and formatting. Moving a declaration between files is not a change
* Test functions transitively referencing a changed declaration are selected (type-checked with `golang.org/x/tools/go/packages`), e.g. adding a constant selects no tests, and changing a constant selects only tests reaching code using it
* A change of an exported method is treated as a change of its receiver type, since it may be called through interfaces of other packages or reflection. A change of an unexported method affects calls through interfaces of the same package
* All tests of a package are selected if its `TestMain`, or the `main` function of a command (whose tests often run the built command), depends on a change
* It falls back to selecting all tests of test binaries linking the package for changes which may affect a package beyond references: `init` functions, blank variables (`var _ = ...`), blank/dot imports, build constraints, cgo, `//go:linkname`, non-Go files (`testdata`, embedded files) and initialization depending on changed declarations
* Tests are run only through references: e.g. tests running another command built with `go build`, or calling methods by name via reflection, may be missed
* If the analysis fails (e.g. a type error), tests are selected by package

## Archiving previous results

Splitting needs the previous results of **all** tests, not only of the last run: when only some tests ran (e.g. with `--changed-since`), replacing the previous results would lose the durations of the other tests. `testsplitter archive` adds the results of a run to the archived ones and keeps the archive small, so that CI only has to restore and save one directory:

```bash
# in CI, after the results of all nodes are collected into ./test-json
testsplitter archive --id "$CI_RUN_ID" --keep 20
```

  | option                   | default                | description                                                                 |
  |--------------------------|------------------------|-----------------------------------------------------------------------------|
  | FILE ...                 | JSON_DIR/test-*.jsonl  | Result files to archive                                                     |
  | -j, --json-dir=DIR       | ./test-json            | Directory containing the results of this run and the archived runs           |
  | --runs-dir=DIR           | JSON_DIR/runs          | Directory to archive runs into                                              |
  | --id=NAME                | (current UTC time)     | Name of the directory of this run, e.g. the CI workflow ID                  |
  | --keep=INT               | 20                     | Number of archived runs to keep, newest first (0: keep all)                 |
  | --max-age=DURATION       | 0 (no limit)           | Remove archived runs older than this duration (e.g. `720h`)                 |
  | --keep-output            | (off)                  | Archive all events including test output                                    |
  | --keep-source            | (off)                  | Keep the archived files in JSON_DIR instead of moving them                  |
  | --dry-run                | (off)                  | Print what would be archived and removed without writing anything            |
  | -q, --quiet              | (off)                  | Print only warnings and errors                                              |
  | --verbose                | (off)                  | Print debug logs                                                            |

* The results of this run are moved into `JSON_DIR/runs/[ID]/`, keeping their file names. The next split reads them together with the runs archived before, since `-j` is read recursively and the newest result of each test wins
* Only the events needed for splitting are archived: the results (`pass`, `fail`, `skip`) of packages and of top-level tests. Test output, which is almost all of the bytes of `go test -json`, and subtests are dropped. The archive is still `go test -json` output, so it can be read by other tools (`--keep-output` archives everything)
* Archived runs beyond `--keep` (newest first by directory time) and older than `--max-age` are removed
* Run it once per CI run, after the results of all nodes are collected (e.g. in a job depending on the test jobs, which runs even if they failed). Each node writes `test-[NODE INDEX].jsonl`, so the files of parallel nodes do not collide
* Restore the archived runs before archiving and save the whole `JSON_DIR` afterwards, so that the older runs are kept. Pass the CI run ID as `--id` to get a stable name per run
* Files given explicitly are archived even if they are not named `test-*.jsonl`. Files of already archived runs are skipped

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
| `{{.RerunFails}}` | int | `--rerun-fails` value (0 if reruns are disabled) |

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
      - image: cimg/go:1.27
    working_directory: /home/circleci/project
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
          command: |
            go install github.com/takuo/go-testsplitter/cmd/testsplitter@latest
            testsplitter archive --id "$CIRCLE_WORKFLOW_ID" --keep 20
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
* The `save-test-result` job adds the results of this run to the previous results under `test-json/runs/`, instead of replacing them. Otherwise, when only some tests run (e.g. `--changed-since`), the results of the other tests are lost. See [Archiving previous results](#archiving-previous-results)
* The cache keys contain `{{ epoch }}` so that a new cache is saved every time, even when re-running a workflow of the same revision. New branches fall back to the results of `main`
* `when: always` saves test reports and results even if tests fail
* The `cimg/go` image is only used to `go install testsplitter`; with the [pre-built binary](https://github.com/takuo/go-testsplitter/releases) a slim image is enough
* Keep fewer runs with `--keep`; `archive --max-age` removes old runs, and `split --max-age` ignores old results

### Running only affected tests in pull requests (CircleCI)

Replace the `Building test binaries` step above to run all tests on `main` and `release/*`, and only tests affected by the changes in other branches. The base branch and the merge base of the pull request are fetched from the GitHub API, since CircleCI does not provide them and the merge base is not in a shallow clone.

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
                  # the merge base of the pull request, which is not in a shallow clone
                  MERGE_BASE=$(curl -fsSL \
                    ${GITHUB_TOKEN:+-H "Authorization: Bearer $GITHUB_TOKEN"} \
                    "https://api.github.com/repos/${CIRCLE_PROJECT_USERNAME}/${CIRCLE_PROJECT_REPONAME}/compare/${BASE}...${CIRCLE_SHA1}" \
                    | jq -r '.merge_base_commit.sha // empty') || MERGE_BASE=""
                  if [ -n "$MERGE_BASE" ]; then
                    git fetch --depth=1 --no-tags origin "$MERGE_BASE"
                    SELECT=(--changed-since "$MERGE_BASE" --granularity symbol)
                  else
                    # the base branch tip: changes on the base branch after the branch point are also included
                    git fetch --depth=1 --no-tags origin "+refs/heads/${BASE}:refs/remotes/origin/${BASE}"
                    SELECT=(--changed-since "origin/${BASE}" --granularity symbol)
                  fi
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
* Changes are compared with the merge base of the pull request (fetched by SHA with `--depth=1`), so commits added to the base branch after the pull request was created are not included. For stacked pull requests, the base is the parent pull request's branch
* If the merge base cannot be fetched from the API, the base branch tip is used. Then changes on the base branch after the branch point are also included and extra tests may run, but no affected tests are missed
* `GITHUB_TOKEN` (e.g. in a CircleCI context) with read access to the repository is required for private repositories. Without it, unauthenticated API requests are rate-limited
* `CIRCLE_PULL_REQUEST` is the first pull request of the branch (all of them are in `CIRCLE_PULL_REQUESTS`)
* With the GitHub CLI instead of `curl` and `jq`, get the base branch as follows. `gh` is not included in `cimg` images, and it requires `GH_TOKEN` even for public repositories. Keep the `CIRCLE_PULL_REQUEST` check, and `|| BASE=""` to run all tests when it fails
  ```bash
  BASE=""
  if [ -n "${CIRCLE_PULL_REQUEST:-}" ]; then
    BASE=$(gh pr view "$CIRCLE_PULL_REQUEST" --json baseRefName -q .baseRefName) || BASE=""
  fi
  ```

### .github/workflows/test.yml (GitHub Actions)

```yaml
name: Test

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read

env:
  TEST_PARALLELISM: 6

jobs:
  build:
    runs-on: ubuntu-latest
    outputs:
      nodes: ${{ steps.split.outputs.nodes }}
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version-file: go.mod
      - name: Restoring previous test results
        uses: actions/cache/restore@v4
        with:
          path: test-json
          key: test-results-${{ github.ref_name }}-${{ github.run_id }}
          restore-keys: |
            test-results-${{ github.ref_name }}-
            test-results-main-
      - name: Building test binaries
        id: split
        env:
          GOGC: "off"
          CGO_ENABLED: "0"
        run: |
          go install github.com/takuo/go-testsplitter/cmd/testsplitter@latest
          testsplitter -s -n "$TEST_PARALLELISM" -b 4 -c 4 -m 20 \
            --plan plan.json -- -test.timeout=10m
          # Only the nodes which got tests, so that no job runs an empty script
          echo "nodes=$(jq -c '[.nodes[] | select(.processes | length > 0) | .index]' plan.json)" >> "$GITHUB_OUTPUT"
      - name: Saving test binaries
        uses: actions/upload-artifact@v4
        with:
          name: test-bin
          path: |
            test-bin
            test-scripts
          retention-days: 1

  test:
    needs: build
    if: needs.build.outputs.nodes != '[]'
    runs-on: ubuntu-latest
    strategy:
      fail-fast: false
      matrix:
        node: ${{ fromJSON(needs.build.outputs.nodes) }}
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version-file: go.mod
      - name: Installing gotestsum
        run: go install gotest.tools/gotestsum@latest
      - name: Restoring test binaries
        uses: actions/download-artifact@v4
        with:
          name: test-bin
      - name: Execute test
        env:
          GOGC: "300"
        run: |
          # Artifacts do not keep the executable bit
          chmod +x test-bin/* test-scripts/*.sh
          bash ./test-scripts/test-node-${{ matrix.node }}.sh
      - name: Saving test json output
        if: always()
        uses: actions/upload-artifact@v4
        with:
          name: test-json-${{ matrix.node }}
          path: test-json
      - name: Saving test reports
        if: always()
        uses: actions/upload-artifact@v4
        with:
          name: test-reports-${{ matrix.node }}
          path: test-reports

  save-test-result:
    needs: [build, test]
    if: always() && needs.build.result == 'success' && needs.build.outputs.nodes != '[]'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-go@v7
        with:
          go-version: stable
      - run: go install github.com/takuo/go-testsplitter/cmd/testsplitter@latest
      # previous results
      - name: Restoring previous test results
        uses: actions/cache/restore@v4
        with:
          path: test-json
          key: test-results-${{ github.ref_name }}-${{ github.run_id }}
          restore-keys: |
            test-results-${{ github.ref_name }}-
            test-results-main-
      # results of this run (test-json/test-[NODE INDEX].jsonl of every node)
      - name: Collecting test results of this run
        uses: actions/download-artifact@v4
        with:
          pattern: test-json-*
          merge-multiple: true
          path: test-json
      - name: Archiving test results of this run
        run: testsplitter archive --id "${{ github.run_id }}-${{ github.run_attempt }}" --keep 20
      - name: Saving JSON test reports
        uses: actions/cache/save@v4
        with:
          path: test-json
          key: test-results-${{ github.ref_name }}-${{ github.run_id }}-${{ github.run_attempt }}
```

* The `test` matrix is built from the `--plan` JSON, so nodes which got no test (e.g. with `--changed-since`) start no job
* Previous test results are restored only where they are read or written, never in the `test` jobs, so that each node uploads the results of its own node (`test-[NODE INDEX].jsonl`) and the artifacts can be merged with `merge-multiple`
* An exact `key` which is never saved, with `restore-keys` prefixes, restores the newest matching cache: a cache is immutable, so a new one is saved for every run (`github.run_id`), and branches fall back to the results of `main`. Caches of a pull request are restorable only in its own runs and those of the base branch
* `save-test-result` depends on the test jobs with `if: always()`, so the results are archived even if tests failed. See [Archiving previous results](#archiving-previous-results)
* The scripts hold the absolute paths of `-j` and `-p`, which match because GitHub-hosted runners check out the repository at the same path in every job
* The `test` job needs only `gotestsum`, since `test2json` is built into `test-bin`. Installing it with `go install` is the simplest way, but the [pre-built binary](https://github.com/gotestyourself/gotestsum/releases) avoids the Go toolchain

### Running only affected tests in pull requests (GitHub Actions)

Check out the full history and replace the `Building test binaries` step above to run all tests on `main`, and only tests affected by the changes in pull requests.

```yaml
      - uses: actions/checkout@v7
        with:
          fetch-depth: 0 # the merge base must be in the clone
      # ... setup-go and the cache steps ...
      - name: Building test binaries
        id: split
        env:
          GOGC: "off"
          CGO_ENABLED: "0"
          BASE_REF: ${{ github.event.pull_request.base.ref }}
        run: |
          go install github.com/takuo/go-testsplitter/cmd/testsplitter@latest

          SELECT=()
          if [ -n "${BASE_REF:-}" ]; then
            SELECT=(--changed-since "origin/${BASE_REF}" --granularity symbol)
          fi

          testsplitter -s -n "$TEST_PARALLELISM" -b 4 -c 4 -m 20 \
            "${SELECT[@]}" --plan plan.json -- -test.timeout=10m
          echo "nodes=$(jq -c '[.nodes[] | select(.processes | length > 0) | .index]' plan.json)" >> "$GITHUB_OUTPUT"
```

* `BASE_REF` is set only for `pull_request` events, so all tests run on `main`. To run all tests, omit `--changed-since` rather than using `--run-all-on '.*'`: `--run-all-on` matches changed files, so it selects no tests when nothing is changed
* `pull_request` events check out the merge commit of the pull request, whose first parent is the base branch. The merge base of `origin/$BASE_REF` and `HEAD` is then the tip of the base branch, and exactly the changes of the pull request are compared
* `fetch-depth: 0` fetches all branches and the full history, which the merge base needs. In a large repository, check out shallow and fetch only the base commit instead:
  ```yaml
      - uses: actions/checkout@v7
      - name: Fetching the base commit of the pull request
        if: github.event_name == 'pull_request'
        env:
          BASE_SHA: ${{ github.event.pull_request.base.sha }}
        run: git fetch --depth=1 --no-tags origin "$BASE_SHA"
  ```
  and pass `--changed-since "$BASE_SHA"` in the split step, with `BASE_SHA` in its `env` too. The merge base is not in a shallow clone, so the changes are compared with `base.sha` directly: commits pushed to the base branch after the pull request was last synchronized are also included, and extra tests may run
* Values from the event are passed through `env` instead of being interpolated into the script, so that a branch name cannot inject shell code
* `permissions: contents: read` is enough; no token is needed to find the base branch, unlike CircleCI

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
