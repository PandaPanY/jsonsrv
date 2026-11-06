# Contributing

Bug reports and pull requests are welcome, in English or Chinese. 欢迎用中文或英文提交问题和代码。

## Reporting a compatibility bug

The most useful report gives the request, what JsonSrv returned, and what json-server returned, with its version (v1 beta or 0.17). Check [docs/COMPATIBILITY.md](docs/COMPATIBILITY.md) first: the difference may already be listed.

## Setting up

You need Go 1.27 or later. The comparison scripts also need Python 3, Node.js, curl and bash.

```bash
go test ./...
go vet ./...
gofmt -l .                  # should print nothing
golangci-lint run ./...
```

## Comparing with real json-server

```bash
bash scripts/setup-ref.sh       # installs both versions into .ref/ (ignored by git)
bash scripts/compare-all.sh     # 8 plans; non-zero exit on any failure
bash scripts/mutation-check.sh  # breaks 7 behaviours one at a time; each must be caught
```

Other checks:

```bash
bash scripts/crash-check.sh        # kill -9 during writes
bash scripts/concurrency-check.sh  # concurrent POSTs
bash scripts/perf-run.sh           # Linux only; timings depend on the machine
```

`perf-run.sh` currently exits non-zero because of the nested sort limitation in the compatibility document.

## Changing behaviour

1. Add a test that fails first.
2. Run `compare-all.sh`. If the change creates a difference from json-server that you intend to keep, add an entry to `KNOWN` in `scripts/compare.py`, with exact values and a `D` number, and describe it in `docs/COMPATIBILITY.md`. The script checks the numbers in both directions.
3. Update both language versions of any document you change.

Matching json-server is the goal, but copying its bugs is not. A request that silently returns 200 and an empty array is usually treated as a bug.

## Style

- Standard Go formatting and `golangci-lint`, configured in `.golangci.yml`.
- Comments explain why, not what. Existing comments are in Chinese; new ones can be in English or Chinese.
- No third-party Go dependencies without discussing it first. A single dependency-free binary is the point of the project.
- Name tests after the behaviour they check.

By contributing, you agree that your contribution is licensed under the MIT License.
