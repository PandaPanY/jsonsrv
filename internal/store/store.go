// Package store 负责数据文件的加载、并发安全的访问与原子保存。
//
// 设计要点：
//   - 读请求只持 RWMutex 读锁取整库快照；Update/Reload 由写者锁串行化，
//     序列化、落盘与重读解析都在读请求等待的锁之外；
//   - 保存采用"临时文件 + 改名"，进程被杀也不会损坏原文件；
//   - 文件被外部修改后可通过 Reload 重新加载，解析失败时保留旧数据。
package store

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/PandaPanY/jsonsrv/internal/json5"
	"github.com/PandaPanY/jsonsrv/internal/jsonx"
)

// Store 表示一个内存中的 JSON 数据库，绑定一个数据文件路径。
type Store struct {
	mu    sync.RWMutex // 仅保护 doc/sig/notes 的换入与读取
	path  string
	wmu   sync.Mutex // 写者锁：串行 Update/Reload 的读取-修改-落盘全过程，不阻塞读
	doc   *jsonx.Object
	sig   fileSig  // 最近一次成功加载的文件签名
	notes []string // 最近一次加载产生的转换说明（如 Infinity/NaN 降级）
}

// Open 读取并解析数据文件。文件不存在、格式错误或顶层不是对象时返回错误。
// 清理崩溃残留不在 Open 做——清理必须推迟到主程序端口监听成功之后（见
// (*Store).CleanupStaleTemps 与 cmd/jsonsrv/main.go），避免端口占用启动失败时
// 误删另一实例正在写的临时文件。
func Open(path string) (*Store, error) {
	doc, sig, notes, err := load(path)
	if err != nil {
		return nil, err
	}
	return &Store{path: path, doc: doc, sig: sig, notes: notes}, nil
}

// LoadNotes 返回最近一次加载产生的转换说明（调用方打印一次警告）。
func (s *Store) LoadNotes() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.notes
}

// Path 返回数据文件路径。
func (s *Store) Path() string { return s.path }

// Snapshot 返回整库的只读快照（共享结构，调用方不得修改）。
// 更新采用写时复制，因此快照之间互不影响且无数据竞争。
func (s *Store) Snapshot() *jsonx.Object {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.doc
}

// savePauseHook 供测试挂用：Update 落盘前回调，模拟慢盘（见
// TestSnapshotDuringPausedSave）；生产路径仅为一次 nil 判断。
var savePauseHook func()

// Update 以写时复制执行修改：浅拷贝当前库顶层（嵌套值共享引用）交给 fn，
// fn 只允许替换顶层键值（Set/Delete），不得就地修改共享的嵌套值——
// 各 handler 都按“构造新值再 Set”的方式变更，读方持有的旧快照因此不受影响。
// fn 返回 nil 后流式序列化副本并原子保存，成功后替换内存数据。
// 全程的拷贝、fn、序列化与落盘都在写者锁（wmu）内、s.mu 之外进行：
// 写盘期间 Snapshot 只需读锁即可立刻拿到旧数据，s.mu 只在换入 s.doc/s.sig 时短暂持有。
// fn 返回错误时取消保存，内存保持不变。
// Update 返回时新数据已换入，之后的读立刻可见本次写入。
//
// 只复制顶层映射，序列化直接流式写入临时文件：每次深拷贝整库再拼完整字节的话，
// 大库下单次 POST 的 CPU 与内存都会和全库大小成正比。
func (s *Store) Update(fn func(db *jsonx.Object) error) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.mu.RLock()
	cur := s.doc
	s.mu.RUnlock()
	newDoc := cur.ShallowClone()
	if err := fn(newDoc); err != nil {
		return err
	}
	if savePauseHook != nil {
		savePauseHook()
	}
	if err := writeFileAtomicFunc(s.path, func(w io.Writer) error {
		return jsonx.MarshalIndent(w, newDoc)
	}); err != nil {
		return fmt.Errorf("save failed: %w", err)
	}
	sig, serr := statSig(s.path)
	s.mu.Lock()
	s.doc = newDoc
	if serr == nil {
		s.sig = sig
	}
	s.mu.Unlock()
	return nil
}

// Reload 检查文件是否被外部修改；是则重新加载。
// 返回值 changed 表示是否成功替换了内存数据；解析失败时保留旧数据并返回错误，
// 同时更新签名，避免对同一份坏内容重复报错。
//
// 整个检查-读取-换入过程处于写者锁（wmu）内：与 Update 的“保存+换入”处于同一
// 临界区，避免读到换入前的旧文件内容又把内存回滚、丢掉并发更新
// （否则并发 POST 会丢失更新：全部返回 201，落盘的记录却变少）。
// 文件 stat、读取与解析都在 s.mu 之外进行，换入时只短暂持有 s.mu。
func (s *Store) Reload() (changed bool, err error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.mu.RLock()
	oldSig := s.sig
	s.mu.RUnlock()
	sig, serr := statSig(s.path)
	if serr != nil {
		return false, serr
	}
	if sig == oldSig {
		return false, nil
	}
	doc, lsig, notes, err := load(s.path)
	if err != nil {
		s.mu.Lock()
		s.sig = sig
		s.mu.Unlock()
		return false, err
	}
	s.mu.Lock()
	s.doc, s.sig, s.notes = doc, lsig, notes
	s.mu.Unlock()
	return true, nil
}

// CleanupStaleTemps 删除数据文件同目录内属于本数据文件的崩溃残留临时文件：
// 严格匹配 ".jsonsrv-<数据文件名>-<纯数字>.tmp"，与 writeFileAtomicFunc 的命名
// 对应（临时文件名即 .jsonsrv-<数据文件名>-<随机数字>.tmp）。不能按前缀匹配，
// 否则会把 db.json-backup.json 这类其他数据文件的临时文件（.jsonsrv-db.json-backup.json-
// 222.tmp）一并删掉。调用方应在端口监听成功后调用；非常规文件与删除失败的文件跳过。
// 返回人类可读的清理说明（无残留时返回空串）。
func (s *Store) CleanupStaleTemps() string {
	path := s.Path()
	target := path
	if real, err := filepath.EvalSymlinks(path); err == nil {
		target = real
	}
	dir := filepath.Dir(target)
	re, err := regexp.Compile("^\\.jsonsrv-" + regexp.QuoteMeta(filepath.Base(target)) + "-\\d+\\.tmp$")
	if err != nil {
		return ""
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var names []string
	deleted := 0
	for _, e := range ents {
		name := e.Name()
		if !re.MatchString(name) {
			continue
		}
		fi, err := e.Info()
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			continue
		}
		deleted++
		if len(names) < 4 {
			names = append(names, name)
		}
	}
	if deleted == 0 {
		return ""
	}
	more := ""
	if deleted > len(names) {
		more = ", ..."
	}
	return fmt.Sprintf("removed %d temporary file(s) left by a previous crash: %s%s", deleted, strings.Join(names, ", "), more)
}

// load 读取文件并按扩展名（.json5 宽松解析）转换为 *Object。
func load(path string) (*jsonx.Object, fileSig, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fileSig{}, nil, err
	}
	sig, serr := statSig(path)
	if serr != nil {
		return nil, fileSig{}, nil, serr
	}
	doc, notes, err := parseDataWithNotes(path, data)
	if err != nil {
		return nil, fileSig{}, nil, err
	}
	if filepath.Ext(path) == ".json5" {
		// 实测 v1 写回同样丢失注释与原始格式，此处提示一次即可（见有意差异）。
		notes = append(notes, "JSON5 data files are written back as standard JSON; comments and relaxed syntax are not kept (v1 does the same)")
	}
	return doc, sig, notes, nil
}

// ParseData 解析数据文件内容；.json5 扩展名启用 JSON5 宽松语法。
// 顶层必须是 JSON 对象。错误信息带文件路径，便于给出清晰提示。
func ParseData(path string, data []byte) (*jsonx.Object, error) {
	doc, _, err := parseDataWithNotes(path, data)
	return doc, err
}

// parseDataWithNotes 同 ParseData，另回传转换说明（如 Infinity/NaN 降级）。
func parseDataWithNotes(path string, data []byte) (*jsonx.Object, []string, error) {
	src := data
	var notes []string
	if filepath.Ext(path) == ".json5" {
		converted, convNotes, err := json5.ToJSONWithNotes(data)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", path, err)
		}
		src, notes = converted, convNotes
	}
	v, err := jsonx.Parse(src)
	if err != nil {
		return nil, notes, fmt.Errorf("%s: %w", path, err)
	}
	doc, ok := jsonx.AsObject(v)
	if !ok {
		return nil, notes, fmt.Errorf("%s: the top level must be a JSON object, such as {\"posts\": []}", path)
	}
	return doc, notes, nil
}
