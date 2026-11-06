package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyConfigFile(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	cfg := `{
  "port": "9090",
  "legacy": true,
  "read-only": true,
  "quiet": true,
  "id": "uid",
  "fks": "ID",
  "delay": 25,
  "static": ["pub", "assets"],
  "watch": false,
  "cors": false
}`
	if err := os.WriteFile(filepath.Join(dir, "json-server.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	// 命令行 --port 8080 应优先于配置文件 9090
	opt, err := parseArgs([]string{"--port", "8080"})
	if err != nil {
		t.Fatal(err)
	}
	if err := applyConfigFile(opt, io.Discard); err != nil {
		t.Fatal(err)
	}
	if opt.port != "8080" {
		t.Errorf("命令行应优先: port=%s", opt.port)
	}
	if !opt.legacy || !opt.readOnly || !opt.quiet {
		t.Errorf("布尔配置未生效: %+v", opt)
	}
	if opt.id != "uid" || opt.fks != "ID" {
		t.Errorf("id/fks 配置未生效: %+v", opt)
	}
	if opt.delay.Milliseconds() != 25 {
		t.Errorf("delay 配置: %v", opt.delay)
	}
	if len(opt.static) != 2 || opt.static[0] != "pub" {
		t.Errorf("static 配置: %v", opt.static)
	}
	if opt.watch {
		t.Error("watch:false 应关闭监听")
	}
	if !opt.noCORS {
		t.Error("cors:false 应等价 --no-cors")
	}
}

func TestConfigFileErrors(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer func() { _ = os.Chdir(old) }()
	_ = os.Chdir(dir)

	// 无配置文件：默认路径不存在时静默跳过
	opt, _ := parseArgs(nil)
	if err := applyConfigFile(opt, io.Discard); err != nil {
		t.Errorf("默认路径缺失应跳过: %v", err)
	}
	// 显式 --config 指向不存在的文件应报错
	opt, _ = parseArgs([]string{"--config", "missing.json"})
	if err := applyConfigFile(opt, io.Discard); err == nil {
		t.Error("显式 --config 缺失应报错")
	}

	// 未知配置项：只警告，不拒绝启动
	warnFile := "json-server.json"
	if err := os.WriteFile(warnFile, []byte(`{"bogus":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	opt, _ = parseArgs(nil)
	var buf warnCollector
	if err := applyConfigFile(opt, &buf); err != nil {
		t.Errorf("未知配置项应只警告: %v", err)
	}
	if !strings.Contains(buf.String(), "bogus") {
		t.Error("未知配置项应有警告输出")
	}
	// 非法 JSON
	if err := os.WriteFile("json-server.json", []byte(`{`), 0o644); err != nil {
		t.Fatal(err)
	}
	opt, _ = parseArgs(nil)
	if err := applyConfigFile(opt, io.Discard); err == nil {
		t.Error("非法 JSON 应报错")
	}
}

// warnCollector 收集 applyConfigFile 的警告输出。
type warnCollector struct{ b strings.Builder }

func (w *warnCollector) Write(p []byte) (int, error) { return w.b.Write(p) }
func (w *warnCollector) String() string              { return w.b.String() }

// TestConfigCamelCaseKeys 验证 0.17 风格的驼峰键名被接受。
func TestConfigCamelCaseKeys(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer func() { _ = os.Chdir(old) }()
	_ = os.Chdir(dir)

	cfg := `{
  "readOnly": true,
  "noCors": true,
  "noGzip": true,
  "foreignKeySuffix": "_id",
  "snapshots": "./snap"
}`
	if err := os.WriteFile("json-server.json", []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	opt, _ := parseArgs(nil)
	if err := applyConfigFile(opt, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !opt.readOnly || !opt.noCORS || !opt.noGzip {
		t.Errorf("驼峰布尔键未生效: %+v", opt)
	}
	if opt.fks != "_id" {
		t.Errorf("foreignKeySuffix 未生效: %q", opt.fks)
	}
	if opt.snapshots != "./snap" {
		t.Errorf("snapshots 未生效: %q", opt.snapshots)
	}
}
