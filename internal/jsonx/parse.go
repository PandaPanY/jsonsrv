package jsonx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Parse 解析 JSON（含 JSON5 允许的宽松差异见 internal/json5）。
// 开头的 UTF-8 BOM 自动跳过（0.17 能启动带 BOM 的文件；写回不带 BOM）。
// 要求输入为完整单一 JSON 值；返回 Value（见类型映射注释）。
func Parse(data []byte) (Value, error) {
	if len(data) >= 3 && data[0] == 0xef && data[1] == 0xbb && data[2] == 0xbf {
		data = data[3:]
	}
	dec := newDecoder(data)
	v, err := readValue(dec)
	if err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("unexpected content after the end of the JSON")
	}
	return v, nil
}

// newDecoder 构造保留数字格式的 Decoder。
func newDecoder(data []byte) *json.Decoder {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return dec
}

// readValue 从 decoder 读取一个完整的 JSON 值（含嵌套）。
func readValue(dec *json.Decoder) (Value, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, mapDecodeError(err)
	}
	return readFromToken(dec, tok)
}

func readFromToken(dec *json.Decoder, tok any) (Value, error) {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			return readObject(dec)
		case '[':
			return readArray(dec)
		default:
			return nil, fmt.Errorf("unexpected bracket %q", string(t))
		}
	case json.Number, string, bool:
		return t, nil
	case nil:
		return nil, nil
	}
	return nil, fmt.Errorf("unsupported token %v", tok)
}

func readObject(dec *json.Decoder) (Value, error) {
	obj := NewObject()
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, mapDecodeError(err)
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("expected a key, got %v", tok)
		}
		v, err := readValue(dec)
		if err != nil {
			return nil, err
		}
		obj.Set(key, v)
	}
	if _, err := dec.Token(); err != nil { // 消费 '}'
		return nil, mapDecodeError(err)
	}
	return obj, nil
}

func readArray(dec *json.Decoder) (Value, error) {
	arr := []Value{}
	for dec.More() {
		v, err := readValue(dec)
		if err != nil {
			return nil, err
		}
		arr = append(arr, v)
	}
	if _, err := dec.Token(); err != nil { // 消费 ']'
		return nil, mapDecodeError(err)
	}
	return arr, nil
}

func mapDecodeError(err error) error {
	if errors.Is(err, io.EOF) {
		return errors.New("JSON ended unexpectedly (it may be truncated)")
	}
	return fmt.Errorf("invalid JSON: %w", err)
}
