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
            - &test-results-cache v1-test-results-{{ .Branch }}-{{ .Revision }}
            - v1-test-results-{{ .Branch }}-
            - v1-test-results
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
      - store_test_results:
          path: test-reports
      - persist_to_workspace:
          root: /home/circleci/project
          name: Saving test json output
          paths:
            - test-json
  save-test-result:
    resource_class: small
    docker:
      - image: cimg/base:current
    steps:
      - attach_workspace:
          at: /home/circleci/project
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
