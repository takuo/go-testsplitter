# 説明

[![CI](https://github.com/takuo/go-testsplitter/actions/workflows/ci.yml/badge.svg)](https://github.com/takuo/go-testsplitter/actions/workflows/ci.yml)

過去のテスト実行時間に基づいて、多数のテストを複数ノードに分散して実行するスクリプトを出力します。
また、スクリプトで実行するテストバイナリを内部で事前ビルドします。

### 使い方

```bash
# 基本的な使い方 (標準入力でインポートパスまたは相対ディレクトリを渡す)
go list ./... | testsplitter -n 4 -- -test.timeout=20m
# パッケージ自動スキャン
testsplitter -s -n 4 -- -test.timeout=20m
# パッケージ除外 (インポートパスまたはディレクトリに対する正規表現)
testsplitter -s -x "/e2e$|/tools/" -n 4 -- -test.timeout=20m
# カスタムスクリプトテンプレート
testsplitter -s -t custom.sh.tmpl -n 4 -- -test.timeout=20m
# ビルドやスクリプト出力をせずに分割結果を確認
testsplitter -s -n 4 --dry-run
# 分割結果を JSON で出力
testsplitter -s -n 4 --plan plan.json -- -test.timeout=20m
```

### インストール

```bash
go install github.com/takuo/go-testsplitter/cmd/testsplitter@latest
```

Linux / macOS 向けのビルド済みバイナリは [Releases](https://github.com/takuo/go-testsplitter/releases) から取得できます。

## オプション

  | オプション                    | デフォルト           | 説明                                                                 | テンプレート変数         |
  |------------------------------|----------------------|----------------------------------------------------------------------|--------------------------|
  | -n, --nodes=INT              | 4                    | テスト実行ノード数                                                    | {{ .NodeIndex }}         |
  | -c, --concurrency=INT        | 4                    | 各ノード内での並列実行プロセス数                                            | {{ .Concurrency }}       |
  | -o, --scripts-dir=DIR        | ./test-scripts       | スクリプトの出力ディレクトリ                                         |                          |
  | -s, --scan-packages          | (標準入力)           | パッケージリストをスキャン。指定しない場合は標準入力から受け取る      |                          |
  | -x, --exclude=PATTERN        | (なし)               | 除外するパッケージの正規表現 (インポートパスとディレクトリに対して評価) |                          |
  | -j, --json-dir=DIR           | ./test-json          | 過去のテスト結果(JSONL) (`go test -json` 出力)のディレクトリ                      | {{ .JSONDir }}         |
  | -m, --max-functions          | 0 (無制限)           | 1プロセスあたりの最大テスト関数の数                                    |                          |
  | -t, --template=FILE          | (組み込み)           | テストスクリプトのテンプレートファイル                               |                          |
  | --default-duration=DURATION  | 0 (中央値)           | 過去結果がないテストの想定実行時間。0 の場合は既知の実行時間の中央値 (結果が全くなければ 5s) |  |
  | --seed=UINT                  | 1                    | 分割の乱数シード。同じ入力とシードなら常に同じスクリプトを生成        |                          |
  | -p, --binaries-dir=DIR       | ./test-bin           | テストバイナリの出力/事前ビルド先                                   | {{ .BinariesDir }}       |
  | -b, --build-concurrency=INT  | 4                    | 並列にビルドするパッケージ数 (`go test -p`)                          |                          |
  | -d, --disable-build          | (ビルド有効)         | テストバイナリをビルドせず、事前ビルド済みを利用                     |                          |
  | --dry-run                    | (無効)               | テストバイナリのビルドやスクリプト出力をせず、分割結果を表示         |                          |
  | --plan=FILE                  | (なし)               | 分割結果を JSON で FILE に出力 (`-` で標準出力)                      |                          |
  | -q, --quiet                  | (無効)               | 警告とエラーのみ出力                                                 |                          |
  | --verbose                    | (無効)               | デバッグログを出力                                                   |                          |
  | -- ...                       | (なし)               | テストバイナリに渡す追加引数 (例: -test.v -test.timeout=20m)         | {{ .Flags }}, {{ .TestFlags }} |

### 概要

* 標準入力（`go list ./...` の出力）からテストパッケージリストを受け取る
  * 各行はインポートパス (`github.com/foo/bar/api`) またはカレントディレクトリからの相対ディレクトリ (`api`, `./api`)
  * パッケージは `go list` で解決し、テストファイルを AST で解析 (ビルド制約を考慮) してトップレベルの `TestXxx(t *testing.T)` 関数リストを取得
  * `-s --scan-packages` 指定時はカレントディレクトリ配下の全パッケージ (`./...`) が対象
  * `-x --exclude PATTERN` で除外パッケージ指定が可能
* 過去の実行結果は `-j` で指定したディレクトリ配下のJSONL(`go test -json`)を再帰的に読み込む
  * 過去結果にないテストは既知の実行時間の中央値 (または `--default-duration`) として分散
* 分割結果は決定的で、同じパッケージ・過去結果・`--seed` なら常に同じスクリプトを生成
* テストバイナリは自動で事前ビルドされ、`./test-bin` に出力される (`-p`オプションで変更可能)
  * `-b` オプションで並列ビルド数を指定可能
  * `-d` オプション指定時はビルドをしないので、別途事前にビルドしておく必要がある `./test-bin` ディレクトリに `foo.bar.test` のように配置
* テストバイナリは 1 回の `go test -c` でまとめてビルド (バイナリのベース名が衝突する場合のみ分割)
* 生成対象外になったノードのスクリプト (例: `-n` を 8 から 4 に減らしたときの `test-node-7.sh`) は削除
* ログは標準エラー出力に `key=value` 形式で出力。`--dry-run` と `--plan -` は標準出力に出力
* 組み込みテンプレート: `internal/templates/test-node.sh.tmpl`
  * 対象パッケージのテストバイナリは事前ビルド済み（`go test` ではなく）を利用する、テスト実行時はパッケージディレクトリに移動
  * パッケージ単位でコマンドを分割 例: `./test-bin/foo.bar.test -test.v -test.timeout=20m -test.run "^TestFooBar|TestHogeMoge$"`
    * 同一パッケージが複数ノードで実行される場合もあるが、`-test.run` で関数単位で実行するため重複実行は回避
    * 一つのプロセスで実行するテスト関数の数を制限可能 (`-m`)
  * ノード内並列実行には `xargs -0 -P` を利用
  * テストが失敗しても各ノードの JSONL は `test-[NODE INDEX].jsonl` にマージされ、スクリプトは失敗ステータスで終了
  * テストは `gotestsum` 経由で実行し、JSONL出力レポートは `./test-json/test-[NODE INDEX]-[EXECUTE NUMBER].jsonl` 形式で出力
    * `./test-json` は `-j` で指定したディレクトリ
  * `-t` オプションで独自テンプレートも利用可能

### テンプレート変数

| 変数 | 型 | 説明 |
|------|----|------|
| `{{.NodeIndex}}` | int | ノード番号 (0 始まり) |
| `{{.Concurrency}}` | int | `-c` の値 |
| `{{.TestLines}}` | []TestLine | テストプロセスの起動単位。`.Index` (1 始まり), `.Package` (カレントディレクトリからの相対ディレクトリ), `.TestPattern` (`^(TestA\|TestB)$`) を持つ |
| `{{.JSONDir}}` | string | `-j` の絶対パス |
| `{{.BinariesDir}}` | string | `-p` の絶対パス |
| `{{.Flags}}` | string | テストフラグをスペースで連結した文字列 (クォートなし) |
| `{{.TestFlags}}` | []string | テストフラグ |

`shquote` 関数で文字列をシェル用にクォートできます。例: `{{range .TestFlags}}{{shquote .}} {{end}}`
* テストスクリプトは `./test-scripts/test-node-$NODE_INDEX.sh` のように出力されるので、CI などでは NODE_INDEX ごとに分散して実行する

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
          name: Saving test result
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
          name: Saving Test Result JSON
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

## 開発

ツールは [mise](https://mise.jdx.dev/) で管理しています (`mise install`)。

```bash
go test ./...        # E2E テストには gotestsum が必要
golangci-lint run
```

### リリース

`v*` タグを push すると、[Release ワークフロー](.github/workflows/release.yml) が GoReleaser でバイナリを公開します。

```bash
git tag v0.x.y
git push origin v0.x.y
```
