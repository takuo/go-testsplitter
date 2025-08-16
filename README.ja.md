# 説明

過去のテスト実行時間に基づいて、多数のテストを複数ノードに分散して実行するスクリプトを出力します。
また、スクリプトで実行するテストバイナリを内部で事前ビルドします。

### 使い方

```bash
# 基本的な使い方
go list ./... | ./testsplitter -n 4 -- -test.timeout=20m -test.v
# パッケージ自動スキャン
testsplitter -s -n 4 -- -test.timeout=20m -test.v
# パッケージ除外 (`-s` 時のみ)
testsplitter -s -x "TestSomething|TestUnnecessaryCI" -n 4 -- -test.timeout=20m -test.v
# カスタムスクリプトテンプレート
testsplitter -s -t custom.sh.tmpl -n 4 -- -test.timeout=20m -test.v
```

## オプション

  | オプション                    | デフォルト           | 説明                                                                 | テンプレート変数         |
  |------------------------------|----------------------|----------------------------------------------------------------------|--------------------------|
  | -n, --nodes=INT              | 4                    | テスト実行ノード数。テンプレート内で {{ .NodeIndex }} で参照可能      |        |
  | -c, --concurrency=INT        | 4                    | 各ノード内での並列実行プロセス数                                            | {{ .Concurrency }}       |
  | -o, --scripts-dir=DIR        | ./test-scripts       | スクリプトの出力ディレクトリ                                         |                          |
  | -s, --scan-packages          | (標準入力)           | パッケージリストをスキャン。指定しない場合は標準入力から受け取る      |                          |
  | -x, --exclude=PATTERN        | (なし)               | `-s` 指定時に除外するパッケージの正規表現                               |                          |
  | -r, --report-dir=DIR         | ./test-reports       | 過去のテスト結果(JUnit XML)のディレクトリ                            | {{ .ReportDir }}         |
  | -m, --max-functions          | 0 (無制限)           | 1プロセスあたりの最大テスト関数の数                                    |                          |
  | -t, --template=FILE          | (組み込み)           | テストスクリプトのテンプレートファイル                               |                          |
  | -p, --binaries-dir=DIR       | ./test-bin           | テストバイナリの出力/事前ビルド先                                   | {{ .BinariesDir }}       |
  | -b, --build-concurrency=INT  | 4                    | テストバイナリのビルド並列数                                         |                          |
  | -d, --disable-build          | (ビルド有効)         | テストバイナリをビルドせず、事前ビルド済みを利用                     |                          |
  | -- ...                       | (なし)               | テストバイナリに渡す追加引数 (例: -test.v -test.timeout=20m)         |                          |

### 概要

* 標準入力（`go list ./...` の出力）からテストパッケージリストを受け取る
  * 受け取ったパッケージをASTで解析し、実行対象のテスト関数リストを取得
  * `-s --scan` 指定時はカレントディレクトリ配下の全パッケージが対象
    * `-s` では `-x --exclude PATTERN` で除外パッケージ指定も可能
* 過去の実行結果は `-r` で指定したディレクトリ配下のJUnit XMLを再帰的に読み込む
  * 過去結果にないテストは実行時間を暫定的に5秒として適切に分散
* テストバイナリは自動で事前ビルドされ、`./test-bin` に出力される (`-p`オプションで変更可能)
  * `-b` オプションで並列ビルド数を指定可能
  * `-d` オプション指定時はビルドをしないので、別途事前にビルドしておく必要がある `./test-bin` ディレクトリに `foo.bar.test` のように配置
* 組み込みテンプレート: `internal/templates/test-node.sh.tmpl`
  * 対象パッケージのテストバイナリは事前ビルド済み（`go test` ではなく）を利用する、テスト実行時はパッケージディレクトリに移動
  * パッケージ単位でコマンドを分割 例: `./test-bin/foo.bar.test -test.v -test.timeout=20m -test.run "^TestFooBar|TestHogeMoge$"`
    * 同一パッケージが複数ノードで実行される場合もあるが、`-test.run` で関数単位で実行するため重複実行は回避
    * 一つのプロセスで実行するテスト関数の数を制限可能 (`-m`)
  * ノード内並列実行には `xargs -P` を利用
  * テストは `gotestsum` 経由で実行し、JUnitレポートは `./test-reports/junit-[NODE INDEX]-[EXECUTE NUMBER].xml` 形式で出力
    * `./test-reports` は `-r` で指定したディレクトリ
  * `-t` オプションで独自テンプレートも利用可能
* テストスクリプトは `./test-scripts/test-node-$NODE_INDEX.sh` のように出力されるので、CI などで NODE_INDEX ごとに分散して実行する

## 例

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
