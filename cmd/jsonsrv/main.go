// Command jsonsrv 是一个零依赖的 REST 模拟服务器，
// 用法见 --help。目标是兼容 json-server 的 v1 与 v0.17。
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/PandaPanY/jsonsrv/internal/httpapi"
	"github.com/PandaPanY/jsonsrv/internal/jsonx"
	"github.com/PandaPanY/jsonsrv/internal/routes"
	"github.com/PandaPanY/jsonsrv/internal/store"
)

// version 是构建版本号，发布构建可通过 -ldflags 注入。
var version = "0.3.2"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		var exit *exitError
		if errors.As(err, &exit) {
			if exit.message != "" {
				fmt.Fprintln(os.Stderr, exit.message)
			}
			os.Exit(exit.code)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// exitError 携带退出码，消息写入 stderr。
type exitError struct {
	code    int
	message string
}

func (e *exitError) Error() string { return e.message }

func run(args []string, stdout, stderr *os.File) error {
	opt, err := parseArgs(args)
	if err != nil {
		return &exitError{code: 2, message: "invalid arguments: " + err.Error() + "\nRun jsonsrv --help for usage"}
	}
	if err := applyConfigFile(opt, stderr); err != nil {
		return &exitError{code: 2, message: err.Error()}
	}
	if opt.help {
		fmt.Fprint(stdout, usage())
		return nil
	}
	if opt.version {
		fmt.Fprintf(stdout, "jsonsrv %s (compatible with json-server v1 / v0.17)\n", version)
		return nil
	}

	if _, statErr := os.Stat(opt.file); statErr != nil {
		return &exitError{code: 1, message: missingFileHint(opt.file)}
	}
	st, err := store.Open(opt.file)
	if err != nil {
		return err
	}
	for _, note := range st.LoadNotes() {
		fmt.Fprintln(stderr, "warning:", note)
	}

	var reqLog *log.Logger
	if !opt.quiet {
		reqLog = log.New(stdout, "", 0)
	}
	if len(opt.middlewares) > 0 {
		fmt.Fprintf(stderr, "note: JS middlewares (--middlewares %s) are not supported. "+
			"Alternatives: serve static files with --static, or handle the logic in a reverse proxy.\n",
			strings.Join(opt.middlewares, " "))
	}
	if opt.snapshots != "" {
		fmt.Fprintf(stderr, "note: JS snapshots (--snapshots %s) are not supported; reads and writes use the JSON data file only.\n", opt.snapshots)
	}

	var routeRules []routes.Rule
	if opt.routes != "" {
		data, err := os.ReadFile(opt.routes)
		if err != nil {
			return &exitError{code: 1, message: "cannot read routes file: " + err.Error()}
		}
		routeRules, err = routes.LoadRules(data)
		if err != nil {
			return &exitError{code: 1, message: "invalid routes file: " + err.Error()}
		}
	}

	app := httpapi.New(st, httpapi.Options{
		IDName:     opt.id,
		FKS:        opt.fks,
		ReadOnly:   opt.readOnly,
		Legacy:     opt.legacy,
		Delay:      opt.delay,
		Log:        reqLog,
		Routes:     routeRules,
		StaticDirs: opt.static,
		NoCORS:     opt.noCORS,
		NoGzip:     opt.noGzip,
	})

	addr := net.JoinHostPort(opt.host, opt.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		var oe *net.OpError
		if errors.As(err, &oe) {
			return fmt.Errorf("cannot listen on %s: the port may be in use; choose another with --port (original error: %v)", addr, err)
		}
		return fmt.Errorf("cannot listen on %s: %w", addr, err)
	}

	// 端口监听成功后才清理崩溃残留的临时文件——端口占用启动失败时
	// 另一实例仍在运行，不应触碰它的临时文件（同文件误启动第二个实例也不会
	// 删到第一个实例正在写的临时文件）。
	if note := st.CleanupStaleTemps(); note != "" {
		fmt.Fprintln(stderr, "warning:", note)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if opt.watch {
		// 加载失败只在首次报告（去重），stderr 输出、--quiet 也不隐藏；
		// 文件恢复后提示一次（去重）。见 store.Watcher。
		store.NewWatcher(st, time.Second).Start(ctx, nil,
			func(err error) {
				fmt.Fprintf(stderr, "warning: cannot reload the data file; keeping the old data, and repeats of this error will not be reported: %v\n", err)
			},
			func() {
				fmt.Fprintln(stderr, "data file recovered; watching for changes")
			})
	}

	srv := &http.Server{Handler: app}
	go func() {
		if serr := srv.Serve(ln); serr != nil && !errors.Is(serr, http.ErrServerClosed) {
			fmt.Fprintln(stderr, "HTTP server error:", serr)
		}
	}()

	if !opt.quiet {
		printStartup(stdout, opt, st)
	}

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	return nil
}

// printStartup 打印监听地址与资源列表。
func printStartup(stdout *os.File, opt *options, st *store.Store) {
	fmt.Fprintf(stdout, "jsonsrv %s (compatible with json-server v1 / v0.17)\n", version)
	fmt.Fprintf(stdout, "Listening on: http://%s\n", net.JoinHostPort(opt.host, opt.port))
	watchNote := ""
	if opt.watch {
		watchNote = " (watching for changes)"
	}
	fmt.Fprintf(stdout, "Data file: %s%s\n", opt.file, watchNote)
	snap := st.Snapshot()
	fmt.Fprintf(stdout, "Resources:")
	for _, key := range snap.Keys() {
		v, _ := snap.Get(key)
		switch tv := v.(type) {
		case []jsonx.Value:
			fmt.Fprintf(stdout, " %s(%d)", key, len(tv))
		case *jsonx.Object:
			fmt.Fprintf(stdout, " %s(singleton)", key)
		}
	}
	fmt.Fprintln(stdout)
}

// missingFileHint 给出清晰的数据文件报错与示例。
func missingFileHint(file string) string {
	return fmt.Sprintf(`Data file %s does not exist.

Create it first, in this format (each top-level key is a resource: an array is a
collection, an object is a singleton):

  %s
  {
    "posts": [
      { "id": 1, "title": "jsonsrv", "author": "typicode" }
    ],
    "comments": [
      { "id": 1, "body": "a comment about post 1", "postId": 1 }
    ],
    "profile": {
      "name": "typicode"
    }
  }`, file, file)
}
