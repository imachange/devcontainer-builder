# 実装の点検結果

2026-11-10 時点の実装（`pkgmgr`、`place`、`merge`、`script`、`apply`）を、設計書（[design.md](design.md)）と、
次の観点で点検した結果です。

- **セキュリティ**: 拒否リストのすり抜け、symlink、権限、コマンドへの注入
- **データの損失**: 黙って内容が失われる、意図しない権限や所有者の変更
- **実行順序**: 計画を作った時点と、実行する時点の食い違い
- **設計書との不一致**

「状態」が「修正済み」のものは、コードとテストを、同時に直しています。
この文書を書いた環境には Go が無く、コンパイルとテストを実行していません。**CI（`ci.yml`、`integration.yml`）で確認してください。**
最初に `gofmt -w .` を実行してください。

## 重大度 高

| # | 場所 | 問題 | 影響 | 修正 | テスト |
|---|---|---|---|---|---|
| 1 | `place` | 拒否リストの判定が、拡張子（`.append`、`.tmpl`）を外す前のパスで行われていた | `files/root/etc/shadow.append` で、`/etc/shadow` などの拒否リストをすり抜けて、追記できた | 拡張子を外した配置先で判定する。実行の直前に、symlink を解決した実体でも判定する（`guardDest`） | `TestDeniedPathCannotBeBypassedWithSuffixes` |
| 2 | `place` | 秘密鍵の権限規則（`.ssh` は 0700 と 0600 など）が、`files/home` では、効かなかった。判定に、ホームの絶対パス（`/home/alice/.ssh/...`）を渡していた | 秘密鍵が 0644 で置かれた | ツリーの中の相対パスで判定する。`files/root` の `/root/...` と `/home/<user>/...` にも適用 | `TestPermissionsInHomeTree`、`TestPermissionsForRootAndHomeDirectoriesInRootTree` |
| 3 | `apply`、`place` | ファイル配置の計画を、pre.d とパッケージ導入の前に作っていた | pre.d が作ったユーザーを見落とし、`files/home` が黙って捨てられるか、root 所有で置かれた | 事前検証（書き込みなし）と、配置の直前の 2 回、環境を判定し直して計画する。ユーザーが未作成なら `HomePending` とし、事前検証では許し、実行では `ErrHomeUnresolved` で失敗させる | `TestHomePendingIsNotExecutable`、`TestApply_FilesHomeForRootUser` |
| 4 | `pkgmgr` | パッケージ名と版を検証せず、そのまま引数にしていた | `-oAPT::Update::Pre-Invoke::=...` のような名前が、apt-get のオプションとして解釈され、任意のコマンドを実行できた | 名前と版を、厳密な書式で検証する（先頭は英数字）。`Validate` で、全系統を、スクリプトの実行前に検証する | `TestParseLineRejectsOptionsAndJunk`、`TestApply_InvalidPackageSpecFailsBeforeAnyScriptRuns` |
| 5 | `place` | 既存ファイルへの追記とマージの後に、`chmod` と `chown` を行っていた | 0600 のファイルに追記すると、0644 になり、秘密が読めるようになった | 追記とマージは、既存の権限、所有者、inode を変えない（その場で書き換える）。既存のディレクトリにも触らない | `TestAppendKeepsExistingModeAndContent`、`TestExistingDirectoriesAreLeftAlone` |
| 6 | `place` | ホームの作成が、root 所有の 0700 で、親ディレクトリ（`/home`）も 0700 で作り、`/etc/skel` も複製しなかった | ユーザーが、自分のホームに入れない。他のユーザーが `/home` を通れない | 親は 0755、ホームは `login.defs` の `HOME_MODE`（無ければ `UMASK`、無ければ 0755）で、ユーザーの所有で作り、skel を、権限を保って複製する。`/nonexistent` などの、実体のないホームは拒否する | `TestCreateHomeLikeUseradd`、`TestPlanRejectsHomesThatAreNotRealHomes`、`TestParseHomeMode` |
| 7 | `merge` | YAML の複数ドキュメント（`---` 区切り）の、2 つ目以降を、黙って捨てて書き戻していた | 既存の設定が失われた | 複数ドキュメントは、エラーにする | `TestYAMLMultipleDocumentsAreRejected`、`TestStructuredAppendFailsOnBrokenExistingFile` |
| 8 | `place` | 配置先が symlink のとき、その先へ書き込んでいた。ルートの外へ出る symlink も、検査していなかった | symlink 経由で、拒否リストの外から、拒否対象へ書ける | 最終要素が symlink なら拒否し、親の symlink は、解決した実体が、ルートの中で、拒否リストに当たらないときだけ許す（`/bin -> usr/bin` は許す） | `TestDestinationSymlinkIsNotFollowed`、`TestParentSymlinkOutsideRootIsRejected`、`TestMergedUsrSymlinkDirectoryIsAllowed` |

## 重大度 中

| # | 場所 | 問題 | 修正 | テスト |
|---|---|---|---|---|
| 9 | `place` | 追記が、置換より先に実行された（辞書順で `.append` が `.tmpl` より前）。`foo.tmpl` と `foo.append` があると、追記が上書きされて消えた | ディレクトリ → 置換 → 追記の順に並べる | `TestReplaceRunsBeforeAppend` |
| 10 | `place` | 同じ配置先を指すものを検出しなかった（`foo` と `foo.tmpl`）。後のものが、黙って勝った | エラーにする。ディレクトリとファイルの衝突も | `TestDuplicateDestinationsAreRejected`、`TestDirectoryAndFileCollisionIsRejected` |
| 11 | `place` | 追記の冪等性の判定が `HasSuffix` で、行の途中の一致（`xfoo` の中の `foo`）でも「追記済み」とみなした | 行の境界に揃った、連続した一致だけを、追記済みとする | `TestAppendDoesNotMatchInTheMiddleOfALine`、`TestContainsLines` |
| 12 | `place` | `chown` の失敗を握りつぶしていた | root で失敗したらエラーにする（root 以外は、テストのため無視） | `TestCreateHomeLikeUseradd`（root の CI でも通る） |
| 13 | `place` | 既存のディレクトリ（`/etc` など）に、`chown` していた | 新規に作ったディレクトリだけ、所有者を変える | `TestExistingDirectoriesAreLeftAlone` |
| 14 | `place` | 置換が、直接の `WriteFile` だった | 一時ファイルに書いてから `rename` する（権限と所有者は、新しいファイルのもの） | `TestReplaceAppliesNewModeToExistingFile` |
| 15 | `place` | テンプレートが `text/template`（`{{ .VAR }}`）で、`pkgmgr` と README の `{{ VAR }}` と食い違っていた。`{{` を含む設定ファイル（Prometheus、Helm など）が壊れた | `internal/tmpl` に一本化。`{{ VAR }}` と `{{ .VAR }}` の両方を許し、他の記法は、そのまま残す | `internal/tmpl` のテスト、`TestTemplateLeavesGoTemplateSyntaxAlone` |
| 16 | `place` | 新規の構造化ファイル（`.json.append`）の構文を、検証せずに書いていた | 書く前に検証する | `TestStructuredAppendRejectsInvalidPatchForNewFile` |
| 17 | `pkgmgr` | ソース間の版の差し替えを、「ダウングレード」として拒否していた（`build.args` で、`packages/` より低い版にできない）。決めた仕様（後勝ち）と違い、版の比較も粗かった | 後ろのソースが勝つ。差し替えは、ログに出す | `TestBuildPlanLaterSourceWinsEvenWhenLower` |
| 18 | `pkgmgr` | コメントの中の `{{ }}` を展開して、未定義の変数のエラーにしていた | コメントを除いてから展開する | `TestBuildPlanDoesNotExpandComments` |
| 19 | `pkgmgr` | コメントだけの `packages/*.txt` で、パッケージマネージャが無い環境（distroless）が、「見つからない」で失敗した | 項目を解釈してから、必要なときだけ、検出を要求する | `TestBuildPlanCommentOnlyFileNeedsNoManager` |
| 20 | `script` | 実行するスクリプト 1 つだけを複製していた。`_lib.sh` のような部品を、読み込めなかった | ディレクトリ全体を、一時ディレクトリに複製して実行する | `TestRunDir_CanSourceHelperLibrary`、`TestApply_ScriptsCanUseHelperLibrary` |
| 21 | `script` | timeout のとき、`cmd.Wait()` を待たずに戻っていた。出力バッファとの競合と、一時ディレクトリの削除中の実行が起こりえた。キャンセルのときは、プロセスグループが残った | `cmd.Cancel` でプロセスグループごと kill し、`WaitDelay` を設定して、`Run` を同期的に待つ | `TestRunFile_TimeoutKillsTheWholeProcessGroup`、`TestRunFile_ContextCancellationStopsTheScript` |
| 22 | `script` | プロキシと CA の環境変数を、捨てていた | `http_proxy`、`https_proxy`、`no_proxy`、`SSL_CERT_FILE` などを、許可リストで引き継ぐ。それ以外は、引き継がない | `TestRunFile_PassesProxyButNotOtherBaseEnv` |
| 23 | `apply` | `DCB_*` を、スクリプトの直前ごとに判定し直していなかった | `DCBEnvFunc` を、スクリプトごとに呼ぶ | `TestRunDir_EvaluatesDCBEnvBeforeEachScript` |
| 24 | `apply` | `--check` が、ファイル配置の計画を検証していなかった | `--check` でも、書き込みなしで、計画まで検証する | `TestApply_CheckAlsoValidatesFilePlacement` |
| 25 | `apply` | パッケージの書式の誤りが、pre.d の実行後に、分かった | `Validate` を、pre.d の前に行う | `TestApply_InvalidPackageSpecFailsBeforeAnyScriptRuns` |
| 26 | `merge` | JSON の末尾の余分なトークン（`}}`）を、見逃した。出力の末尾に改行が無かった | EOF まで読んで検証する。末尾に改行を付ける | `TestJSONTrailingGarbageIsRejected`、`TestJSONOutputEndsWithNewline` |

## 重大度 低

| # | 場所 | 問題 | 修正 |
|---|---|---|---|
| 27 | `pkgmgr` | apt の `/var/lib/apt/lists` が、イメージに残った（`apt-get clean` では消えない） | `DirCleaner` で、中身を削除する。本物の Runner のときだけ（モックでは、ホストを触らない） |
| 28 | `pkgmgr` | リトライの待ち時間が、キャンセルできなかった | `ctx` を見て待つ。`TestExecutorRetryHonorsContext` |
| 29 | `script` | CRLF の shebang が、`bad interpreter` という分かりにくいエラーになった | 検出して、`ErrBadShebang` で、明確に知らせる。`.gitattributes` で `*.sh` を LF に固定する |
| 30 | `script` | `build.args` で、`DCB_*` を偽装できる余地があった | `script` の側でも、`DCB_` で始まる `build.args` を、環境に渡さない |
| 31 | `apply` | `SIGTERM` で、実行中のスクリプトを止められなかった | `signal.NotifyContext` で、`RunContext` にキャンセルを伝える |
| 32 | `apply` | `umask` を固定していなかった | 022 にする（unix のみ） |
| 33 | `apply` | `ErrNotImplemented` と、README の「未実装」の記述が、残っていた | 削除して、README を更新した |
| 34 | `place` | `BUILD_OVERWRITE=false` の確認が、pre.d の前の状態に対する、古い確認だった。追記にも「上書き」のログを出していた | 実行の直前に、あらためて確認する（早く失敗するための確認は残す）。ログは、置換のときだけ出す |

## 修正していないもの（既知のリスクと、判断が必要なもの）

| 項目 | 内容 |
|---|---|
| YAML ライブラリ | 決定は `go.yaml.in/yaml/v4` だが、実装は `gopkg.in/yaml.v3`。v4 の API とリリース状況を、確認できなかったので、移行していない |
| JSONC | 設計は「拒否」だが、実装は、コメントを除いて受け付ける。書き戻しで、コメントは失われる。devcontainer.json を `.append` でマージする用途では便利なので、変更していない |
| sudoers の検証 | `/etc/sudoers.d/*` への配置は許可し、モードを 0440 にするが、`visudo -cf` による検証は、`place` では行わない（レシピ `30-sudo.sh` は行う）。壊れた sudoers を `files/` に置くと、sudo が全面的に使えなくなる |
| dnf / apt のフラグ | `install_weak_deps` や `Acquire::Retries` は、実機で確認できないので、追加していない（イメージが、わずかに大きくなる） |
| `.append` の意味 | 「同じ内容が、ファイルのどこかに連続してあれば、追記済み」。順序に意味がある設定（`PATH` の前置など）では、末尾にあることは、保証しない |
| パッケージの版を下げる | 同じイメージの上で、導入済みの版より低い版を指定すると、パッケージマネージャが、失敗することがある。下の層に入れない設計にする |
| TOCTOU | `guardDest` の確認と、書き込みの間の、競合は、考慮していない（ビルドの RUN の中で、単独で動くため） |
| Windows | `syscall.Stat_t`、`Setpgid` などを使うので、unix 以外ではビルドできない（apply は Linux のみが対象） |
| `place` の入力 | ソース（`target/files`）は、`os.Root` ではなく、通常のパスで読む。コンテキストは、読み取り専用の bind なので、実害は小さい。`layout.Scan` が、symlink を拒否している |

## 確認が必要なこと（実機）

統合テスト（`integration.yml`）の、最初の実行で、次の点が確かめられます。失敗したら、ディストリビューションごとの違いなので、レシピか、`verify.sh` を直してください。

- UBI minimal の `microdnf` で、`shadow-utils`、`glibc-langpack-ja`、`sudo` が導入できること。
- openSUSE の `glibc-locale`、`timezone` というパッケージ名。
- Alpine の `sudo` に `visudo` が含まれ、`/etc/sudoers` が `/etc/sudoers.d` を読み込むこと。
- `RUN --mount` のマウント先（`/tmp/apply`、`/tmp/ctx`）が、イメージに残るか（`verify.sh` が `note` として出す）。
- コンテキストの外の `Dockerfile.devcontainer` を、`docker build -f` で渡せること（このリポジトリの統合テストが、まさに、その形）。
- `${BUILD_USER:-root}` が、`USER` で期待どおり動くこと（`non-root-base` ジョブが検証する）。
