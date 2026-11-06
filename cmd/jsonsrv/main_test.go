package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRunHelpVersionAndErrors(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "stdout.log")
	errPath := filepath.Join(dir, "stderr.log")
	outFile, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	errFile, err := os.Create(errPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = outFile.Close()
		_ = errFile.Close()
	}()

	if err := run([]string{"--help"}, outFile, errFile); err != nil {
		t.Fatalf("--help 应成功: %v", err)
	}
	if err := run([]string{"--version"}, outFile, errFile); err != nil {
		t.Fatalf("--version 应成功: %v", err)
	}

	var exit *exitError
	err = run([]string{filepath.Join(dir, "不存在.json")}, outFile, errFile)
	if !errors.As(err, &exit) || exit.code != 1 {
		t.Fatalf("缺少数据文件应退出码 1: %v", err)
	}
	err = run([]string{"--bogus"}, outFile, errFile)
	if !errors.As(err, &exit) || exit.code != 2 {
		t.Fatalf("未知选项应退出码 2: %v", err)
	}

	out, _ := os.ReadFile(outPath)
	if len(out) == 0 {
		t.Fatal("help/version 输出不应为空")
	}
}

func TestParseArgsKeepsPositionalFile(t *testing.T) {
	opt, err := parseArgs([]string{"my.json", "--port", "3001"})
	if err != nil {
		t.Fatal(err)
	}
	if opt.file != "my.json" || opt.port != "3001" {
		t.Fatalf("解析错误: %+v", opt)
	}
}
