# devcontainer-builder 設計書

## 1. 目的と範囲

**目的**: 同じ Dockerfile で、devcontainer.json の `build.args` を反映したイメージを作る。ファイル配置、パッケージ導入、スクリプト実行を CLI に任せ、Dockerfile の記述を最小にする。

**前提**

- CI で事前ビルドして GHCR に配信する。層は base → python → project → user のように、作成済みのイメージを次のベースにする。
- 層はモノレポにも別リポジトリにも置かれる。層の間の接点は、GHCR のイメージ（digest 固定）だけ。
- CLI は Dockerfile の `RUN` の中で、ビルド時だけ動く。最終イメージには残らない。

**やらないこと**

- features の解釈（無視して警告する）。
- ホスト側の処理（Dockerfile の取得などは devcontainer-bootstrap の担当）。
- ビルドの実行（`docker build` や VS Code が行う）。
- 補助コマンド（`ensure-user`、`fetch` など）。スクリプトはベースイメージに合わせて自由に書く。
- `manifest.json`、`customizations` への独自キー。
- 共通の `target/`（共有は層で行う）。
- XML のマージ。

## 2. 構成

```
main.go                      薄いエントリポイント (引数、終了コード)
internal/sysinfo/            DCB_* の判定 (OS、パッケージマネージャ、ユーザー、LANG)
internal/config/             devcontainer.json の探索、build.args の分類と検証
internal/layout/             target/ の走査と規約の検査
internal/apply/              apply の流れ
(予定) internal/pkgmgr/      パッケージ導入
(予定) internal/place/       ファイル配置 (拡張子、権限、ホーム作成)
(予定) internal/merge/       JSON / TOML / YAML のマージ
(予定) internal/script/      スクリプト実行
Dockerfile                   apply イメージ (FROM scratch に /apply)
Dockerfile.devcontainer      利用側へコピーする正本
docs/
.github/workflows/           (予定)
```

- ライブラリとしては公開しない。必要になったら `manifest` や `plan` に当たる部分だけを公開する。
- parser（devcontainer-parser）は変更しない。`Config.Build.Args` が `map[string]string` で既にある。
- 設定ファイルは `os.Root` で読み、`parser.Parse` にバイト列を渡す（`LoadFromDir` は使わない）。

**利用側（層ごと）**

```
Dockerfile.devcontainer              正本のコピー。monorepo ではルートに 1 つ
<層>/.devcontainer/devcontainer.json
<層>/.devcontainer/target/           任意
```

**配布**: apply イメージを GHCR へ（マルチアーキ、cosign 署名）。利用側は digest で固定する。

## 3. 共有 Dockerfile（Dockerfile.devcontainer）

```dockerfile
# syntax=docker/dockerfile:1
ARG BUILD_BASE_IMAGE
ARG BUILD_APPLY_IMAGE=ghcr.io/imachange/devcontainer-builder/apply@sha256:...

FROM ${BUILD_APPLY_IMAGE:?} AS apply
FROM ${BUILD_BASE_IMAGE:?} AS runner
ARG BUILD_USER
USER root
RUN --mount=type=bind,from=apply,source=/apply,target=/tmp/apply \
    --mount=type=bind,target=/tmp/ctx \
    ["/tmp/apply", "apply", "/tmp/ctx"]
USER ${BUILD_USER:-root}
```

- `RUN` は 1 本。`--network=none` は使わない（パッケージ導入とスクリプトに通信が必要）。apply 自身は通信しない。
- `ENV` は書かない。ベースの `ENV`（`LANG` など）は維持する。
- `USER` は root で実行し、`BUILD_USER`（既定 root）へ戻す。`RUN --user` は存在しない（実機で確認済み）。
- `BUILD_APPLY_IMAGE` は ARG の既定値にして、テスト時に差し替える。
- `build.context` は省略し、既定のコンテキスト（devcontainer.json のあるディレクトリ）のままにする。devcontainer.json から `../Dockerfile.devcontainer` のように相対パスで参照する。
- **確認済み**: `${VAR:?}` は `USER` の中でも効く。`USER` と `LANG` は維持される。
- **未確認**: `${VAR:-root}` の `USER` での挙動。

## 4. CLI 仕様（契約）

| コマンド | 内容 |
|---|---|
| `apply [flags] <ctx>` | `RUN` の中で動く本体 |
| `detect` | `DCB_*` を `KEY=VALUE` で出力する（診断用） |
| `version` | バージョンだけを 1 行で出力する |

- フラグは `apply` の `--config`、`--target`、`--check`、`-v`。フラグはコンテキストより前に置く。
  - 環境変数 `BUILD_CONFIG`、`BUILD_TARGET_DIR` は、Dockerfile に ARG を足した場合の既定値。
  - `--check` は検証だけを行う（root も Linux も不要）。
  - `-v` は INFO の診断も表示する。
- 終了コード: 0 成功 / 1 失敗 / 2 使い方の誤り。何もすることが無ければ `nothing to do` と出して 0。
- 実行条件（`--check` なし）: Linux、root、静的バイナリ（`CGO_ENABLED=0`）。
- 互換性: マイナーバージョンでは、引数、`BUILD_*` と `DCB_*` の名前、target の規約を変えない。
- 設定の探索順: `--config`、`<ctx>/devcontainer.json`、`<ctx>/.devcontainer/devcontainer.json`、`<ctx>/.devcontainer.json`。複数あればエラー。`--config` と `--target` は、コンテキストからの相対パス。
- target は devcontainer.json の隣の `target/`。

## 5. 入力（devcontainer.json の build.args）

標準キーだけを使う。

| キー | 意味 |
|---|---|
| `BUILD_BASE_IMAGE` | Dockerfile の ARG。ベースイメージ（`tag@sha256:...`）。apply は読まない |
| `BUILD_APPLY_IMAGE` | Dockerfile の ARG。apply イメージ。apply は読まない |
| `BUILD_USER` | Dockerfile の ARG。ベースの USER の写し。既定 root。非 root のベースのときだけ書く |
| `BUILD_OVERWRITE` | 既存ファイルの上書き。既定 true。上書きはログに出す |
| `BUILD_SCRIPT_TIMEOUT` | スクリプト 1 つあたりの timeout。既定 10m |
| `BUILD_PACKAGES`、`BUILD_PACKAGES_{APT,DNF,APK,ZYPPER}` | 追加パッケージ（7 章） |

- `BUILD_` 始まりは制御用。未知のキーは警告して無視する。`BUILD_LANG` は廃止（あれば警告）。
- `DCB_` 始まりは予約。あればエラー。
- `BUILD_` 以外の `build.args` は、テンプレート変数（`{{ VAR }}`）とスクリプトの環境変数になる。
- 値は文字列のみ（数値は parser が `ParseError` にする）。
- apply が読む値に `${...}` があればエラー（dev container tools が置換する記法で、apply は生ファイルを読むため）。`BUILD_BASE_IMAGE` と `BUILD_APPLY_IMAGE` は apply が読まないので対象外。
- `--build-arg` で上書きして apply に届くのは、Dockerfile が宣言する ARG だけ。
- `features` と `overrideFeatureInstallOrder` は無視して WARN。`dockerComposeFile` は無視して WARN。
- features は使わない規約とし、ユーザーとツールはベースイメージか `pre.d/` で用意する。
- parser の INFO（使わないキー）は `-v` のときだけ表示する。

## 6. target の構成と実行順

```
target/
├── pre.d/        1. パッケージ導入の前
├── packages/     2. default.txt apt.txt dnf.txt apk.txt zypper.txt
├── files/
│   ├── root/     3. / に重ねる (root:root)
│   └── home/     3. 開発ユーザーのホームに重ねる
└── post.d/       4. 配置のあと
```

- 実行順は pre → パッケージ → ファイル配置 → post。書き込みの前に、検出できるエラーはすべて検証する。
- target が無くても動く。既定の場所が無ければ空として INFO。`--target` で明示して無ければエラー。
- 直下に許可する名前は上の 4 つだけ。`image/` などの旧名や未知の名前は、移行先を示して失敗する（`.` で始まる名前は無視）。
- `pre.d` は `packages` で入れるものを使えない。リポジトリ追加や、`files/home` のためのユーザー作成など、導入より前に必要なものだけに使う。

## 7. パッケージ

- 指定は `packages/*.txt`（1 行 1 パッケージ、`#` はコメント）と、`build.args` の `BUILD_PACKAGES*`（空白かカンマ区切り）。形式は `name` または `name@version`。版には `{{ VAR }}` が使える。
- 優先順位（後ろが勝つ）: `default.txt`、系統別 txt、`BUILD_PACKAGES`、`BUILD_PACKAGES_<系統>`。
  - 同名は後勝ち。導入順は最初に現れた順。版の差し替えはログに出す。
  - 版指定のない項目は、版を変えない（要確認）。
- dnf 系は dnf、microdnf、yum のすべてに適用する。検出した系統に合わない指定は使わず INFO。未知の `BUILD_PACKAGES_*` はエラー。
- 検出はコマンドの有無で行う。`PATH` の絶対パスと標準ディレクトリで、実行可能な通常ファイルを探す。
  - 順序は apt-get、dnf、microdnf、yum、apk、zypper。
  - 複数あれば os-release の系統に合うものを選び（無ければ先頭）、WARN。
  - 見つからず指定があれば、探した場所を列挙して失敗する。
- 実行はシェルを経由しない。失敗は 3 回まで再試行し、導入後にキャッシュを削除する。版を下げる指定は自動では行わず、明示的なエラーにする。

## 8. ファイル配置

- `files/root/<path>` は `/<path>` へ root:root で、`files/home/<path>` は開発ユーザーのホームへ、そのユーザーと主グループで置く。
- 開発ユーザーは remoteUser → containerUser → BUILD_USER → root の順（仕様の既定の連鎖）。
  - `files/home` があるときだけ解決し、存在しなければ失敗する（作成は `pre.d` かベース）。
  - どれも指定が無ければ WARN。remoteUser が BUILD_USER と同じなら INFO。
- ホームが無ければ `useradd -m` と同じ結果で作る。
  - 権限は `/etc/login.defs` の HOME_MODE、無ければ UMASK から算出、無ければ 0755。
  - `/etc/skel` をコピーし、上位のディレクトリは root:root 0755。
  - 新規に作ったディレクトリだけ所有者を変える。

| 拡張子 | 意味 |
|---|---|
| なし | 置換 |
| `.tmpl` | `{{ VAR }}` を展開して置換。未定義はエラー。先頭のドット（`{{ .VAR }}`）も許す。他のテンプレートの記法（`{{ .Values.x }}` など）は、そのまま残す（`internal/tmpl`） |
| `.append` | 追記。配置先が無ければ新規作成 |
| `.tmpl.append` | 展開してから追記 |

- 追記は置換のあとに実行する。同じ配置先を指すものが複数あればエラー。改行で終わらない既存の末尾には改行を足す。同じ内容が連続して既にあればスキップする（冪等）。
- 配置先が `.json`、`.toml`、`.yaml`、`.yml` の `.append` は、キー単位のマージ。
  - オブジェクトは再帰的にマージし、それ以外と配列は入れ替える。キーの削除はできない。
  - 既存の構文エラーと JSONC は失敗する。コメントと整形は失われ、キーは辞書順になる。
  - 新規作成のときは、パッチの内容をそのまま書く。変更はログに出す。
  - ライブラリは `github.com/pelletier/go-toml/v2` と、現在は `gopkg.in/yaml.v3`（`go.yaml.in/yaml/v4` への移行は、リリース状況と API を確認してから）。
  - YAML の複数ドキュメントは、書き戻しで黙って失われるので、エラーにする。JSONC は、コメントを除いて受け付け、書き戻しでコメントは失われる。
- 権限: 規則に当たらないものは、ディレクトリ 0755、ファイルは実行ビットがあれば 0755、無ければ 0644。
  - 組み込みの規則: `.ssh`（0700 と 0600、`*.pub` は 0644）、`.gnupg`（0700 と 0600）、`.netrc`・`.pgpass`・`.git-credentials`（0600）、`/etc/sudoers.d/*`（0440）。
  - 変えたいときは、ファイルは `post.d`、ディレクトリは `pre.d` で先に作る。
- `sudoers.d` は許可する。名前に `.` を含むか `~` で終わるものは拒否し、visudo があれば `visudo -cf` で検証する。
- 安全性:
  - 入力は `os.Root` に閉じ込める。
  - symlink、デバイス、FIFO、setuid 系は拒否する。
  - 配置先の親が symlink を経由しないことを確認する。
  - 組み込みの deny（`/proc`、`/sys`、`/dev`、`/etc/passwd`、`/etc/shadow`、`/etc/group`、`/etc/gshadow`、`/etc/sudoers`、`/etc/ld.so.preload` など）。
  - 置換は一時ファイルから rename。追記とマージは同じファイルへ書く。
  - ファイル数とサイズに上限を設ける。
- 既存ファイルは上書きを許可する（`BUILD_OVERWRITE=false` でエラー）。

## 9. スクリプトと DCB_*

- `pre.d/` と `post.d/` の通常ファイルを名前順に実行する。`.` と `_` で始まる名前は部品用で、実行しない。
- 先頭に shebang が必須。実行権限は不要。一時ディレクトリにコピーして実行し、終了時に削除する。シェルを経由せず、root で動く（別ユーザーはスクリプト内で `runuser` などを使う）。
- 環境変数は許可したものだけ。
  - `PATH` はベースから引き継ぐ（無ければ標準値）。
  - `HOME`、`USER`、`LOGNAME`、`DEBIAN_FRONTEND=noninteractive`。
  - `BUILD_` 以外の `build.args`、`DCB_*`。
  - `PATH`、`LD_PRELOAD`、`LD_LIBRARY_PATH`、`IFS`、`BASH_ENV`、`ENV`、`SHELLOPTS` は上書き不可。
- timeout は 10 分（`BUILD_SCRIPT_TIMEOUT`）。超えるとプロセスグループごと kill する。1 つでも失敗すればビルドは失敗。
- 補助コマンドは提供しない。[docs/recipes/](recipes/README.md) に、ユーザー作成、タイムゾーンと日本語ロケール、sudo、チェックサム付きダウンロードのスクリプトを置く。CI で、各ディストリビューション上で、実際に実行して検証する。
- ディレクトリ全体（`_lib.sh` などの部品を含む）を、一時ディレクトリに複製して実行するので、`. "$(dirname "$0")/_lib.sh"` で部品を読み込める。
- プロキシ（`http_proxy` など）と CA（`SSL_CERT_FILE` など）の環境変数は、ベースから引き継ぐ。それ以外のベースの環境変数は、引き継がない。

| 変数 | 内容 |
|---|---|
| `DCB_PHASE` | pre または post |
| `DCB_OS_ID` | os-release の ID（`usr/lib` や古い `*-release` も見る） |
| `DCB_OS_FAMILY` | 選んだパッケージマネージャの系統（apt、dnf、apk、zypper）。無ければ ID と ID_LIKE から |
| `DCB_PKGMGR` | 7 章の検出結果 |
| `DCB_ARCH` | `runtime.GOARCH` |
| `DCB_USER`、`DCB_HOME` | 開発ユーザーとホーム（ユーザーが未作成ならホームは空） |
| `DCB_LANG` | RUN 時点の `LANG`（ベースのもの。無ければ空） |

- 8 個すべてを必ず設定し、判定できなければ空にする。各スクリプトの直前に判定し直す。
- 判定は `fs.FS` を受ける形にして、`fstest` でテストする。
- `LANG` は apply では設定できない。ベースの `ENV` か利用者の `containerEnv` で決め、ロケール生成は `post.d` で行う。

## 10. 層構成での運用

| 機能 | 適用済みイメージの上での 2 回目の挙動 |
|---|---|
| 追記 | 同じ内容があればスキップ |
| 置換 | 上書き（既定） |
| マージ | 同じパッチなら結果は同じ |
| パッケージ | 導入済みは何もしない。版を上げるのは動く。下げるのは失敗しうるので、下の層に入れない設計にする |
| スクリプト | 毎回実行される。冪等に書く |
| USER | `BUILD_USER` を層ごとに渡す |

- 各層が自分の devcontainer.json と target を持つ。変わりにくいもの（パッケージ）を下の層に置くと、キャッシュが効く。
- `BUILD_BASE_IMAGE` は `tag@sha256` で書き、Renovate か CI のコミットで更新する。VS Code でビルドする経路では build.args は固定値なので、`BUILD_USER` も手で書く。
- `BUILD_USER` と `remoteUser` は別。ベースが root で開発ユーザーが `vscode` なら、`remoteUser` だけを書く。ベースが `node` で開発ユーザーも `node` なら、`BUILD_USER` だけを書く。
- CI で、ベースと結果の `USER` が一致することを検査する（空は root とみなす）。
- 正本のコピーは Renovate で `BUILD_APPLY_IMAGE` を更新する。正本の中身を変えるのはメジャーバージョン。

## 11. 割り切り

- スクリプトは Dockerfile の `RUN` と同じ信頼レベル。シークレットは `build.args` に入れず `--secret` を使う。`build.args` の値はイメージの履歴に残る。
- ファイル単位の owner や mode、スクリプトごとの user は指定できない。
- マージでコメントと整形が失われる。
- `RUN` が 1 本なので、コンテキストが変わるとパッケージ導入ごと再実行される。
- BuildKit が必須（旧ビルダーでは `--mount` が使えない）。
- 実行時にホームがバインドマウントで置き換わると、`files/home` は隠れる。

## 12. テスト

- 単体: `fstest.MapFS` と一時ディレクトリ（`os.Root`）。`sysinfo` は、各ディストリビューション、複数マネージャ、os-release なし、distroless 相当を表形式でテストする。
- `main` の統合テスト: `--check` で、探索、診断、規約違反、`nothing to do` を通しで確かめる。
- GitHub Actions のマトリクス（予定）: debian:12-slim、ubuntu:24.04、alpine、fedora、UBI minimal、opensuse/leap。
  - 正本の `Dockerfile.devcontainer` を `docker build` で直接ビルドする（`BUILD_APPLY_IMAGE` に `apply:ci`）。
  - buildx の docker-container ドライバは、ローカルの `apply:ci` を `FROM` で参照できない。`docker build` か `driver: docker` を使う。
  - `verify.sh` で権限、所有者、内容、パッケージを検査する。
  - Docker Hub の匿名プル制限に注意する。arm64 は `ubuntu-24.04-arm` を候補とする。
- `detect` で各ディストリビューションの判定を確認し、`USER` 一致の検査も CI に入れる。

## 13. 未確認と未決

**未確認（実機で確認する）**

- `RUN` の `--mount` のマウント先が、空ファイルとしてレイヤーに残るか。
- `${BUILD_USER:-root}` の `USER` での挙動。
- `build.context` 省略時のコンテキストと、コンテキスト外の Dockerfile を VS Code と devcontainer CLI が渡せるか。
- 各ディストリビューションのパッケージマネージャのフラグ。
- ホーム作成（Ubuntu の `adduser` の `DIR_MODE`、Alpine の busybox）。
- `sudoers` の `includedir`（Alpine と openSUSE の初期状態）。
- `updateRemoteUserUID` による UID の付け替え。
- Renovate が `Dockerfile.devcontainer` を認識するか。
- `go.yaml.in/yaml/v4` のリリース状況と API。

**未決**

- 版指定のない項目の扱い（7 章の提案でよいか）。

**決着済み**

- XML のマージは行わない。
- YAML は `go.yaml.in/yaml/v4`、TOML は `go-toml/v2`。
- 層はモノレポと別リポジトリの両方を想定し、接点は GHCR のイメージだけにする。
- `LoadFromDir` の `FilePath` の形式は、`Parse` を使うので問題にならない。

## 14. 実装状況

| 内容 | 状況 |
|---|---|
| `sysinfo`（DCB_* の判定） | 実装済み |
| `config`（探索、build.args の分類と検証、診断） | 実装済み |
| `layout`（target の走査と規約の検査） | 実装済み |
| `tmpl`（`{{ VAR }}` の展開。files と packages で共通） | 実装済み |
| `pkgmgr`（検出、優先順位の結合、書式の検証、導入、キャッシュ削除） | 実装済み |
| `place`（files/root、files/home、拡張子、権限、ホーム作成、安全性） | 実装済み |
| `merge`（JSON、TOML、YAML） | 実装済み |
| `script`（部品の複製、環境、timeout、キャンセル） | 実装済み |
| `apply`（事前検証、実行、配置の直前の再判定）、`main`、`detect` | 実装済み |
| docs/recipes | 実装済み（issue #14） |
| CI（ビルドとテスト）、マルチディストリビューション、リリース | 実装済み（issue #8、#15） |

issue #13（パイプライン統合と、実際の実行フェーズ）は、`apply` の、事前検証 → pre → パッケージ → ファイル配置 → post の流れと、`internal/apply` のテスト、`integration.yml` の、実環境での検証で、対応している。
