# Changelog

## 0.3.2 - Unreleased

First public release.

- REST API over a JSON or JSON5 file: collections, singletons, immediate atomic saves and reload on file change.
- Query syntax from json-server v1 and v0.17 in the default mode, and `--legacy` to follow v0.17 where they conflict.
- Filtering, sorting, pagination, slicing, full-text search, `_embed`, `_expand`, `_dependent`, `_where` and nested routes.
- `--routes` rewrites, static files, home page, CORS, gzip, `--delay`, `--read-only` and a config file.
- `--help`, error responses and log messages in English.
- Binaries for Linux, macOS and Windows.
- A comparison suite against real json-server 1.0.0-beta.15 and 0.17.4. Remaining differences are listed in [docs/COMPATIBILITY.md](docs/COMPATIBILITY.md).
