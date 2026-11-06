# JsonSrv

[![CI](https://github.com/PandaPanY/jsonsrv/actions/workflows/ci.yml/badge.svg)](https://github.com/PandaPanY/jsonsrv/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

[简体中文](README.zh-CN.md)

A single-binary, no-Node.js alternative to [json-server](https://github.com/typicode/json-server): point it at a JSON file and get a full REST API for prototyping and front-end development.

```bash
jsonsrv db.json
curl http://localhost:3000/posts/1
```

JsonSrv is an independent project and is not affiliated with json-server. It is written in Go with no third-party dependencies, and it understands both the v1 and v0.17 json-server query syntax.

## Why JsonSrv

- **No runtime to install.** One file of about 7.5 MB. Useful on machines and CI images without Node.js.
- **Both query syntaxes.** Requests written for json-server v1 or v0.17 work in the default mode. `--legacy` follows v0.17 where the two conflict.
- **Compatibility is measured.** A test suite runs the same requests against real json-server 1.0.0-beta.15 and 0.17.4 and compares status codes, headers, bodies and the saved file. Every remaining difference is listed in [docs/COMPATIBILITY.md](docs/COMPATIBILITY.md).

It is close to a drop-in replacement, but not an exact one. Check the differences if your front end relies on a specific edge case, and note that JavaScript middlewares (`--middlewares`) are not supported.

## Install

Download a binary from the [Releases page](https://github.com/PandaPanY/jsonsrv/releases). Each release includes `SHA256SUMS`.

| File | Platform |
|---|---|
| `jsonsrv-linux-amd64` | Linux x86_64 |
| `jsonsrv-linux-arm64` | Linux ARM64 |
| `jsonsrv-darwin-amd64` | macOS Intel |
| `jsonsrv-darwin-arm64` | macOS Apple Silicon |
| `jsonsrv-windows-amd64.exe` | Windows x86_64 |

Or install with Go 1.27 or later:

```bash
go install github.com/PandaPanY/jsonsrv/cmd/jsonsrv@latest
```

Or build from source:

```bash
git clone https://github.com/PandaPanY/jsonsrv.git
cd jsonsrv
go build -o jsonsrv ./cmd/jsonsrv
```

## Quick start

```bash
jsonsrv                  # uses ./db.json, listens on http://localhost:3000
jsonsrv db.json          # .json5 also works
jsonsrv --legacy db.json # follow v0.17 where it conflicts with v1
```

Try it with [example/db.json](example/db.json):

```bash
jsonsrv example/db.json

curl http://localhost:3000/posts/1
curl -X POST -H "Content-Type: application/json" \
  -d '{"title":"hello","views":0}' http://localhost:3000/posts
curl "http://localhost:3000/posts?views:gt=100&_sort=-views&_page=1&_per_page=2"
curl "http://localhost:3000/posts/1/comments"
```

Writes are saved to the data file immediately, using a temporary file and a rename, so killing the process does not corrupt the file. If you edit the file by hand, the server reloads it (`--no-watch` turns this off).

## What it supports

| Feature | Example |
|---|---|
| CRUD on collections and singletons | `GET/POST /posts`, `GET/PUT/PATCH/DELETE /posts/1`, `GET /profile` |
| v1 operators | `views:gt=100`, `title:contains=go`, `id:in=1,2` |
| v0.17 operators | `views_gte=100`, `title_like=^go`, `id=1&id=2`, `q=text` |
| Sorting | `_sort=-views,title`, `_sort=views&_order=desc` |
| Pagination | `_page=2&_per_page=10`, `_start=0&_end=10`, `_limit=10` |
| Relationships | `_embed=comments`, `_expand=post`, `/posts/1/comments`, `DELETE /posts/1?_dependent=comments` |
| Complex filters | `_where={"or":[{"views":{"gt":100}},{"title":{"contains":"go"}}]}` |
| Everything else | whole-database `GET /db`, static files, home page, CORS, gzip, `--routes` rewrites, `.json5`, response delay, read-only mode |

## Command-line options

| Option | Default | Description |
|---|---|---|
| `-p`, `--port` | `3000` | Port |
| `--host` | `localhost` | Address to listen on |
| `--watch` / `--no-watch` | on | Reload when the data file changes |
| `-s`, `--static` | `./public` if present | Static directory; can be repeated |
| `-r`, `--routes` | | Path rewrite file in v0.17 format |
| `--read-only` | off | Write requests return 403 |
| `--no-cors` | off | Disable CORS |
| `--no-gzip` | off | Disable gzip |
| `--delay` | | Response delay in milliseconds |
| `--id` | `id` | Name of the id field |
| `--fks` | `Id` | Foreign key suffix for nested routes |
| `--legacy` | off | Follow v0.17 where it conflicts with v1 |
| `--quiet` | off | Do not log requests |
| `--config` | `./json-server.json` | Config file path |
| `--version` | | Print the version |
| `-h`, `--help` | | Print help |

A config file is a JSON object with the same names as the options, for example `{"port": 8080, "read-only": true}`. Command-line arguments take priority.

## Limitations

- **Sorting on nested fields is slower than the other queries.** On 100,000 records, `_sort=author.name,-views` takes about 125 ms and `_sort=author.name` about 100 ms. Other queries measured 4–65 ms.
- **Every write rewrites the whole file.** With 100,000 records, a POST takes about 260 ms. Reads are not blocked while a write is saved.
- **This is a development tool.** It has no authentication, and with CORS on, any web page you visit can read and change the data. See [SECURITY.md](SECURITY.md).
- **Not everything is verified on every platform.** Unit tests pass on Linux, Windows and macOS in CI. The comparison with real json-server and the crash and concurrency checks run on Linux only. Release binaries are cross-compiled, and only the Linux x86_64 one has been run directly. Behaviour in real browsers and `--watch` under heavy editor saves have not been tested.

## Development

```bash
go test ./...                     # unit tests
bash scripts/setup-ref.sh         # install the reference json-server versions (needs Node.js)
bash scripts/compare-all.sh       # compare against real json-server
bash scripts/mutation-check.sh    # check that the comparison catches deliberate bugs
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for more.

## License and acknowledgements

MIT. See [LICENSE](LICENSE).

JsonSrv reimplements the behaviour of [json-server](https://github.com/typicode/json-server) by typicode. Parts of the query logic and the pluralization rules are ported from json-server, [pluralize](https://github.com/plurals/pluralize) and [sort-on](https://github.com/sindresorhus/sort-on), which are MIT-licensed. Their notices are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
