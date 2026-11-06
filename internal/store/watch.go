package store

import (
	"context"
	"time"
)

// Watcher 以轮询方式监听数据文件变化（跨平台、无第三方依赖）。
// 间隔内检测到文件签名变化即调用 store.Reload；解析失败时保留旧数据并通知错误。
type Watcher struct {
	st       *Store
	interval time.Duration
}

// NewWatcher 创建文件监听器。interval 为轮询间隔。
func NewWatcher(st *Store, interval time.Duration) *Watcher {
	return &Watcher{st: st, interval: interval}
}

// Start 在后台协程中轮询，直到 ctx 取消。
// onChange 在成功重载后回调（用于刷新路由表）。
// 错误与恢复的通知语义：进入失败状态时回调 onError 恰好一次（文件被删
// 或解析失败不再逐秒重复），期间保持静默；恢复成功时若曾在失败状态则回调
// onRecovered 恰好一次，之后回到正常轮询。回调应写入 stderr 之类不受
// --quiet 抑制的通道（由调用方保证）。
func (w *Watcher) Start(ctx context.Context, onChange func(), onError func(error), onRecovered func()) {
	go func() {
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()
		inError := false
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				changed, err := w.st.Reload()
				if err != nil {
					if !inError && onError != nil {
						onError(err) // 只在进入失败状态时报告一次
					}
					inError = true
					continue
				}
				if inError {
					inError = false
					if onRecovered != nil {
						onRecovered() // 恢复后提示一次
					}
				}
				if changed && onChange != nil {
					onChange()
				}
			}
		}
	}()
}
