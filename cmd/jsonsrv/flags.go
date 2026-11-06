package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// options 是命令行与配置文件合并后的最终配置。
type options struct {
	file        string        // 数据文件路径，默认 db.json
	port        string        // 监听端口，默认 3000
	host        string        // 监听地址，默认 localhost
	watch       bool          // 监听数据文件变化，默认开
	quiet       bool          // 关闭请求日志
	legacy      bool          // v0.17 冲突行为（--legacy）
	readOnly    bool          // 只读模式
	id          string        // id 字段名，默认 id
	fks         string        // 外键后缀，默认 Id
	routes      string        // --routes 重写文件，空则不启用
	static      []string      // --static 静态目录（可重复）
	configFile  string        // --config 配置文件（默认当前目录 json-server.json）
	delay       time.Duration // 响应延迟
	noCORS      bool          // 关闭 CORS
	noGzip      bool          // 关闭 gzip
	help        bool
	version     bool
	middlewares []string        // --middlewares/-m 取值（接受但忽略，JS 中间件不支持）
	snapshots   string          // --snapshots/-S 取值（接受但忽略，快照不支持）
	set         map[string]bool // 命令行显式设置过的键（配置文件不会覆盖）
}

// defaultOptions 返回与 json-server 一致的默认值。
func defaultOptions() *options {
	return &options{
		file:  "db.json",
		port:  "3000",
		host:  "localhost",
		watch: true,
		id:    "id",
		fks:   "Id",
		set:   map[string]bool{},
	}
}

// markSet 记录某个键在命令行中显式出现过。
func (o *options) markSet(key string) {
	if o.set == nil {
		o.set = map[string]bool{}
	}
	o.set[normFlagName(key)] = true
}

// wasSet 报告命令行是否显式设置过该键。
func (o *options) wasSet(key string) bool {
	return o.set[normFlagName(key)]
}

// valueFlagChars 是"取值型"短选项字符，支持 -p3000 这类拼接写法。
const valueFlagChars = "pHrsSdicm"

// canonicalFlag 把选项名映射到内部规范名。
// known=false 表示不是选项（位置参数）；name="" 且 known=true 表示无法识别。
// 单短横线形式区分大小写（-H/-h、-S/-s），双横线长名不区分。
func canonicalFlag(key string) (name string, known bool) {
	if key == "" || key[0] != '-' || key == "-" || key == "--" {
		return "", false
	}
	if !strings.HasPrefix(key, "--") {
		switch key {
		case "-p":
			return "port", true
		case "-H":
			return "host", true
		case "-w":
			return "watch", true
		case "-r":
			return "routes", true
		case "-m":
			return "middlewares", true
		case "-s":
			return "static", true
		case "-S":
			return "snapshots", true
		case "-d":
			return "delay", true
		case "-i":
			return "id", true
		case "-q":
			return "quiet", true
		case "-c":
			return "config", true
		case "-h":
			return "help", true
		case "-v":
			return "version", true
		case "-ro":
			return "read-only", true
		case "-nc":
			return "no-cors", true
		case "-ng":
			return "no-gzip", true
		}
		return "", true
	}
	switch strings.ToLower(key) {
	case "--port":
		return "port", true
	case "--host":
		return "host", true
	case "--watch":
		return "watch", true
	case "--no-watch":
		return "no-watch", true
	case "--routes":
		return "routes", true
	case "--middlewares":
		return "middlewares", true
	case "--static":
		return "static", true
	case "--snapshots":
		return "snapshots", true
	case "--delay":
		return "delay", true
	case "--id":
		return "id", true
	case "--fks", "--foreignkeysuffix":
		return "fks", true
	case "--quiet":
		return "quiet", true
	case "--config":
		return "config", true
	case "--help":
		return "help", true
	case "--version":
		return "version", true
	case "--read-only", "--readonly", "--ro":
		return "read-only", true
	case "--no-cors", "--nc":
		return "no-cors", true
	case "--no-gzip", "--ng":
		return "no-gzip", true
	case "--legacy":
		return "legacy", true
	}
	return "", true
}

// normFlagName 归一化选项名：去前导 -，别名映射到与配置文件一致的规范名。
// no-cors/no-gzip 归到 cors/gzip，与配置文件的开关名对齐。
func normFlagName(key string) string {
	key = strings.TrimLeft(key, "-")
	switch key {
	case "p":
		return "port"
	case "H":
		return "host"
	case "w":
		return "watch"
	case "r":
		return "routes"
	case "s":
		return "static"
	case "m":
		return "middlewares"
	case "S":
		return "snapshots"
	case "d":
		return "delay"
	case "i":
		return "id"
	case "q":
		return "quiet"
	case "c":
		return "config"
	case "h":
		return "help"
	case "v":
		return "version"
	case "ro":
		return "read-only"
	case "nc", "no-cors":
		return "cors"
	case "ng", "no-gzip":
		return "gzip"
	case "no-watch":
		return "watch"
	case "fks":
		return "fks"
	}
	if strings.ToLower(key) == "foreignkeysuffix" {
		return "fks"
	}
	return key
}

// parseArgs 解析命令行参数，支持 v0.17 的全部短选项别名。
// 布尔开关支持 --flag 与 --no-flag / --flag=false 两种写法。
// isScriptArg 判断参数是否 JS/TS 脚本路径（--middlewares/-m 只消费这类参数，
// 其余参数按数据文件或选项处理）。
func isScriptArg(arg string) bool {
	if arg == "" || arg[0] == '-' {
		return false
	}
	lower := strings.ToLower(arg)
	for _, ext := range []string{".js", ".cjs", ".mjs", ".ts"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func parseArgs(args []string) (*options, error) {
	opt := defaultOptions()
	positional := []string{}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			// 选项终止符：-- 之后全部按位置参数处理（如 jsonsrv -m a.js -- c.json）。
			positional = append(positional, args[i+1:]...)
			break
		}
		key, val, hasVal := splitFlag(arg)
		name, known := canonicalFlag(key)
		if !known {
			positional = append(positional, arg)
			continue
		}
		if name == "" && !strings.HasPrefix(key, "--") && len(key) > 2 &&
			strings.IndexByte(valueFlagChars, key[1]) >= 0 {
			// 拼接式短选项取值（-p3000、-H0.0.0.0、-S./snap 等），仅限取值型选项；
			// 精确匹配的选项名优先（如 -ro 是只读开关，不是 -r 的取值）。
			if shortName, ok := canonicalFlag(key[:2]); ok && shortName != "" {
				key, val, hasVal = key[:2], key[2:], true
				name = shortName
			}
		}
		if name == "" {
			return nil, fmt.Errorf("unknown option %q", arg)
		}
		opt.markSet(key) // 记录命令行显式设置，配置文件不得覆盖
		switch name {
		case "port":
			v, err := next(args, i, val, hasVal, key)
			if err != nil {
				return nil, err
			}
			i = v.i
			n, err := strconv.Atoi(v.s)
			if err != nil || n < 1 || n > 65535 {
				return nil, fmt.Errorf("%s requires a port number from 1 to 65535, got %q", key, v.s)
			}
			opt.port = v.s
		case "host":
			v, err := next(args, i, val, hasVal, key)
			if err != nil {
				return nil, err
			}
			i = v.i
			opt.host = v.s
		case "id":
			v, err := next(args, i, val, hasVal, key)
			if err != nil {
				return nil, err
			}
			i = v.i
			if v.s == "" {
				return nil, fmt.Errorf("%s requires a field name", key)
			}
			opt.id = v.s
		case "fks":
			v, err := next(args, i, val, hasVal, key)
			if err != nil {
				return nil, err
			}
			i = v.i
			opt.fks = v.s
		case "routes":
			v, err := next(args, i, val, hasVal, key)
			if err != nil {
				return nil, err
			}
			i = v.i
			if v.s == "" {
				return nil, fmt.Errorf("%s requires a file path", key)
			}
			opt.routes = v.s
		case "static":
			v, err := next(args, i, val, hasVal, key)
			if err != nil {
				return nil, err
			}
			i = v.i
			if v.s == "" {
				return nil, fmt.Errorf("%s requires a directory path", key)
			}
			opt.static = append(opt.static, v.s)
		case "config":
			v, err := next(args, i, val, hasVal, key)
			if err != nil {
				return nil, err
			}
			i = v.i
			if v.s == "" {
				return nil, fmt.Errorf("%s requires a file path", key)
			}
			opt.configFile = v.s
		case "delay":
			v, err := next(args, i, val, hasVal, key)
			if err != nil {
				return nil, err
			}
			i = v.i
			ms, err := strconv.Atoi(v.s)
			if err != nil || ms < 0 {
				return nil, fmt.Errorf("%s requires a number of milliseconds, got %q", key, v.s)
			}
			opt.delay = time.Duration(ms) * time.Millisecond
		case "snapshots":
			// 接受取值并提示不支持（JS 快照机制无对应实现），继续启动。
			v, err := next(args, i, val, hasVal, key)
			if err != nil {
				return nil, err
			}
			i = v.i
			opt.snapshots = v.s
		case "middlewares":
			// 与 0.17 一致：接受多个取值直到下一个选项为止，但 JS 中间件不支持。
			if hasVal {
				opt.middlewares = append(opt.middlewares, val)
			} else {
				seen := false
				// 只消费 .js/.cjs/.mjs/.ts 结尾的参数（0.17 yargs 按类型收列，数据文件
				// 等非脚本参数留给位置）；遇到其他参数（含数据文件、--）停止。
				for i+1 < len(args) && isScriptArg(args[i+1]) {
					opt.middlewares = append(opt.middlewares, args[i+1])
					i++
					seen = true
				}
				if !seen {
					return nil, fmt.Errorf("option %s requires a value", key)
				}
			}
		case "watch", "read-only", "no-cors", "no-gzip", "quiet", "legacy", "help", "version":
			b, err := parseBool(val, hasVal, key)
			if err != nil {
				return nil, err
			}
			switch name {
			case "watch":
				opt.watch = b
			case "read-only":
				opt.readOnly = b
			case "no-cors":
				opt.noCORS = b
			case "no-gzip":
				opt.noGzip = b
			case "quiet":
				opt.quiet = b
			case "legacy":
				opt.legacy = b
			case "help":
				opt.help = b
			case "version":
				opt.version = b
			}
		case "no-watch":
			b, err := parseBool(val, hasVal, key)
			if err != nil {
				return nil, err
			}
			opt.watch = !b
		}
	}
	if len(positional) > 0 {
		opt.markSet("file")
		opt.file = positional[0]
		if len(positional) > 1 {
			return nil, fmt.Errorf("only one data file is allowed, got %v", positional)
		}
	}
	return opt, nil
}

type nextVal struct {
	s string
	i int
}

// next 取选项的值：--key=value 或 --key value。
func next(args []string, i int, val string, hasVal bool, key string) (nextVal, error) {
	if hasVal {
		return nextVal{s: val, i: i}, nil
	}
	if i+1 >= len(args) {
		return nextVal{}, fmt.Errorf("option %s requires a value", key)
	}
	return nextVal{s: args[i+1], i: i + 1}, nil
}

// splitFlag 拆出选项名与可选的 =value。
func splitFlag(arg string) (key, val string, hasVal bool) {
	for i := 0; i < len(arg); i++ {
		if arg[i] == '=' {
			return arg[:i], arg[i+1:], true
		}
	}
	return arg, "", false
}

// parseBool 解析布尔选项的显式取值（--flag 或 --flag=true/false）。
func parseBool(val string, hasVal bool, key string) (bool, error) {
	if !hasVal {
		return true, nil
	}
	switch val {
	case "true", "1", "":
		return true, nil
	case "false", "0":
		return false, nil
	}
	return false, fmt.Errorf("option %s accepts only a boolean, got %q", key, val)
}

// usage 返回帮助文本。
func usage() string {
	return `Usage: jsonsrv [data-file] [options]

Uses db.json in the current directory when no file is given. Follows json-server
v1 syntax; use --legacy for v0.17 behaviour that conflicts with v1 (numeric
auto-increment ids, _page returning arrays).

Options:
  -p, --port <port>       Port to listen on (default 3000)
  -H, --host <address>    Address to listen on (default localhost)
  -w, --watch / --no-watch
                          Reload when the data file changes (default on)
  -i, --id <field>        Name of the id field (default id)
  --fks, --foreignKeySuffix <suffix>
                          Foreign key suffix (default Id)
  -r, --routes <file>     Path rewrite file in v0.17 format, such as "/api/*": "/$1"
  --legacy                Follow v0.17 where it conflicts with v1
  --read-only, --ro       Read-only mode (write requests return 403)
  -d, --delay <ms>        Delay every response
  -s, --static <dir>      Static file directory; can be repeated (default ./public if present)
  -c, --config <file>     Config file (default json-server.json in the current directory;
                          command-line options take priority)
  --no-cors, --nc         Disable CORS
  --no-gzip, --ng         Disable gzip
  -q, --quiet             Do not log requests
  -m, --middlewares <file...>
                          JS middlewares (not supported; prints a notice and starts anyway)
  -S, --snapshots <dir>   JS snapshot directory (not supported; prints a notice and starts anyway)
  -h, --help              Show this help
  -v, --version           Show the version

Examples:
  jsonsrv db.json
  jsonsrv --legacy --port 8080
`
}
