# 説明

[![CI](https://github.com/takuo/go-testsplitter/actions/workflows/ci.yml/badge.svg)](https://github.com/takuo/go-testsplitter/actions/workflows/ci.yml)

過去のテスト実行時間に基づいて、多数のテストを複数ノードに分散して実行するスクリプトを出力します。
また、スクリプトで実行するテストバイナリを内部で事前ビルドします。

### 前提

多数のパッケージにテストが分散し、`go test ./...` の実時間が問題になる規模の大規模モノレポを対象としたツールです。分割はテストの実行順序とまとまりを崩すため、**各テスト関数が互いに独立していること**を前提とします。

* パッケージのテストはノードとプロセスに分割される。あるテストが別のテストの実行済みであることや、同じプロセスで動くことに依存できない。実行ごとにパッケージへ割り当てられるテストの組み合わせは変わる (各テストが 1 回だけ実行されることは保証される)
* プロセス内のテストは `-test.run "^(TestA|TestB)$"` の順で実行され、プロセスは全ノードで同時に並列実行される (`-c`)。そのため外部リソース (データベース、固定ポート、パッケージ外のファイルなど) を共有するテストは衝突しうる
* `TestMain` はパッケージごとではなくプロセスごとに 1 回実行されるため、複数プロセスに分割されたパッケージでは setup / teardown が複数回走る (これが splitter が推定し、分割時に加算しているパッケージのオーバーヘッド)
* **サブテストは分割されない**: トップレベルのテストの `t.Run` サブテストは、常に同じプロセスで、宣言順にまとめて実行される。順序に依存するテストや状態を共有するテストは、1 つのトップレベルのテスト関数のサブテストにまとめる
* 独立にできないテストは `-x --exclude` で除外し、別ジョブで実行する

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
# origin/main とのマージベースからの変更の影響を受けるテストだけを実行 (PR など)
testsplitter -s -n 4 --changed-since origin/main -- -test.timeout=20m
# 今回の結果を次回の分割のための過去結果として保存 (CI で全ノードの実行後)
testsplitter archive
```

`split` がデフォルトのコマンドなので、`testsplitter -s -n 4` は `testsplitter split -s -n 4` と同じです。
もう一つのコマンドは [`testsplitter archive`](#過去結果のアーカイブ) です。

### インストール

```bash
go install github.com/takuo/go-testsplitter/cmd/testsplitter@latest
```

ソースからのビルドには Go 1.26 以降が必要です。ツールチェーン不要の Linux / macOS 向けビルド済みバイナリは [Releases](https://github.com/takuo/go-testsplitter/releases) から取得できます。

## オプション

`split` (デフォルト) コマンドのオプションです。`testsplitter archive` は [過去結果のアーカイブ](#過去結果のアーカイブ) を参照してください。

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
  | --rerun-fails=INT            | 0 (再実行しない)     | 生成スクリプトで失敗したテストを最大この回数まで再実行 (`gotestsum --rerun-fails`) | {{ .RerunFails }} |
  | --changed-since=REV          | (なし)               | REV と HEAD のマージベース以降の変更の影響を受けるパッケージのテストだけを実行 |              |
  | --granularity=GRANULARITY    | package              | `--changed-since` 指定時の選択単位。影響を受けるパッケージ (`package`) または影響を受けるトップレベルの宣言 (`symbol`) |  |
  | --run-all-on=REGEX           | `(^\|/)go[.](mod\|sum\|work)$` | `--changed-since` 指定時、変更ファイルのパス (リポジトリルートからの相対パス) が一致したら全テストを実行。`''` で無効化 |  |
  | --max-age=DURATION           | 0 (無制限)           | この期間より古い過去結果を無視 (例: `720h`)                           |                          |
  | -p, --binaries-dir=DIR       | ./test-bin           | テストバイナリの出力/事前ビルド先                                   | {{ .BinariesDir }}       |
  | -b, --build-concurrency=INT  | 4                    | 並列にビルドするパッケージ数 (`go test -p`)                          |                          |
  | -d, --disable-build          | (ビルド有効)         | テストバイナリをビルドせず、事前ビルド済みを利用                     |                          |
  | --dry-run                    | (無効)               | テストバイナリのビルドやスクリプト出力をせず、分割結果を表示         |                          |
  | --plan=FILE                  | (なし)               | 分割結果を JSON で FILE に出力 (`-` で標準出力)                      |                          |
  | -q, --quiet                  | (無効)               | 警告とエラーのみ出力                                                 |                          |
  | --verbose                    | (無効)               | デバッグログを出力                                                   |                          |
  | -- ...                       | (なし)               | テストバイナリに渡す追加引数 (例: -test.v -test.timeout=20m)         | {{ .Flags }}, {{ .TestFlags }} |

### 概要

* **パッケージ**: 標準入力（`go list ./...` の出力）からテストパッケージリストを受け取る
  * 各行はインポートパス (`github.com/foo/bar/api`) またはカレントディレクトリからの相対ディレクトリ (`api`, `./api`)
  * パッケージは `go list` で解決し、テストファイルを AST で解析 (ビルド制約を考慮) して `go test` が実行するトップレベルの関数リストを取得: `TestXxx(t *testing.T)`、`FuzzXxx(f *testing.F)` (シードコーパスをテストとして実行)、出力コメント (`// Output:`) を持つ `ExampleXxx()`
  * ベンチマーク、出力コメントのない Example、`TestMain` は `go test` が実行しないため対象外
  * `-s --scan-packages` 指定時はカレントディレクトリ配下の全パッケージ (`./...`) が対象
  * `-x --exclude PATTERN` で除外パッケージ指定が可能
* **過去の実行結果**: `-j` で指定したディレクトリ配下の JSONL (パッケージ名付きの `go test -json` 出力。例: `test2json -p "pkgname"`) を再帰的に読み込む
  * 同じテストが複数ファイルにある場合はイベント時刻が最も新しい結果を採用。`--max-age` より古い結果は無視
  * 過去結果にないテストは既知の実行時間の中央値 (または `--default-duration`) として扱う
  * パッケージごとのプロセスのオーバーヘッド (パッケージの実行時間からテストの合計を引いたもの。プロセス起動や `TestMain` など) を推定
* **分割**: テスト関数単位で、最も遅いノードの時間が最小になるように分割
  * パッケージのオーバーヘッドはそのパッケージを実行するノードごとに 1 回加算されるため、オーバーヘッドの大きいパッケージは同じノードにまとまりやすい
  * `-m` 指定時は、パッケージ内の関数を実行時間が均等になるようにプロセスへ振り分け
  * 1 ノードあたりの理想時間より長いテストは、均等化の妨げになるため警告を出力
  * 分割結果は決定的で、同じパッケージ・過去結果・`--seed` なら常に同じスクリプトを生成
* **ビルド**: テストバイナリは 1 回の `go test -c` でまとめて `-p` (`./test-bin`) にビルド (バイナリのベース名が衝突する場合のみ分割)
  * バイナリ名はパッケージディレクトリを `.` で連結したもの (`api/foo` → `api.foo.test`)。パス要素中の `.` と `%` はエスケープ (`a/b.c` → `a.b%2Ec.test`, `.` → `%2E.test`)
  * `test2json` も同じディレクトリにビルドするため、テスト実行ノードに Go ツールチェーンは不要 (`gotestsum` は必要)
* **スクリプト**: 組み込みテンプレート `internal/templates/test-node.sh.tmpl` (または `-t` の独自テンプレート) から `./test-scripts/test-node-[NODE INDEX].sh` を生成
  * 各プロセスはパッケージディレクトリでテストバイナリを `-test.run "^(TestFoo|TestBar)$"` 付きで実行。同じパッケージが複数ノードで実行されることもあるが、各テストは 1 回だけ実行
  * ノード内では `xargs -0 -P` で長いプロセスから順に並列実行
  * テストは `gotestsum` 経由で実行し、JSONL を `[JSON DIR]/test-[NODE INDEX]-[EXECUTE NUMBER].jsonl` に出力。テストが失敗しても `test-[NODE INDEX].jsonl` にマージし、失敗ステータスで終了
  * 失敗したテストの再実行はしない。`--rerun-fails=N` を指定すると gotestsum が失敗テストを最大 N 回再実行し、`test-reports/rerun-[NODE INDEX]-[EXECUTE NUMBER].txt` にレポートを出力する。ただし Flaky なテストを隠してしまうこと、過去結果には最後の試行の実行時間が残ることに注意
  * 生成対象外になったノードのスクリプト (例: `-n` を 8 から 4 に減らしたときの `test-node-7.sh`) は削除
* ログは標準エラー出力に `key=value` 形式で出力。`--dry-run` と `--plan -` は標準出力に出力
* テストスクリプトは CI などで NODE_INDEX ごとに分散して実行する

### 変更の影響を受けるテストだけを実行

`--changed-since REV` を指定すると、`REV` と `HEAD` のマージベース以降の変更の影響を受けるパッケージのテストだけをスクリプトに分割し、そのテストバイナリだけをビルドします。

* 変更ファイルはマージベース以降のコミット済み・未コミットの変更 (削除、リネーム、未追跡ファイルを含む。`git diff --name-only --no-renames` と `git ls-files --others`)
* ファイルは、パッケージディレクトリ直下、その `testdata` 配下、または `//go:embed` で埋め込まれている場合にそのパッケージに属する
* テストバイナリが変更パッケージに依存している (テストでのみ import する場合を含む) パッケージを選択 (`go list -deps -test`)
* 変更ファイルが `--run-all-on` (デフォルトは `go.mod`, `go.sum`, `go.work`) に一致した場合は全パッケージを選択
* import グラフに現れない依存 (パッケージ外のファイルを相対パスで読む、外部サービスなど) は検出できないため、main ブランチでは全テストの実行を推奨
* `--dry-run` と `--plan` で、どのパッケージがなぜ選択されたかを確認可能
* ベースのリビジョンを fetch しておく必要あり。マージベースが見つからない浅いクローンでは、警告を出して `REV` と直接比較する。分岐後に `REV` 側で入った変更も含まれるため、余分なテストが選択されることがある。正確な差分を得るには、マージベースのコミット (GitHub の compare API の `merge_base_commit.sha` など) を `git fetch --depth=1 origin <SHA>` で取得して `REV` に渡す

#### `--granularity symbol`

デフォルトでは影響を受けるパッケージのテストをすべて選択するため、ほぼ全パッケージから import されるパッケージに定数を 1 つ追加しただけで、ほぼ全テストが選択されます。`--granularity symbol` を指定すると、トップレベルの宣言 (関数、メソッド、型、定数、変数) 単位でテストを選択します。

* 変更ファイルの宣言をマージベース時点と現在で比較して、変更された宣言を検出 (コメントと整形の差は無視。ファイル間の宣言の移動は変更とみなさない)
* 変更された宣言を推移的に参照するテスト関数を選択 (`golang.org/x/tools/go/packages` で型チェック)。例えば定数の追加ではテストは選択されず、定数の変更ではそれを使うコードに到達するテストだけが選択される
* exported メソッドの変更は、他パッケージのインターフェースやリフレクション経由で呼ばれうるため、レシーバの型の変更として扱う。unexported メソッドの変更は同じパッケージのインターフェース経由の呼び出しに影響
* `TestMain`、またはコマンドの `main` 関数 (コマンドのテストはビルドしたコマンドを実行することが多い) が変更に依存する場合は、そのパッケージのテストをすべて選択
* 参照以外でパッケージに影響しうる変更は、そのパッケージをリンクするテストバイナリのテストをすべて選択: `init` 関数、blank 変数 (`var _ = ...`)、blank/dot import、ビルド制約、cgo、`//go:linkname`、Go 以外のファイル (`testdata`、埋め込みファイル)、変更された宣言に依存する初期化処理
* 参照を通じた依存のみ検出するため、`go build` した別のコマンドを実行するテストや、リフレクションでメソッド名を指定して呼ぶテストは選択漏れの可能性あり
* 解析に失敗した場合 (型エラーなど) はパッケージ単位で選択

## 過去結果のアーカイブ

分割には、直近の実行だけでなく **すべての** テストの過去結果が必要です。一部のテストだけを実行した場合 (`--changed-since` など) に過去結果を置き換えてしまうと、他のテストの実行時間が失われます。`testsplitter archive` は今回の結果をアーカイブ済みの結果に追加し、アーカイブを小さく保つため、CI では 1 つのディレクトリを復元・保存するだけで済みます。

```bash
# CI で全ノードの結果を ./test-json に集めたあと
testsplitter archive --id "$CI_RUN_ID" --keep 20
```

  | オプション               | デフォルト              | 説明                                                                 |
  |--------------------------|-------------------------|----------------------------------------------------------------------|
  | FILE ...                 | JSON_DIR/test-*.jsonl   | アーカイブする結果ファイル                                            |
  | -j, --json-dir=DIR       | ./test-json             | 今回の結果とアーカイブ済みの実行を含むディレクトリ                      |
  | --runs-dir=DIR           | JSON_DIR/runs           | 実行ごとのディレクトリを作る場所                                      |
  | --id=NAME                | (現在の UTC 時刻)       | 今回の実行のディレクトリ名 (CI のワークフロー ID など)                 |
  | --keep=INT               | 20                      | 保持するアーカイブ済み実行の数 (新しい順。0 で無制限)                  |
  | --max-age=DURATION       | 0 (無制限)              | この期間より古いアーカイブ済み実行を削除 (例: `720h`)                  |
  | --keep-output            | (無効)                  | テスト出力を含むすべてのイベントをアーカイブ                           |
  | --keep-source            | (無効)                  | 移動せず JSON_DIR のファイルを残す                                    |
  | --dry-run                | (無効)                  | 何も書き込まず、アーカイブ・削除の対象を表示                           |
  | -q, --quiet              | (無効)                  | 警告とエラーのみ出力                                                  |
  | --verbose                | (無効)                  | デバッグログを出力                                                    |

* 今回の結果はファイル名のまま `JSON_DIR/runs/[ID]/` へ移動する。`-j` は再帰的に読まれ、テストごとに最新の結果が使われるため、次回の分割では過去のアーカイブと合わせて利用される
* アーカイブするのは分割に必要なイベントだけ: パッケージとトップレベルのテストの結果 (`pass`, `fail`, `skip`)。`go test -json` の大部分を占めるテスト出力とサブテストは破棄する。アーカイブも `go test -json` 出力のままなので他のツールでも読める (`--keep-output` ですべて保存)
* ディレクトリの時刻が新しい順に `--keep` 個を残し、それを超えた実行と `--max-age` より古い実行を削除する
* 実行は CI の 1 回につき 1 度、全ノードの結果を集めたあと (テストジョブに依存し、失敗しても実行されるジョブなど)。各ノードは `test-[NODE INDEX].jsonl` を書くため、並列ノードのファイルは衝突しない
* アーカイブ前にアーカイブ済みの実行を復元し、終了後に `JSON_DIR` 全体を保存することで、過去の実行が保持される。`--id` に CI の実行 ID を渡すと実行ごとに安定した名前になる
* 明示的に指定したファイルは `test-*.jsonl` 以外でもアーカイブする。アーカイブ済み実行のファイルは対象外

### テンプレート変数

| 変数 | 型 | 説明 |
|------|----|------|
| `{{.NodeIndex}}` | int | ノード番号 (0 始まり) |
| `{{.Concurrency}}` | int | `-c` の値 |
| `{{.TestLines}}` | []TestLine | テストプロセスの起動単位 (長い順、下表参照) |
| `{{.JSONDir}}` | string | `-j` の絶対パス |
| `{{.BinariesDir}}` | string | `-p` の絶対パス |
| `{{.Flags}}` | string | テストフラグをスペースで連結した文字列 (クォートなし) |
| `{{.TestFlags}}` | []string | テストフラグ |
| `{{.RerunFails}}` | int | `--rerun-fails` の値 (再実行しない場合は 0) |

| TestLine のフィールド | 型 | 説明 |
|-----------------------|----|------|
| `.Index` | int | ノード内の通し番号 (1 始まり) |
| `.Package` | string | カレントディレクトリからの相対パッケージディレクトリ |
| `.Binary` | string | `{{.BinariesDir}}` 内のテストバイナリのファイル名 |
| `.TestPattern` | string | `-test.run` のパターン。例: `^(TestA\|TestB)$` |
| `.Functions` | []string | プロセスで実行するテスト関数 |
| `.Estimated` | time.Duration | パッケージのオーバーヘッドを含む予想実行時間 |

`shquote` 関数で文字列をシェル用にクォートできます。例: `{{range .TestFlags}}{{shquote .}} {{end}}`

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
          name: Saving test result
          paths:
            - test-json
          when: always
  save-test-result:
    resource_class: small
    docker:
      - image: cimg/go:1.27
    working_directory: /home/circleci/project
    steps:
      # 過去の結果
      - restore_cache:
          name: Restoring previous test results
          keys:
            - v1-test-results-{{ .Branch }}-
            - v1-test-results-main-
            - v1-test-results-
      # 今回の実行の結果 (test-json/test-[NODE INDEX].jsonl)
      - attach_workspace:
          at: /home/circleci/project
      - run:
          name: Archiving test results of this run
          command: |
            go install github.com/takuo/go-testsplitter/cmd/testsplitter@latest
            testsplitter archive --id "$CIRCLE_WORKFLOW_ID" --keep 20
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

* 過去のテスト結果は、`testsplitter` が読む `build` ジョブでのみ復元する。`test` ジョブは空の `test-json` に自分のノードの結果 (`test-[NODE INDEX].jsonl`) だけを書いて保存するため、並列ノード間で workspace のファイルが衝突しない
* `save-test-result` ジョブは、今回の結果を過去の結果に置き換えるのではなく `test-json/runs/` 以下に追加して保存する。置き換えると、一部のテストだけを実行した場合 (`--changed-since` など) に他のテストの結果が失われる。[過去結果のアーカイブ](#過去結果のアーカイブ) を参照
* 同じリビジョンのワークフローを再実行しても新しいキャッシュが保存されるよう、キャッシュキーに `{{ epoch }}` を含める。新しいブランチは `main` の結果を引き継ぐ
* `when: always` で、テストが失敗してもレポートと結果を保存する
* `cimg/go` イメージは `go install testsplitter` のためだけに使っている。[ビルド済みバイナリ](https://github.com/takuo/go-testsplitter/releases) を使えば軽量イメージでよい
* 保持する実行回数は `--keep` で調整する。古い結果は `archive --max-age` で削除、`split --max-age` で無視できる

### PR では影響を受けるテストだけを実行する (CircleCI)

上記の `Building test binaries` ステップを以下に置き換えると、`main` と `release/*` では全テストを、それ以外のブランチでは変更の影響を受けるテストだけを実行します。CircleCI は PR のマージ先ブランチを提供せず、マージベースは浅いクローンに含まれないため、どちらも GitHub API から取得します。

```yaml
      - run:
          name: Building test binaries
          command: |
            export GOGC=off CGO_ENABLED=0
            go install github.com/takuo/go-testsplitter/cmd/testsplitter@latest

            SELECT=()
            case "$CIRCLE_BRANCH" in
              main|release/*)
                ;; # 全テストを実行
              *)
                # PR のマージ先ブランチを取得 (失敗したら空のままにして全テストを実行)
                BASE=""
                if [ -n "${CIRCLE_PULL_REQUEST:-}" ]; then
                  BASE=$(curl -fsSL \
                    ${GITHUB_TOKEN:+-H "Authorization: Bearer $GITHUB_TOKEN"} \
                    "https://api.github.com/repos/${CIRCLE_PROJECT_USERNAME}/${CIRCLE_PROJECT_REPONAME}/pulls/${CIRCLE_PULL_REQUEST##*/}" \
                    | jq -r '.base.ref // empty') || BASE=""
                fi
                if [ -n "$BASE" ]; then
                  # PR のマージベース (浅いクローンには含まれない)
                  MERGE_BASE=$(curl -fsSL \
                    ${GITHUB_TOKEN:+-H "Authorization: Bearer $GITHUB_TOKEN"} \
                    "https://api.github.com/repos/${CIRCLE_PROJECT_USERNAME}/${CIRCLE_PROJECT_REPONAME}/compare/${BASE}...${CIRCLE_SHA1}" \
                    | jq -r '.merge_base_commit.sha // empty') || MERGE_BASE=""
                  if [ -n "$MERGE_BASE" ]; then
                    git fetch --depth=1 --no-tags origin "$MERGE_BASE"
                    SELECT=(--changed-since "$MERGE_BASE" --granularity symbol)
                  else
                    # マージ先ブランチの先端: 分岐後にマージ先ブランチで入った変更も含まれる
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

* 全テストを実行するには、`--run-all-on '.*'` ではなく `--changed-since` を付けない。`--run-all-on` は変更ファイルに対して評価されるため、変更がない場合 (例: `main` で `--changed-since origin/main`) はテストが 0 件になる
* マージ先ブランチが分からない場合 (PR のないブランチ、API の失敗など) は全テストを実行
* 比較対象は PR のマージベース (SHA を指定して `--depth=1` で取得) のため、PR 作成後にマージ先ブランチへ追加されたコミットは含まれない。積み重ねた PR では親 PR のブランチがマージ先になる
* API からマージベースを取得できない場合はマージ先ブランチの先端と比較する。分岐後にマージ先ブランチで入った変更も含まれるため余分なテストが実行されることがあるが、影響を受けるテストが漏れることはない
* プライベートリポジトリでは、リポジトリの read 権限を持つ `GITHUB_TOKEN` (CircleCI の Context など) が必要。トークンなしの API リクエストはレート制限を受ける
* `CIRCLE_PULL_REQUEST` はブランチの最初の PR (すべての PR は `CIRCLE_PULL_REQUESTS`)
* `curl` と `jq` の代わりに GitHub CLI を使う場合は以下のように取得する。`gh` は `cimg` イメージに含まれないためインストールが必要で、公開リポジトリでも `GH_TOKEN` が必要。`CIRCLE_PULL_REQUEST` が空の場合の判定と、失敗時に全テストを実行するための `|| BASE=""` は残す
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
          # テストが割り当てられたノードだけ (空のスクリプトを実行するジョブを作らない)
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
          # アーティファクトは実行ビットを保持しない
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
      # 過去の結果
      - name: Restoring previous test results
        uses: actions/cache/restore@v4
        with:
          path: test-json
          key: test-results-${{ github.ref_name }}-${{ github.run_id }}
          restore-keys: |
            test-results-${{ github.ref_name }}-
            test-results-main-
      # 今回の実行の結果 (各ノードの test-json/test-[NODE INDEX].jsonl)
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

* `test` の matrix は `--plan` の JSON から作るため、テストが割り当てられなかったノード (`--changed-since` 使用時など) ではジョブが起動しない
* 過去のテスト結果は、読み書きするジョブでのみ復元する。`test` ジョブでは復元しないため、各ノードは自分のノードの結果 (`test-[NODE INDEX].jsonl`) だけをアップロードし、アーティファクトを `merge-multiple` でまとめられる
* 保存されることのない `key` と `restore-keys` のプレフィックスを組み合わせて、最新の一致キャッシュを復元する。キャッシュは変更できないため、実行ごとに新しいキャッシュを保存し (`github.run_id`)、ブランチは `main` の結果を引き継ぐ。PR のキャッシュはその PR の実行とマージ先ブランチの実行からのみ復元できる
* `save-test-result` は `if: always()` でテストジョブに依存するため、テストが失敗しても結果をアーカイブする。[過去結果のアーカイブ](#過去結果のアーカイブ) を参照
* スクリプトには `-j` と `-p` の絶対パスが埋め込まれる。GitHub ホストランナーではどのジョブでも同じパスにチェックアウトされるため一致する
* `test2json` は `test-bin` にビルドされるため、`test` ジョブに必要なのは `gotestsum` だけ。`go install` が手軽だが、[ビルド済みバイナリ](https://github.com/gotestyourself/gotestsum/releases) なら Go ツールチェーンも不要

### PR では影響を受けるテストだけを実行する (GitHub Actions)

全履歴をチェックアウトし、上記の `Building test binaries` ステップを以下に置き換えると、`main` では全テストを、PR では変更の影響を受けるテストだけを実行します。

```yaml
      - uses: actions/checkout@v7
        with:
          fetch-depth: 0 # マージベースがクローンに含まれる必要がある
      # ... setup-go とキャッシュのステップ ...
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

* `BASE_REF` は `pull_request` イベントでのみ設定されるため、`main` では全テストを実行する。全テストを実行するには `--run-all-on '.*'` ではなく `--changed-since` を付けない。`--run-all-on` は変更ファイルに対して評価されるため、変更がない場合はテストが 0 件になる
* `pull_request` イベントでは PR のマージコミットがチェックアウトされ、その第 1 親はマージ先ブランチになる。そのため `origin/$BASE_REF` と `HEAD` のマージベースはマージ先ブランチの先端となり、PR の変更だけが比較される
* マージベースにはすべてのブランチと全履歴が必要なため `fetch-depth: 0` を指定する。大きなリポジトリでは、浅いクローンのままベースのコミットだけを取得してもよい
  ```yaml
      - uses: actions/checkout@v7
      - name: Fetching the base commit of the pull request
        if: github.event_name == 'pull_request'
        env:
          BASE_SHA: ${{ github.event.pull_request.base.sha }}
        run: git fetch --depth=1 --no-tags origin "$BASE_SHA"
  ```
  このとき split のステップでも `env` に `BASE_SHA` を渡し、`--changed-since "$BASE_SHA"` を指定する。浅いクローンにはマージベースが含まれないため `base.sha` と直接比較され、PR の最終同期後にマージ先ブランチへ push されたコミットも含まれて余分なテストが実行されることがある
* イベントの値はスクリプトに `${{ ... }}` で展開せず `env` 経由で渡し、ブランチ名によるシェルインジェクションを防ぐ
* `permissions: contents: read` で十分で、CircleCI と違いマージ先ブランチの取得にトークンは不要

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
