# devcontainer-builder

Dockerfile の `RUN` の中で、**ビルド時だけ**動く Go 製の CLI です。同じ Dockerfile を使い、`devcontainer.json` の `build.args` と `target/` ディレクトリの内容を反映したイメージを作ります。パッケージ導入、ファイル配置、スクリプト実行を CLI に任せるので、Dockerfile は十数行のままです。

CI で事前ビルドして GHCR に配信するイメージ（base → python → project → user のように、作成済みのイメージを次のベースにする構成）を想定しています。CLI は最終イメージに残りません。

> **状況**: パッケージ導入、ファイル配置、スクリプト実行（pre → パッケージ → ファイル配置 → post）まで実装されています。設計は [docs/design.md](docs/design.md)、実装の点検結果は [docs/audit.md](docs/audit.md) にあります。

## 仕組み

```
利用側                                        apply イメージ (GHCR)
┌──────────────────────────┐                  ┌──────────────┐
│ Dockerfile.devcontainer  │ -- FROM ... ---> │ /apply       │  FROM scratch、静的バイナリ
│ devcontainer.json        │                  └──────────────┘
│ target/                  │
└──────────────────────────┘
   RUN --mount=type=bind,from=apply,... /tmp/apply apply /tmp/ctx
   (パッケージ導入 → ファイル配置 → スクリプト実行。ビルドコンテキストを読み取り専用で bind する)
```

- `Dockerfile.devcontainer` は、利用側のリポジトリにコピーして使います。`BUILD_BASE_IMAGE` に、ベースイメージを渡します。
- ベースイメージの `USER` と `ENV`（`LANG` など）は変更しません。`USER` は、root で実行したあと、`BUILD_USER`（既定 `root`）に戻します。
- BuildKit が必要です（旧ビルダーでは `RUN --mount` が使えません）。

## 使い方

**1. 正本の Dockerfile をコピーします。**

[Dockerfile.devcontainer](Dockerfile.devcontainer) を、リポジトリのルートに置きます。monorepo なら、全層で 1 つです。`BUILD_APPLY_IMAGE` の既定値は、リリースした apply イメージの digest にしてください。

**2. 層ごとに、devcontainer.json を書きます。**

```jsonc
{
  "name": "python",
  "remoteUser": "vscode",
  "build": {
    "dockerfile": "../Dockerfile.devcontainer",   // build.context は省略する
    "args": {
      "BUILD_BASE_IMAGE": "ghcr.io/OWNER/base@sha256:...",
      "BUILD_PACKAGES": "git curl jq@{{ JQ_VERSION }}",
      "BUILD_PACKAGES_APT": "build-essential",
      "JQ_VERSION": "1.6-2.1"
    }
  }
}
```

**3. 必要なら、`target/` を置きます。**

```
.devcontainer/
├── devcontainer.json
└── target/
    ├── pre.d/        パッケージ導入の前に実行するスクリプト
    ├── packages/     default.txt apt.txt dnf.txt apk.txt zypper.txt
    ├── files/
    │   ├── root/     / に重ねるファイル (root:root)
    │   └── home/     開発ユーザーのホームに重ねるファイル
    └── post.d/       ファイル配置のあとに実行するスクリプト
```

`target/` が無くても動きます（その場合は `build.args` だけが使われます）。実行順は **pre → パッケージ → ファイル配置 → post** です。

## ファイル配置の規則

`files/root/<path>` は `/<path>` へ `root:root` で、`files/home/<path>` は開発ユーザーのホームへ、そのユーザーの所有で置きます。ホームが無ければ、`useradd -m` と同じ結果（`login.defs` の権限、`/etc/skel` の複製）で作ります。

| 拡張子 | 意味 |
|---|---|
| なし | 置換（既存があれば、`BUILD_OVERWRITE` が `false` でない限り、上書き。上書きはログに出る） |
| `.tmpl` | `{{ VAR }}` を展開して置換。未定義の変数はエラー。`{{ .Values.x }}` のような、他のテンプレートの記法は、そのまま残る |
| `.append` | 追記。配置先が無ければ新規作成。同じ内容が既にあれば、何もしない（冪等）。`.json` / `.toml` / `.yaml` / `.yml` は、キー単位でマージ |
| `.tmpl.append` | 展開してから追記（`.append.tmpl` でも同じ） |

- 実行順は、ディレクトリ → 置換 → 追記です。`foo.tmpl` と `foo.append` が両方あれば、置換してから追記します。同じ配置先を指すものが複数あればエラーです。
- 新規作成のときの権限は、規則で決まります: `~/.ssh` は 0700（ファイルは 0600、`*.pub` は 0644）、`~/.gnupg` は 0700 と 0600、`~/.netrc` などは 0600、`/etc/sudoers.d/*` は 0440。それ以外は、ディレクトリ 0755、ファイルは 0644（ソースに実行ビットがあれば 0755）。変えたいときは、`post.d/` で `chmod` します。
- **既存のファイルへの追記とマージは、権限と所有者を変えません。** 既存のディレクトリにも触りません。
- 次のものは拒否します: symlink、デバイス、setuid / setgid のソース。`/etc/passwd`、`/etc/shadow`、`/etc/group`、`/etc/sudoers`、`/etc/ld.so.preload`、`/proc`、`/sys`、`/dev`（拡張子を外した配置先と、symlink を解決した実体の、両方で判定します）。配置先が symlink なら、その先へは書き込みません。

## レシピ

スクリプトは、ベースイメージにある言語で、自由に書けます。ディストリビューションごとの違いを、CLI が吸収する代わりに、コピーして使えるスクリプトを [docs/recipes/](docs/recipes/) に置いています。CI で、各ディストリビューション上で、実際に実行して検証しています。

| レシピ | 内容 |
|---|---|
| [10-create-user.sh](docs/recipes/10-create-user.sh) | ユーザーを作る（`useradd` / busybox の `adduser`。UBI minimal では `shadow-utils` も導入） |
| [20-timezone-locale.sh](docs/recipes/20-timezone-locale.sh) | タイムゾーンと、日本語ロケール |
| [30-sudo.sh](docs/recipes/30-sudo.sh) | sudo の導入と、`/etc/sudoers.d` への、検証付きの配置 |
| [40-download-verified.sh](docs/recipes/40-download-verified.sh) | チェックサムが必須の、ダウンロードと展開 |

## build.args

| キー | 意味 |
|---|---|
| `BUILD_BASE_IMAGE` | ベースイメージ（`tag@sha256:...` を推奨） |
| `BUILD_APPLY_IMAGE` | apply イメージ（正本に既定値あり） |
| `BUILD_USER` | ベースの `USER` の写し。既定 `root`。**非 root のベースのときだけ**書く |
| `BUILD_OVERWRITE` | 既存ファイルの上書き。既定 `true`。上書きはログに出る |
| `BUILD_SCRIPT_TIMEOUT` | スクリプト 1 つあたりの timeout。既定 `10m` |
| `BUILD_PACKAGES`、`BUILD_PACKAGES_{APT,DNF,APK,ZYPPER}` | 追加パッケージ（空白かカンマ区切り。`name` または `name@version`） |
| 上記以外（`BUILD_` で始まらない） | テンプレート変数（`{{ VAR }}`）と、スクリプトの環境変数 |

- 値は文字列だけです。`${localEnv:...}` のような `${...}` を含む値は、apply が使うものではエラーになります（dev container tools が置換する記法で、apply は生ファイルを読むため）。
- `DCB_` で始まるキーは予約されています。
- `features` は無視して警告します。ユーザーとツールは、ベースイメージか `pre.d/` で用意してください。
- `--build-arg` で上書きして apply に届くのは、Dockerfile が宣言している ARG（`BUILD_BASE_IMAGE`、`BUILD_APPLY_IMAGE`、`BUILD_USER`）だけです。

### remoteUser と BUILD_USER

| ベースと開発ユーザー | 書くもの |
|---|---|
| ベースは root、開発ユーザーは `vscode` | `remoteUser: "vscode"` だけ |
| ベースは root、開発も root | 何も書かない |
| ベースは `node`、開発もそのユーザー | `BUILD_USER: "node"` だけ |
| ベースは `node`、開発は別ユーザー `vscode` | 両方書く |

`BUILD_USER` は、イメージの `USER` を**維持する**ためのものです。`remoteUser` を書いても、イメージの `USER` は変わりません。

## コマンド

```
devcontainer-builder apply [--config PATH] [--target PATH] [--check] [-v] <context-dir>
devcontainer-builder detect
devcontainer-builder version
```

| | |
|---|---|
| `--config` / `--target` | コンテキストからの相対パス。省略時は、コンテキストの中から `devcontainer.json` を探し、その隣の `target/` を使う |
| `--check` | 検証だけを行う（root も Linux も不要）。手元や CI での確認用 |
| `-v` | INFO の診断も表示する |
| `detect` | スクリプトに渡す `DCB_*` の判定結果を出力する（診断用） |

終了コードは、0 成功、1 失敗、2 使い方の誤りです。

手元で確かめる例:

```sh
go run . apply --check path/to/layer
```

## スクリプトに渡される環境変数

スクリプトは、先頭に shebang を書けば、ベースイメージにある言語で書けます。`DCB_*` は、すべて必ず設定されます（判定できなければ空文字）。

| 変数 | 内容 |
|---|---|
| `DCB_PHASE` | `pre` または `post` |
| `DCB_OS_ID` | os-release の `ID` |
| `DCB_OS_FAMILY` | `apt` / `dnf` / `apk` / `zypper` |
| `DCB_PKGMGR` | 見つかったコマンド（`apt-get` / `dnf` / `microdnf` / `yum` / `apk` / `zypper`） |
| `DCB_ARCH` | `amd64` / `arm64` など |
| `DCB_USER`、`DCB_HOME` | 開発ユーザーとそのホーム |
| `DCB_LANG` | 実行時点の `LANG`（ベースのもの） |

## 開発

```sh
go mod tidy          # 初回。go.sum を作る
gofmt -w .
go vet ./...
go test ./...
```

- Go 1.24 以上が必要です（`os.Root` を使います）。
- apply イメージ: `docker build -t apply:dev .`（`FROM scratch` に静的バイナリ `/apply` を置きます）。
- 依存は、[devcontainer-parser](https://github.com/imachange/devcontainer-parser)、`github.com/pelletier/go-toml/v2`、`gopkg.in/yaml.v3` です（YAML は、`go.yaml.in/yaml/v4` への移行を予定しています）。
- 一部のテストは、root のときに、本当の `chown` の経路を通ります。CI は、root の Docker コンテナの中でも、`go test` を実行します。

### CI

| ワークフロー | 内容 |
|---|---|
| [ci.yml](.github/workflows/ci.yml) | gofmt、`go mod tidy`、`go vet`、`go build`、`go test -race`、root での `go test`、`govulncheck`、apply イメージのビルド |
| [integration.yml](.github/workflows/integration.yml) | Debian、Ubuntu、Alpine、Fedora、UBI minimal、openSUSE（と、arm64）の上で、`Dockerfile.devcontainer` を、実際に `docker build` する。2 回重ねて冪等性を、非 root のベースで `USER` の維持を、確かめる |
| [release.yml](.github/workflows/release.yml) | タグ（`v*`）で、amd64 / arm64 の apply イメージを GHCR に push し、cosign で署名する |

統合テストは、[test/integration/](test/integration/) の、テスト用のコンテキストと `verify.sh` を使います。

## 関連

- [devcontainer-parser](https://github.com/imachange/devcontainer-parser): devcontainer.json の解析
- [devcontainer-bootstrap](https://github.com/imachange/devcontainer-bootstrap): ホスト側の初期化ツール

## ライセンス

[MIT](LICENSE)
