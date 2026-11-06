package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestParseArgsDefaults(t *testing.T) {
	opt, err := parseArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	if opt.file != "db.json" || opt.port != "3000" || opt.host != "localhost" {
		t.Fatalf("默认值不符: %+v", opt)
	}
	if !opt.watch || opt.id != "id" || opt.fks != "Id" {
		t.Fatalf("默认值不符: %+v", opt)
	}
}

func TestParseArgsTable(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want func(*options) string
	}{
		{"位置文件", []string{"data.json"}, func(o *options) string {
			if o.file != "data.json" {
				return "file=" + o.file
			}
			return ""
		}},
		{"端口两种写法", []string{"--port=8080", "-p", "9090"}, func(o *options) string {
			if o.port != "9090" {
				return "port=" + o.port
			}
			return ""
		}},
		{"no-watch", []string{"--no-watch"}, func(o *options) string {
			if o.watch {
				return "watch=true"
			}
			return ""
		}},
		{"watch=false", []string{"--watch=false"}, func(o *options) string {
			if o.watch {
				return "watch=true"
			}
			return ""
		}},
		{"开关们", []string{"--legacy", "--read-only", "--quiet", "--no-cors", "--no-gzip"}, func(o *options) string {
			if !o.legacy || !o.readOnly || !o.quiet || !o.noCORS || !o.noGzip {
				return "开关未生效"
			}
			return ""
		}},
		{"id 与 fks", []string{"--id", "uid", "--fks", "ID"}, func(o *options) string {
			if o.id != "uid" || o.fks != "ID" {
				return "id/fks 未生效"
			}
			return ""
		}},
		{"routes", []string{"-r", "routes.json"}, func(o *options) string {
			if o.routes != "routes.json" {
				return "routes=" + o.routes
			}
			return ""
		}},
		{"delay", []string{"--delay", "250"}, func(o *options) string {
			if o.delay != 250*time.Millisecond {
				return "delay=" + o.delay.String()
			}
			return ""
		}},
		{"host", []string{"--host", "0.0.0.0"}, func(o *options) string {
			if o.host != "0.0.0.0" {
				return "host=" + o.host
			}
			return ""
		}},
		{"help/version", []string{"--help"}, func(o *options) string {
			if !o.help {
				return "help 未生效"
			}
			return ""
		}},
		{"middlewares 带数据文件与多值", []string{"db.json", "--middlewares", "a.js", "b.js"}, func(o *options) string {
			if o.file != "db.json" {
				return "file=" + o.file
			}
			if len(o.middlewares) != 2 || o.middlewares[0] != "a.js" || o.middlewares[1] != "b.js" {
				return fmt.Sprintf("middlewares=%v", o.middlewares)
			}
			return ""
		}},
		{"middlewares 短名", []string{"db.json", "-m", "a.js"}, func(o *options) string {
			if o.file != "db.json" || len(o.middlewares) != 1 {
				return fmt.Sprintf("file=%s middlewares=%v", o.file, o.middlewares)
			}
			return ""
		}},
		{"middlewares 等号写法", []string{"db.json", "--middlewares=a.js"}, func(o *options) string {
			if o.file != "db.json" || len(o.middlewares) != 1 || o.middlewares[0] != "a.js" {
				return fmt.Sprintf("file=%s middlewares=%v", o.file, o.middlewares)
			}
			return ""
		}},
		{"snapshots 接受后忽略", []string{"db.json", "-S", "./snap"}, func(o *options) string {
			if o.file != "db.json" || o.snapshots != "./snap" {
				return fmt.Sprintf("file=%s snapshots=%q", o.file, o.snapshots)
			}
			return ""
		}},
		{"0.17 短别名们", []string{"db.json", "-H", "0.0.0.0", "-q", "-d", "50", "-i", "uid", "-c", "cfg.json", "-ro", "-v"},
			func(o *options) string {
				if o.host != "0.0.0.0" || !o.quiet || o.delay != 50*time.Millisecond ||
					o.id != "uid" || o.configFile != "cfg.json" || !o.readOnly || !o.version {
					return fmt.Sprintf("%+v", o)
				}
				return ""
			}},
		{"--nc --ng --foreignKeySuffix", []string{"db.json", "--nc", "--ng", "--foreignKeySuffix", "_id"}, func(o *options) string {
			if !o.noCORS || !o.noGzip || o.fks != "_id" {
				return fmt.Sprintf("%+v", o)
			}
			return ""
		}},
		{"短选项拼接取值", []string{"-p8080", "-iuid"}, func(o *options) string {
			if o.port != "8080" || o.id != "uid" {
				return fmt.Sprintf("port=%s id=%s", o.port, o.id)
			}
			return ""
		}},
		{"-w 生效", []string{"--no-watch", "-w"}, func(o *options) string {
			if !o.watch {
				return "watch=false"
			}
			return ""
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opt, err := parseArgs(tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if msg := tc.want(opt); msg != "" {
				t.Fatalf("%s", msg)
			}
		})
	}
}

func TestParseArgsErrors(t *testing.T) {
	for _, args := range [][]string{
		{"--bogus"},
		{"--port", "x"},
		{"--port"},
		{"a.json", "b.json"},
		{"--quiet=maybe"},
		{"--delay", "-5"},
		{"-r"},
		{"--routes", ""},
	} {
		if _, err := parseArgs(args); err == nil {
			t.Fatalf("args %v 应报错", args)
		}
	}
}

func TestUsageAndHint(t *testing.T) {
	if !strings.Contains(usage(), "--port") || !strings.Contains(usage(), "--legacy") {
		t.Fatal("usage 应列出全部选项")
	}
	if !strings.Contains(missingFileHint("db.json"), "does not exist") {
		t.Fatal("missingFileHint 应给出清晰报错")
	}
}

// TestParseArgsOptionsTerminator 验证 -- 选项终止符与 --middlewares/-m
// 只消费脚本扩展名参数。
func TestParseArgsOptionsTerminator(t *testing.T) {
	cases := []struct {
		name string
		args []string
		mws  []string
		file string
	}{
		{"-- 终止符后的位置参数为数据文件", []string{"-m", "a.js", "--", "c.json"}, []string{"a.js"}, "c.json"},
		{"-m 后续非脚本参数留给数据文件", []string{"--middlewares", "a.js", "c.json"}, []string{"a.js"}, "c.json"},
		{"-m 收多个脚本参数，数据文件在其后", []string{"-m", "a.js", "b.cjs", "x.json"}, []string{"a.js", "b.cjs"}, "x.json"},
		{"-- 后即使带连字符也按位置", []string{"--", "data.json"}, nil, "data.json"},
		{"-- 无后续取默认数据文件", []string{"--"}, nil, "db.json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opt, err := parseArgs(c.args)
			if err != nil {
				t.Fatal(err)
			}
			if len(opt.middlewares) != len(c.mws) {
				t.Fatalf("middlewares=%v，期望 %v", opt.middlewares, c.mws)
			}
			for i := range c.mws {
				if opt.middlewares[i] != c.mws[i] {
					t.Fatalf("middlewares[%d]=%q，期望 %q", i, opt.middlewares[i], c.mws[i])
				}
			}
			if opt.file != c.file {
				t.Fatalf("file=%q，期望 %q", opt.file, c.file)
			}
		})
	}
}
