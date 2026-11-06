# JsonSrv

[![CI](https://github.com/PandaPanY/jsonsrv/actions/workflows/ci.yml/badge.svg)](https://github.com/PandaPanY/jsonsrv/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

[English](README.md)

[json-server](https://github.com/typicode/json-server) 的单文件替代品，不需要 Node.js：给它一个 JSON 文件，就能得到一套完整的 REST 接口，用于原型开发和前端联调。

```bash
jsonsrv db.json
curl http://localhost:3000/posts/1
```

JsonSrv 是独立项目，和 json-server 没有隶属关系。它用 Go 编写，没有第三方依赖，同时识别 json-server v1 和 v0.17 的查询写法。`--help`、错误响应和日志都是英文。

## 为什么用 JsonSrv

- **不用装运行环境。** 只有一个约 7.5 MB 的文件，适合没有 Node.js 的机器和 CI 镜像。
- **两种写法都认。** 为 json-server v1 或 v0.17 写的请求，在默认模式下都能用；两者冲突时，用 `--legacy` 跟随 v0.17。
- **兼容性是测出来的。** 测试会把同一批请求发给真实的 json-server 1.0.0-beta.15 和 0.17.4，比较状态码、响应头、响应体和保存的文件。所有剩余差异都列在 [docs/COMPATIBILITY.zh-CN.md](docs/COMPATIBILITY.zh-CN.md)。

它接近直接替换，但并非完全一致。如果前端依赖某个边界行为，请先查一下差异列表。另外不支持 JS 中间件（`--middlewares`）。

## 安装

从 [Releases 页面](https://github.com/PandaPanY/jsonsrv/releases)下载，每个版本都附 `SHA256SUMS`。

| 文件 | 平台 |
|---|---|
| `jsonsrv-linux-amd64` | Linux x86_64 |
| `jsonsrv-linux-arm64` | Linux ARM64 |
| `jsonsrv-darwin-amd64` | macOS Intel |
| `jsonsrv-darwin-arm64` | macOS Apple Silicon |
| `jsonsrv-windows-amd64.exe` | Windows x86_64 |

也可以用 Go 1.27 或更新版本安装：

```bash
go install github.com/PandaPanY/jsonsrv/cmd/jsonsrv@latest
```

或者从源码构建：

```bash
git clone https://github.com/PandaPanY/jsonsrv.git
cd jsonsrv
go build -o jsonsrv ./cmd/jsonsrv
```

## 快速开始

```bash
jsonsrv                  # 使用当前目录的 db.json，监听 http://localhost:3000
jsonsrv db.json          # 也支持 .json5
jsonsrv --legacy db.json # 和 v1 冲突时跟随 v0.17
```

用 [example/db.json](example/db.json) 试一下：

```bash
jsonsrv example/db.json

curl http://localhost:3000/posts/1
curl -X POST -H "Content-Type: application/json" \
  -d '{"title":"hello","views":0}' http://localhost:3000/posts
curl "http://localhost:3000/posts?views:gt=100&_sort=-views&_page=1&_per_page=2"
curl "http://localhost:3000/posts/1/comments"
```

写操作会立即保存到数据文件（先写临时文件再改名，进程被杀也不会损坏原文件）。手动编辑文件后，服务会自动重新加载（`--no-watch` 关闭）。

## 支持的功能

| 功能 | 例子 |
|---|---|
| 集合和单例的增删改查 | `GET/POST /posts`、`GET/PUT/PATCH/DELETE /posts/1`、`GET /profile` |
| v1 运算符 | `views:gt=100`、`title:contains=go`、`id:in=1,2` |
| v0.17 运算符 | `views_gte=100`、`title_like=^go`、`id=1&id=2`、`q=text` |
| 排序 | `_sort=-views,title`、`_sort=views&_order=desc` |
| 分页 | `_page=2&_per_page=10`、`_start=0&_end=10`、`_limit=10` |
| 关联 | `_embed=comments`、`_expand=post`、`/posts/1/comments`、`DELETE /posts/1?_dependent=comments` |
| 复杂条件 | `_where={"or":[{"views":{"gt":100}},{"title":{"contains":"go"}}]}` |
| 其他 | 整库 `GET /db`、静态文件、首页、CORS、gzip、`--routes` 重写、`.json5`、响应延迟、只读模式 |

## 命令行选项

| 选项 | 默认值 | 说明 |
|---|---|---|
| `-p`, `--port` | `3000` | 端口 |
| `--host` | `localhost` | 监听地址 |
| `--watch` / `--no-watch` | 开 | 数据文件变化时重新加载 |
| `-s`, `--static` | `./public`（如存在） | 静态目录，可重复指定 |
| `-r`, `--routes` | | v0.17 格式的路径重写文件 |
| `--read-only` | 关 | 写请求返回 403 |
| `--no-cors` | 关 | 关闭 CORS |
| `--no-gzip` | 关 | 关闭 gzip |
| `--delay` | | 响应延迟（毫秒） |
| `--id` | `id` | id 字段名 |
| `--fks` | `Id` | 嵌套路由的外键后缀 |
| `--legacy` | 关 | 和 v1 冲突时跟随 v0.17 |
| `--quiet` | 关 | 不打印请求日志 |
| `--config` | `./json-server.json` | 配置文件路径 |
| `--version` | | 显示版本 |
| `-h`, `--help` | | 显示帮助 |

配置文件是一个 JSON 对象，键名与选项相同，例如 `{"port": 8080, "read-only": true}`。命令行参数优先。

## 限制

- **按嵌套字段排序比其他查询慢。** 10 万条数据下，`_sort=author.name,-views` 约 125 毫秒，`_sort=author.name` 约 100 毫秒；其他查询为 4–65 毫秒。
- **每次写入都重写整个文件。** 10 万条数据下，一次 POST 约 260 毫秒。保存期间不阻塞读请求。
- **这是开发工具。** 没有身份验证；开启 CORS 时，你访问的任何网页都能读取和修改数据。见 [SECURITY.md](SECURITY.md)。
- **不是每个平台都验证了全部内容。** 单元测试在 CI 的 Linux、Windows、macOS 上都通过。和真实 json-server 的对照、崩溃和并发检查只在 Linux 上跑。发布产物是交叉编译的，只有 Linux x86_64 版实际运行过。真实浏览器中的行为，以及编辑器频繁保存时的 `--watch`，都还没有测过。

## 开发

```bash
go test ./...                     # 单元测试
bash scripts/setup-ref.sh         # 安装对照用的 json-server（需要 Node.js）
bash scripts/compare-all.sh       # 和真实 json-server 对照
bash scripts/mutation-check.sh    # 检查对照能否抓到故意改坏的代码
```

更多内容见 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 许可证和致谢

MIT，见 [LICENSE](LICENSE)。

JsonSrv 重新实现了 typicode 的 [json-server](https://github.com/typicode/json-server) 的行为。部分查询逻辑和单复数规则移植自 json-server、[pluralize](https://github.com/plurals/pluralize) 和 [sort-on](https://github.com/sindresorhus/sort-on)，它们都是 MIT 许可，声明见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
