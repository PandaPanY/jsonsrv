# Compatibility with json-server

[简体中文](COMPATIBILITY.zh-CN.md)

Compared against v1 = `1.0.0-beta.15` and v0.17 = `0.17.4`. The statements below come from running the same requests against the real servers with [`scripts/compare-all.sh`](../scripts/compare-all.sh).

## Syntax by mode

| Syntax | Default mode | `--legacy` |
|---|---|---|
| Collection and singleton CRUD, `GET /db`, immediate save | Supported; `DELETE` returns the deleted object (v1) | Supported; `DELETE` returns `{}` (v0.17) |
| `field:op=value` (`eq/ne/lt/lte/gt/gte/in/contains/startsWith/endsWith`, dotted nested fields) | v1 operators; `contains` and friends ignore case | Treated as an unknown field and ignored (v0.17: `views:gt=3` returns everything) |
| `field_suffix=value` (`_gt/_gte/_lt/_lte/_ne/_in/_contains/...`) | v1 suffix table | v0.17 suffix table |
| `field_like=value` | Regular expression, case-insensitive | Same (v0.17) |
| Unknown `field=value` | Ignored when no record has the field (as v0.17, see D19), so cache-busting parameters such as `?_=1699` do not empty the list. A field that exists but does not match still returns nothing. `field:op=value` is not covered: a missing field does not match | Ignored (v0.17) |
| `q=value` | Full-text match on the string form of any field, nested included, ignoring case; numbers and booleans count (as v0.17). Empty `q=` is treated as absent. v1 beta.15 always matches nothing; not reproduced (D5) | Same (v0.17) |
| Repeated `a=1&a=2` | Equals: OR (as v0.17; v1 uses the last value, see D15). `_ne`: all apply (AND). `:op`: last value wins (v1) | Non-`_ne`: OR. `_ne`: AND (v0.17) |
| `_sort` (`-` prefix, comma-separated fields) | v1: empty values last, missing and null keep their order, strings ignore case | lodash style: null last, strings case-sensitive (v0.17) |
| `_order=desc,asc` | Applied to `_sort` by position; a `-` prefix takes priority | Same (v0.17) |
| `_start`/`_end`/`_limit` | Slices (`_end` exclusive) and sends `X-Total-Count` and `Access-Control-Expose-Headers`; `_start` alone does not slice (as v0.17) | Same (v0.17) |
| `_page`/`_per_page` | Envelope `{data,first,prev,next,last,pages}`; invalid values are corrected (`_page=abc` is page 1, `_per_page=0` is 10) | `_page` slices 10 per page with `Link` and `X-Total-Count`; `_per_page` is ignored (v0.17) |
| `_page` + `_limit` | Still an envelope; `_limit` is the page size | Array with `Link` and `X-Total-Count` (v0.17) |
| `_embed=singular or plural` | v1: plural embeds children, singular embeds the parent | v0.17 expansion. Not the same as `_expand`: a target with no match gives `post: []` on each record |
| `_expand=plural` | v0.17: finds the target collection by pluralizing the name and attaches it through the `<singular>Id` foreign key. Works on lists and single records; repeated values all apply | Same (v0.17) |
| `_dependent=<child collection>` | Deleting a parent also deletes that collection's records pointing to it (repeat or use commas for several). No cascade without it | Same; also, with or without it, removes orphans whose foreign key points to a record that is gone (v0.17) |
| `_where=JSON` | Full v1 condition tree `{field:{op:value}}`, top-level `{"or":[...]}` and `{"and":[...]}`; invalid JSON falls back to plain conditions | Unknown parameter, ignored (v0.17) |
| Nested routes `/posts/1/comments` | Always recognized. GET compares the foreign key with the raw path segment, so numeric and string keys both match, and `007` does not match the number 7. A missing parent gives 200 and an empty array. POST: a foreign key in the body is kept; otherwise it takes the parent's id value when the parent exists, or the raw path text when it does not | Always recognized. A missing parent gives 200 and an empty array. POST always writes the raw path text as a string, overriding the body (v0.17) |
| `--routes` rewrite file | Supported (v0.17 format; verb prefixes such as `GET /api/*` are not) | Same |
| POST with an empty body | 201, created from `{}` (as both versions) | Same |
| Generated id on POST | 11-character random string | Numeric ids: max + 1. String-only ids: random string |
| Form and `text/plain` bodies | Any `Content-Type` is parsed as JSON (v1) | Forms are parsed as urlencoded; `text/plain` bodies are ignored (v0.17) |
| JSONP `callback` | Handled as an unknown parameter: ignored unless a `callback` field exists (as v0.17, see D19) | Parameter removed; neither mode wraps JSONP |
| Integer literals beyond JS safe range | Kept as written (the originals rewrite them; see D12) | Same |
| Response format | Compact single-line JSON; the data file keeps 2-space indent and key order | Same |
| `--` option terminator | `jsonsrv -m a.js --port 3000 -- db.json`: everything after `--` is positional | Same |
| `-m/--middlewares` | Not supported: script arguments (`.js/.cjs/.mjs/.ts`) get a clear message, others are handled as usual | Same |
| Home page, static files, CORS, gzip, `--delay`, `--read-only`, config file, `--quiet` | Supported | Supported |

## Known differences

The comparison suite records each remaining difference in the `KNOWN` table of [`scripts/compare.py`](../scripts/compare.py), with exact values and a number from this list. The numbers are checked in both directions.

- **D1 id stringification in v1 beta.15.** It turns numeric ids into strings when reading and then compares strictly, so on numeric data `?id:in=` finds nothing, `id:ne` has almost no effect, and `?_embed=` finds no matching foreign keys. JsonSrv keeps the original type and matches numerically. Without this, `:in`, foreign keys and `_embed` do not work on numeric data.
- **D2 `GET /db`.** v1 beta.15 returns 404. JsonSrv returns the whole database, as v0.17 does.
- **D3 Nested routes.** v1 returns 404 for `/categories/1/books`. JsonSrv supports them as v0.17 does, including 200 and an empty array for a missing parent, and adding the foreign key on POST.
- **D4 POST with a duplicate id.** v1 beta.15 appends a second record with the same id and returns 201. JsonSrv returns 409, because a duplicate write is most likely a caller error.
- **D5 `q` full-text search.** v1 beta.15 always matches nothing, although its README describes the feature. JsonSrv matches as v0.17 does, numbers and booleans included.
- **D6 CORS.** With an `Origin` header, JsonSrv echoes it and sends `Access-Control-Allow-Credentials: true`, as v0.17 does. v1 beta.15 sends `*` with no credentials header.
- **D7 `Vary`.** JsonSrv sends `Vary: Origin, Accept-Encoding`, as v0.17 does; v1 beta.15 sends none. In v0.17, POST responses add `X-HTTP-Method-Override`; `--legacy` does too, the default mode does not.
- **D8 Home page.** `GET /` returns a small HTML page in each server, and their content differs. v0.17's `charset=UTF-8` and Go's `utf-8` differ only in case.
- **D9 JSON `Content-Type`.** v1 beta.15 omits `charset`. JsonSrv always sends `charset=utf-8`, as v0.17 conventionally does.
- **D10 v0.17 syntax in the default mode.** `_limit`, `_start`/`_end` with `X-Total-Count`, `_order`, `_like`, `_expand`, repeated parameters, `"and"` in `_where`, and `_page` + `_limit`. v1 beta.15 treats these as field filters and returns empty arrays; the default mode follows v0.17. Sort comparison stays v1 (empty values last, strings ignore case).
- **D11 Invalid `_page`/`_per_page`.** `_page=abc` is page 1 and `_per_page=0` is 10. v1 beta.15 behaves differently.
- **D12 Large integers.** Both originals parse numbers as IEEE-754 doubles, so `12345678901234567890` becomes `12345678901234567000`. JsonSrv keeps the original text.
- **D13 Colon operators.** The default mode supports `field:op=value`. v0.17 drops these parameters and returns everything. `--legacy` follows v0.17.
- **D14 Arrays.** Plain `field=value` on an array field matches when any element is equal. v0.17 compares the array's string form (`"a,b"`). This is a deliberate relaxation; `--legacy` follows v0.17.
- **D15 Repeated parameters.** Repeated equals are OR, the same as v0.17 on non-array fields. The comparison differs only on array fields, because of D14: for `tags=b&tags=c`, v0.17 returns `[2,4]` and the default mode also matches `tags:["a","b"]`. v1 beta.15 uses the last value.
- **D16 Unknown `_embed` target.** The default mode adds no key, as v1 beta.15. v0.17 adds `x: []` to every record; `--legacy` follows v0.17.
- **D17 `field=null`.** The default mode matches null fields, as v1 beta.15. v0.17 compares it as a string and matches nothing; `--legacy` follows v0.17.
- **D18 Sorting strings.** The default mode uses v1 sort-on (lowercased comparison, empty values last). v0.17 uses lodash (case-sensitive, null participates).
- **D19 Unknown fields in equals parameters.** When no record has the field, the default mode ignores the parameter, as v0.17 does. v1 beta.15 returns an empty array, which would silently empty the list for any front end sending `?_=1699` or `?t=1699`.
- **D20 POST in the default mode compared with v0.17.** The default mode generates an 11-character random id and sends no `Location`, as v1 does for collections. v0.17 generates a numeric id and sends `Location`. The foreign key itself is the same in both. `--legacy` matches v0.17.

Differences the comparison normalizes rather than records:

- **Error bodies.** When the status codes match (≥400), the text is not compared. JsonSrv sends `{"message": "..."}`; v1 uses `{"error": "Not Found"}`.
- **JSON formatting.** Bodies are compared as parsed JSON. JsonSrv sends one line; the originals indent.
- **Generated ids.** Random id values are replaced before comparing.
- **Purely numeric foreign key strings.** The comparison treats `"007"` and 7 as equal in id and foreign key fields, so these cases are covered by unit tests in `internal/httpapi/param_edge_cases_test.go` instead.

Other behaviour worth knowing:

- **`Infinity`/`NaN`.** JSON has no such values. JsonSrv loads them as `null` and warns once at startup.
- **Writing `.json5`.** Files are written back as standard JSON, dropping comments, as v1 beta.15 does. A message is printed on load.
- **`--routes` query values.** JsonSrv escapes `&`, `=` and `#` when a rewrite puts a value into the query string. v0.17 does not, which allows parameter injection.
- **`--middlewares` arguments.** Only script extensions are consumed. In v0.17, yargs also consumes the following non-option arguments, including the data file.

## Known limitations

- **Sorting on nested fields.** On 100,000 records with a 100 ms target, `_sort=author.name,-views&_page=2&_per_page=100` takes about 125 ms, and `_sort=author.name&_page=2&_per_page=100` about 100 ms, right at the target. sort-on semantics require normalizing each record's sort fields before comparing. Because of this, `scripts/perf-run.sh` currently exits non-zero.
- **Write throughput.** On 100,000 records under continuous POSTs, a POST averages about 260 ms including serialization and atomic save, while GET p50 and p90 stay around 1 ms. Larger files write more slowly, because every write rewrites the whole file.
- **Loose matching in the default mode, kept from v1.** A URL value that looks like a number is also compared numerically, so `zip=02134` matches the number 2134, and `n=05` and `n=1e3` match 5 and 1000; v0.17 does not. With a null leaf field, `w_gte=0` also matches, since `null >= 0` is true in JavaScript. Use `_where` for strict comparison.
- **Platforms.** Unit tests pass on Linux, Windows and macOS in CI. The comparison with real json-server runs on Linux only. Behaviour in real browsers and `--watch` under heavy editor saves have not been tested.
- **JavaScript middlewares.** `-m/--middlewares` needs a Node.js runtime and is not supported. You get a clear message rather than silent ignoring.
