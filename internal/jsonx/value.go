// Package jsonx 提供保留键顺序的 JSON 值模型，以及解析与序列化。
//
// JsonSrv 需要"保持原有的键顺序"写回数据文件，Go 标准库的 map 做不到，
// 这里用自定义 Object 类型（内部同时维护键列表与值映射）代替。
// 数字用 json.Number 原样保存，避免精度和格式（如 1.0）丢失。
package jsonx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

// Value 表示任意 JSON 值。具体类型映射如下：
//
//	null      → nil
//	bool      → bool
//	string    → string
//	number    → json.Number
//	array     → []Value
//	object    → *Object
type Value any

// Object 是按键插入顺序序列化、可反序列化的 JSON 对象。
type Object struct {
	keys   []string
	values map[string]Value
}

// NewObject 创建空对象。
func NewObject() *Object {
	return &Object{values: map[string]Value{}}
}

// Len 返回对象中键值对的数量。
func (o *Object) Len() int { return len(o.keys) }

// Keys 返回键的插入顺序副本。
func (o *Object) Keys() []string {
	keys := make([]string, len(o.keys))
	copy(keys, o.keys)
	return keys
}

// Get 按键取值，第二个返回值表示键是否存在。
func (o *Object) Get(key string) (Value, bool) {
	v, ok := o.values[key]
	return v, ok
}

// MustGet 按键取值，不存在时返回 nil。
func (o *Object) MustGet(key string) Value {
	return o.values[key]
}

// Set 设置键值。键已存在时更新值且保持原位置；新键追加到末尾。
func (o *Object) Set(key string, v Value) {
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = v
}

// Delete 删除键，返回是否删除了内容。
func (o *Object) Delete(key string) bool {
	if _, ok := o.values[key]; !ok {
		return false
	}
	delete(o.values, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
	return true
}

// ShallowClone 浅拷贝对象：仅复制键列表与值映射，嵌套值共享引用。
// 调用方约定：对副本只做 Set/Delete（替换顶层键值），不得就地修改嵌套值，
// 否则会通过共享引用观察到与旧对象不一致的内容（store.Update 的写时复制依赖此约定）。
func (o *Object) ShallowClone() *Object {
	out := &Object{
		keys:   append([]string(nil), o.keys...),
		values: make(map[string]Value, len(o.values)),
	}
	for k, v := range o.values {
		out.values[k] = v
	}
	return out
}

// Clone 深拷贝对象。
func (o *Object) Clone() *Object {
	out := NewObject()
	for _, k := range o.keys {
		out.Set(k, DeepClone(o.values[k]))
	}
	return out
}

// MarshalJSON 按键顺序输出紧凑 JSON。
func (o *Object) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := marshalString(k)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		v, err := marshalValue(o.values[k])
		if err != nil {
			return nil, err
		}
		buf.Write(v)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// UnmarshalJSON 读取 JSON 对象并保留键顺序，重复键取后者、位置保持首次出现处。
func (o *Object) UnmarshalJSON(data []byte) error {
	dec := newDecoder(data)
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	tm, ok := tok.(json.Delim)
	if !ok || tm != '{' {
		return fmt.Errorf("jsonx: expected an object, got %v", tok)
	}
	o.keys, o.values = nil, map[string]Value{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := tok.(string)
		if !ok {
			return fmt.Errorf("jsonx: expected a key, got %v", tok)
		}
		v, err := readValue(dec)
		if err != nil {
			return err
		}
		if o.values == nil {
			o.values = map[string]Value{}
		}
		o.Set(key, v)
	}
	if _, err := dec.Token(); err != nil { // 消费收尾的 '}'
		return err
	}
	return nil
}

// DeepClone 深拷贝任意 JSON 值。
func DeepClone(v Value) Value {
	switch tv := v.(type) {
	case *Object:
		return tv.Clone()
	case []Value:
		out := make([]Value, len(tv))
		for i, e := range tv {
			out[i] = DeepClone(e)
		}
		return out
	default:
		return v
	}
}

// DeepEqual 递归比较两个 JSON 值。数字按字符串比较（json.Number 保持原格式）。
func DeepEqual(a, b Value) bool {
	ab, aerr := marshalValue(a)
	bb, berr := marshalValue(b)
	if aerr != nil || berr != nil {
		return false
	}
	return bytes.Equal(ab, bb)
}

// AsObject 将 Value 断言为 *Object。
// IsScalarLike 判断 JSON 标量类型（数字/字符串/布尔/null 为 true；对象/数组/缺失为 false）。
func IsScalarLike(v Value) bool {
	switch v.(type) {
	case *Object, []Value:
		return false
	}
	return v != nil
}

func AsObject(v Value) (*Object, bool) {
	o, ok := v.(*Object)
	return o, ok
}

// AsArray 将 Value 断言为 []Value。
func AsArray(v Value) ([]Value, bool) {
	a, ok := v.([]Value)
	return a, ok
}

// AsNumber 将 Value 断言为 json.Number。
func AsNumber(v Value) (json.Number, bool) {
	n, ok := v.(json.Number)
	return n, ok
}

// ToString 将标量值转为其字符串形式（用于查询比较）；非标量返回 false。
func ToString(v Value) (string, bool) {
	switch tv := v.(type) {
	case string:
		return tv, true
	case json.Number:
		return tv.String(), true
	case bool:
		return strconv.FormatBool(tv), true
	case nil:
		return "", true
	}
	return "", false
}
