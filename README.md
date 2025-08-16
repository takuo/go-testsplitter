# Description

Outputs scripts to distribute and run a large number of tests across multiple nodes based on the previous execution time.

### Usage

```bash
# Basic usage
go list ./... | testsplitter -n 4 -- -test.timeout=20m -test.v
# with auto scan packages
testsplitter -s -n 4 -- -test.timeout=20m -test.v
# with package exclusion (only for `-s`)
testsplitter -s -x "TestSomething|TestUnnecessaryCI" -n 4 -- -test.timeout=20m -test.v
# with custom script template
testsplitter -s -t custom.sh.tmpl -n 4 -- -test.timeout=20m -test.v
```

## Specification

* Supports running Go language tests
* Arguments
  | option                      | default             | description                                                                 | variable in template  |
  |-----------------------------|---------------------|-----------------------------------------------------------------------------|-----------------------|
  | -n, --nodes=INT             | 4                   | Number of test execution nodes, NodeIndex is can be refered in template  with {{ .NodeIndex }} |   |
  | -c, --concurrency=INT       | 4                   | Number of concurrency of test execution in a node                           | {{.Concurrency}}      |
  | -o, --scripts-dir=DIR       | ./test-scripts      | Output directory for scripts                                                |                       |
  | -s, --scan-packages         | (use stdin)         | Scan for package list; if not specified, receives from standard input        |                       |
  | -x, --exclude=PATTERN       | (none)              | Regular expression for packages to exclude when -s is specified              |                       |
  | -r, --report-dir=DIR        | ./test-reports      | Directory containing previous test results (JUnit Test Report XML)           | {{.ReportDir}}        |
  | -m, --max-functions         | 0  (unlimited)      | Maximum number of test functions per invoking a test process                 |                       |
  | -t, --template=FILE         | (built-in)          | Template file for test scripts                                               |                       |
  | -p, --binaries-dir=DIR      | ./test-bin          | Path to test binaries, to output or pre-built                                | {{.BinariesDir}}      |
  | -b, --build-concurrency=INT | 4                   | Number of parallel builds                                                    |                       |
  | -d, --disable-build         | (build)             | Don't build test binaries, use pre-built by other way instead                |                       |
  | -- ...                      | (none)              | Arguments to pass to the test binary (e.g., -test.v -test.timeout=20m)       |                       |

### Overview

* Receives a list of test packages from standard input (output of `go list ./...`)
  * Parses the received packages with AST to obtain a list of test functions to execute
  * If the `-s --scan` argument is specified, all packages under the current directory are targeted
    * With `-s`, you can also specify packages to exclude using `-x --exclude PATTERN`
* For previous execution results, recursively reads all JUnit Test Report XML files under the directory specified by `-r`
  * Tests not found in previous results are distributed appropriately
* Built-in template: `internal/templates/test-node.sh.tmpl`
  * Assumes that test binaries for the packages to be executed are pre-built (instead of `go test`), and changes the current directory to the package directory when running tests
  * Assumes test binaries are named like `./test-bin/foo.bar.test`
  * Execution is divided by package, resulting in commands like `./test-bin/foo.bar.test -test.v -test.timeout=20m -test.run "^TestFooBar|TestHogeMoge$"`
    * However, since distribution is at the test function level, the same package may be tested on multiple nodes, but duplication is avoided by specifying `-test.run`
  * Uses `xargs -P` for parallel execution within a node
  * Tests are run via gotestsum, and junit report files are output in the format `./test-reports/junit-[NODE INDEX]-[EXECUTE NUMBER].xml`
  * You can use own custom template with `-t` option.

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
      - image: cimg/go:1.24
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
            testsplitter -n << pipeline.parameters.test-parallelism >> -s -b 7 -c 4 -m 20 -- -test.v -test.timeout=10m
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
          name: Saving test result
          paths:
            - test-reports
  save-test-result:
    resource_class: small
    docker:
      - image: cimg/base:current
    steps:
      - attach_workspace:
          at: /home/circleci/project
      - save_cache:
          name: Saving JUnit Test Reports
          key: *test-results-cache
          paths:
            - /home/circleci/project/test-reports

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