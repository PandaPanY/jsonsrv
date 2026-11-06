package main

// 配置文件支持：从 json-server.json 读取与命令行同名的选项，
// 命令行显式给出的选项优先。--config 可改路径（v0.17 约定文件为当前目录
// 的 json-server.json）。同时接受 0.17 yargs 风格的驼峰写法
// （readOnly、noCors、noGzip、foreignKeySuffix、snapshots）。
// 未知配置项只警告、不拒绝启动。

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/PandaPanY/jsonsrv/internal/jsonx"
)

// canonicalConfigKey 归一化配置键名：小写、去连字符，别名并到规范名。
// 返回 "" 表示未知配置项。
func canonicalConfigKey(key string) string {
	k := strings.ToLower(strings.ReplaceAll(key, "-", ""))
	switch k {
	case "port", "host", "id", "fks", "routes", "config", "file", "db", "static",
		"delay", "watch", "quiet", "legacy", "readonly", "middlewares",
		"cors", "gzip", "nocors", "nogzip", "snapshots":
		return k
	case "foreignkeysuffix":
		return "fks"
	}
	return ""
}

// applyConfigFile 读取配置文件并填充未在命令行显式设置的选项。
// 文件不存在且未显式指定 --config 时静默跳过；显式指定却找不到时报错。
// 未知配置项向 stderr 打警告后忽略。
func applyConfigFile(opt *options, stderr io.Writer) error {
	explicit := opt.wasSet("config")
	path := opt.configFile
	if path == "" {
		path = "json-server.json"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && !explicit {
			return nil
		}
		return fmt.Errorf("cannot read config file %s: %w", path, err)
	}
	v, err := jsonx.Parse(data)
	if err != nil {
		return fmt.Errorf("cannot parse config file %s: %w", path, err)
	}
	obj, ok := jsonx.AsObject(v)
	if !ok {
		return fmt.Errorf("config file %s must contain a JSON object", path)
	}
	var errs []string
	for _, key := range obj.Keys() {
		canonical := canonicalConfigKey(key)
		if canonical == "" {
			fmt.Fprintf(stderr, "warning: config file %s: ignoring unknown option %q\n", path, key)
			continue
		}
		val, _ := obj.Get(key)
		if err := applyConfigValue(opt, canonical, val); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("config file %s: %s", path, strings.Join(errs, "; "))
	}
	return nil
}

// applyConfigValue 应用单个配置项（键为 canonicalConfigKey 的规范名）；
// 命令行已设置的跳过。
func applyConfigValue(opt *options, key string, val jsonx.Value) error {
	if opt.wasSet(key) {
		return nil
	}
	switch key {
	case "port":
		s, ok := jsonx.ToString(val)
		if !ok {
			return fmt.Errorf("%s must be a port number", key)
		}
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("%s requires a port number from 1 to 65535, got %q", key, s)
		}
		opt.port = s
	case "host", "id", "fks", "routes", "config", "snapshots":
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("%s must be a string", key)
		}
		switch key {
		case "host":
			opt.host = s
		case "id":
			opt.id = s
		case "fks":
			opt.fks = s
		case "routes":
			opt.routes = s
		case "snapshots":
			opt.snapshots = s
		}
	case "file", "db":
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("%s must be a string", key)
		}
		opt.file = s
	case "static":
		switch tv := val.(type) {
		case string:
			opt.static = append(opt.static, tv)
		case []jsonx.Value:
			for _, e := range tv {
				s, ok := e.(string)
				if !ok {
					return fmt.Errorf("static array items must be strings")
				}
				opt.static = append(opt.static, s)
			}
		default:
			return fmt.Errorf("static must be a string or an array of strings")
		}
	case "delay":
		s, ok := jsonx.ToString(val)
		if !ok {
			return fmt.Errorf("%s must be a number of milliseconds", key)
		}
		ms, err := strconv.Atoi(s)
		if err != nil || ms < 0 {
			return fmt.Errorf("%s requires a number of milliseconds, got %q", key, s)
		}
		opt.delay = time.Duration(ms) * time.Millisecond
	case "watch", "quiet", "legacy", "readonly":
		b, ok := val.(bool)
		if !ok {
			return fmt.Errorf("%s must be true or false", key)
		}
		switch key {
		case "watch":
			opt.watch = b
		case "quiet":
			opt.quiet = b
		case "legacy":
			opt.legacy = b
		case "readonly":
			opt.readOnly = b
		}
	case "middlewares":
		// 接受 true / 字符串 / 字符串数组；JS 中间件不支持，启动时提示。
		switch tv := val.(type) {
		case bool:
			if tv {
				opt.middlewares = append(opt.middlewares, "config file")
			}
		case string:
			opt.middlewares = append(opt.middlewares, tv)
		case []jsonx.Value:
			for _, e := range tv {
				s, ok := e.(string)
				if !ok {
					return fmt.Errorf("middlewares array items must be strings")
				}
				opt.middlewares = append(opt.middlewares, s)
			}
		default:
			return fmt.Errorf("middlewares must be a boolean, a string or an array of strings")
		}
	case "cors", "gzip":
		// 配置里 cors/gzip:false 等价于 --no-cors / --no-gzip
		b, ok := val.(bool)
		if !ok {
			return fmt.Errorf("%s must be true or false", key)
		}
		if key == "cors" {
			opt.noCORS = !b
		} else {
			opt.noGzip = !b
		}
	case "nocors":
		if opt.wasSet("cors") {
			return nil // 命令行 --no-cors/--nc 已决定，配置文件不再覆盖
		}
		b, ok := val.(bool)
		if !ok {
			return fmt.Errorf("%s must be true or false", key)
		}
		opt.noCORS = b
	case "nogzip":
		if opt.wasSet("gzip") {
			return nil
		}
		b, ok := val.(bool)
		if !ok {
			return fmt.Errorf("%s must be true or false", key)
		}
		opt.noGzip = b
	}
	return nil
}
