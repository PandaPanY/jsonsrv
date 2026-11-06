# Security

## Reporting a vulnerability

Please use GitHub's [private vulnerability reporting](https://github.com/PandaPanY/jsonsrv/security/advisories/new) rather than a public issue. This is a personal project, so responses are best effort. Fixes go into the latest release only.

中文说明：请通过上面的链接私下报告漏洞，不要公开提 issue。

## Intended use

JsonSrv is a mock server for local development. It is not meant to be exposed to untrusted networks.

- **No authentication.** Anyone who can reach the port can read and change all the data.
- **Permissive CORS.** By default the server echoes the request's `Origin` and allows credentials, as json-server 0.17 does. Any web page open in your browser can therefore read and modify the data on `localhost`. Use `--no-cors` or `--read-only` if the data matters.
- **Listens on `localhost` by default.** `--host 0.0.0.0` exposes it to your network, with the risks above.
- **Static files.** Everything in the static directory (`./public` by default) is served.
- **Regular expressions.** `_like` accepts a regular expression from the request. Go's regexp engine runs in linear time, but very large data files can still make requests slow.

Reports about the missing authentication and the CORS defaults above are expected behaviour, not vulnerabilities.
