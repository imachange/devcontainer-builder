# devcontainer-builder

Dockerfile の `RUN` の中で、**ビルド時だけ**動く Go 製の CLI です。同じ Dockerfile を使い、`devcontainer.json` の `build.args` と `target/` ディレクトリの内容を反映したイメージを作ります。パッケージ導入、ファイル配置、スクリプト実行を CLI に任せるので、Dockerfile は十数行のままです。

CI で事前ビルドして GHCR に配信するイメージ（base → python → project → user のように、作成済みのイメージを次のベースにする構成）を想定しています。CLI は最終イメージに残りません。

> **状況**: 設計は [docs/design.md](docs/design.md) にまとまっています。現在の実装は、設定の読み込み、`target/` の規約の検査、環境の判定までで、**実行フェーズ（パッケージ導入、ファイル配置、スクリプト実行）は未実装**です。実行すべき構成で `--check` なしに実行すると、黙って成功せず、`not implemented yet` で失敗します。

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
- 依存は、[devcontainer-parser](https://github.com/imachange/devcontainer-parser) です。`go.mod` の版が存在しない場合は、`go get github.com/imachange/devcontainer-parser@latest` を実行してください。
- マージ機能の実装時に、`github.com/pelletier/go-toml/v2` と `go.yaml.in/yaml/v4` を追加します。

## 関連

- [devcontainer-parser](https://github.com/imachange/devcontainer-parser): devcontainer.json の解析
- [devcontainer-bootstrap](https://github.com/imachange/devcontainer-bootstrap): ホスト側の初期化ツール
