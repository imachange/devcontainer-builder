# レシピ集

ディストリビューションごとの違い（`useradd` と busybox の `adduser`、`apt` と `dnf` のパッケージ名、
musl にロケールが無いこと、など）は、devcontainer-builder が吸収せず、**スクリプトで書きます**。
`pre.d/` と `post.d/` のスクリプトは、ベースイメージの中で動くので、そのディストリビューションに合わせて、
シェルでも Python でも、書けます。

ここのスクリプトは、そのまま `pre.d/` や `post.d/` にコピーして使えます。
CI（[integration.yml](../../.github/workflows/integration.yml)）が、各ディストリビューションの上で、
実際に実行して、結果を検証しています。

## 使い方

1. 使うレシピと、[`_lib.sh`](_lib.sh) を、`target/pre.d/` か `target/post.d/` にコピーします。
2. 名前の先頭の数字で、実行順が決まります（`10-` より `20-` が後）。
3. 設定は、`devcontainer.json` の `build.args` に書きます。`BUILD_` で始まらないキーは、スクリプトの環境変数になります。

`_lib.sh` は、`_` で始まるので、実行されません。同じディレクトリから、`. "$(dirname "$0")/_lib.sh"` で読み込めます
（devcontainer-builder は、ディレクトリ全体を一時ディレクトリに複製して実行します）。

```
.devcontainer/target/
├── pre.d/
│   ├── _lib.sh
│   └── 10-create-user.sh
├── packages/default.txt
└── post.d/
    ├── _lib.sh
    ├── 20-timezone-locale.sh
    └── 30-sudo.sh
```

## レシピの一覧

| レシピ | 置き場所 | 内容 |
|---|---|---|
| [10-create-user.sh](10-create-user.sh) | `pre.d/` | ユーザーを作る。`files/home` の前に必要 |
| [20-timezone-locale.sh](20-timezone-locale.sh) | `post.d/` | タイムゾーンと、日本語ロケール |
| [30-sudo.sh](30-sudo.sh) | `post.d/` | sudo の導入と権限 |
| [40-download-verified.sh](40-download-verified.sh) | `post.d/` | チェックサム付きのダウンロード |

### 10-create-user.sh

| 環境変数 | 意味 |
|---|---|
| `USER_NAME` | ユーザー名。省略時は `DCB_USER`（`remoteUser`、`containerUser`、`BUILD_USER` の順で決まる） |
| `USER_UID`、`USER_GID` | UID と GID。省略時は、システムが決める |
| `USER_SHELL` | シェル。既定は `/bin/bash`（無ければ `/bin/sh`） |

- すでにユーザーがいれば、何もしません。`root` は作りません。
- Ubuntu 24.04 は、UID 1000 を `ubuntu` が使っています。UID を指定せず、システムに決めさせるか、別の UID を指定してください。
- UBI minimal には `useradd` が無いので、`shadow-utils` を導入します。

`remoteUser` を devcontainer.json に書いておけば、`DCB_USER` が、そのユーザー名になるので、`build.args` は不要です。

### 20-timezone-locale.sh

| 環境変数 | 意味 |
|---|---|
| `TZ_NAME` | タイムゾーン。既定 `Asia/Tokyo` |
| `LOCALES` | 生成するロケール（空白区切り）。既定 `ja_JP.UTF-8 en_US.UTF-8` |
| `DEFAULT_LANG` | 既定の `LANG`。既定は `LOCALES` の先頭 |

| 系統 | ロケールの生成 | 既定の書き込み先 |
|---|---|---|
| apt | `locales` を導入して `locale-gen` | `/etc/default/locale` |
| dnf | `glibc-langpack-<言語>` を導入 | `/etc/locale.conf` |
| zypper | `glibc-locale` を導入 | `/etc/locale.conf` |
| apk | しない（musl には、glibc のロケールが無い） | しない |

**`LANG` は、RUN からは設定できません。** `docker exec` や VS Code のターミナルに効くのは、環境変数 `LANG` で、
これは、ベースイメージの `ENV` か、利用側の devcontainer.json の `containerEnv` で決めます。
このレシピが書く `/etc/default/locale` などは、ログイン時にだけ読まれます。

### 30-sudo.sh

| 環境変数 | 意味 |
|---|---|
| `SUDO_USER_NAME` | 対象のユーザー。省略時は `DCB_USER` |
| `SUDO_NOPASSWD` | `1` なら、パスワード無し（`NOPASSWD`）。省略時は、パスワードが要る |

- `/etc/sudoers.d/<ユーザー>` を `0440` で書き、`visudo` があれば、構文を検証します。
- sudo は、名前に `.` を含むファイルや、`~` で終わるファイルを、**黙って無視する**ので、ユーザー名は、英数字、`_`、`-` に限ります。
- `NOPASSWD` は、そのユーザーが、コンテナの中で、root になれる設定です。公開するイメージでは、よく考えてください。

### 40-download-verified.sh

| 環境変数 | 意味 |
|---|---|
| `DOWNLOAD_URL` | `https://` の URL（必須） |
| `DOWNLOAD_SHA256` | 期待する SHA-256（必須。一致しなければ、何も配置しない） |
| `DOWNLOAD_DEST` | 配置先。`DOWNLOAD_EXTRACT=1` のときは、展開先のディレクトリ |
| `DOWNLOAD_MODE` | 権限。既定 `0644` |
| `DOWNLOAD_EXTRACT`、`DOWNLOAD_STRIP` | tar を展開する、先頭のディレクトリを落とす数 |

- チェックサムは、省略できません。チェックサムが無い取得は、再現性も、安全性も、保証できません。
- 展開しない場合は、配置済みで、同じ内容なら、何もしません（冪等）。
- `curl` か `wget` が無ければ、`curl` と `ca-certificates` を導入します。`packages/default.txt` に書いておくと確実です。

## 書き方の指針

- **冪等に書いてください。** 層を重ねると、同じスクリプトが、適用済みのイメージの上で、もう一度実行されます。
  `id "$user" || useradd ...` や、`grep -q ... || echo ... >> ...` の形にします。
- **導入したら、キャッシュを消してください。** `pkg_clean`（`_lib.sh`）を、導入したレシピの最後に呼びます。
- `DCB_OS_FAMILY`（`apt` / `dnf` / `apk` / `zypper`）と、`DCB_PKGMGR` で、分岐できます。
  スクリプトの直前ごとに、判定し直されるので、前のスクリプトが、パッケージマネージャを用意しても、反映されます。
- **`pre.d/` からは、`packages/` で入れるものを使えません。** 導入より前に実行されるためです。
  リポジトリの追加と、`files/home` のためのユーザー作成のように、導入より前に必要なものだけを置きます。
- 先頭に shebang が必須です。CRLF の改行だと、`bad interpreter` になるので、LF にします
  （このリポジトリの `.gitattributes` が、`*.sh` を LF に固定します）。
- シークレットを、`build.args` に書かないでください。値は、イメージの履歴に残ります。BuildKit の `--secret` を使います。
