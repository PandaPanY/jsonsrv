package jsonx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Marshal 序列化为 2 空格缩进的 JSON，键按插入顺序输出，不做 HTML 转义。
func Marshal(v Value) ([]byte, error) {
	var buf bytes.Buffer
	if err := MarshalIndent(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// MarshalIndent 流式写出 2 空格缩进的 JSON（等价于 json.Indent 的结果再补一个换行），
// 输出逐段写入 w，不在内存中拼完整的紧凑字节串（大库保存时避免双份内存）。
// 输出形态必须与 marshalValue + json.Indent 逐字节一致，jsonx 测试里有对拍。
func MarshalIndent(w io.Writer, v Value) error {
	enc := &indentWriter{w: w, indent: "  "}
	if err := enc.value(v); err != nil {
		return err
	}
	_, err := enc.w.Write([]byte{'\n'})
	return err
}

// indentWriter 按 json.Indent 的规则缩进：
// 容器非空时元素前后换行加缩进，空容器保持 "{}"/"[]" 单行；冒号后有一个空格。
type indentWriter struct {
	w      io.Writer
	indent string
	depth  int
	err    error
}

// writeAll 向底层写入字符串（忽略中间错误，首次出错后短路）。
func (enc *indentWriter) writeAll(s string) {
	if enc.err != nil {
		return
	}
	_, enc.err = io.WriteString(enc.w, s)
}

// level 调整当前缩进层级。
func (enc *indentWriter) level(delta int) { enc.depth += delta }

// newline 写入换行与当前层缩进。
func (enc *indentWriter) newline() {
	enc.writeAll("\n")
	enc.writeAll(strings.Repeat(enc.indent, enc.depth))
}

// value 递归写出一个 JSON 值。
func (enc *indentWriter) value(v Value) error {
	switch tv := v.(type) {
	case *Object:
		if tv.Len() == 0 {
			enc.writeAll("{}")
			return enc.err
		}
		enc.writeAll("{")
		enc.level(1)
		for i, k := range tv.Keys() {
			if i > 0 {
				enc.writeAll(",")
			}
			enc.newline()
			key, err := marshalString(k)
			if err != nil {
				return err
			}
			enc.writeAll(string(key))
			enc.writeAll(": ")
			if err := enc.value(tv.MustGet(k)); err != nil {
				return err
			}
		}
		enc.level(-1)
		enc.newline()
		enc.writeAll("}")
		return enc.err
	case []Value:
		if len(tv) == 0 {
			enc.writeAll("[]")
			return enc.err
		}
		enc.writeAll("[")
		enc.level(1)
		for i, e := range tv {
			if i > 0 {
				enc.writeAll(",")
			}
			enc.newline()
			if err := enc.value(e); err != nil {
				return err
			}
		}
		enc.level(-1)
		enc.newline()
		enc.writeAll("]")
		return enc.err
	default:
		b, err := marshalValue(v)
		if err != nil {
			return err
		}
		_, err = enc.w.Write(b)
		return err
	}
}

// MarshalCompact 输出紧凑 JSON（不缩进、不换行）。
func MarshalCompact(v Value) ([]byte, error) {
	return marshalValue(v)
}

// marshalValue 将 Value 编码为紧凑 JSON 字节。
func marshalValue(v Value) ([]byte, error) {
	switch tv := v.(type) {
	case nil:
		return []byte("null"), nil
	case bool:
		return []byte(strconv.FormatBool(tv)), nil
	case json.Number:
		return []byte(tv.String()), nil
	case string:
		return marshalString(tv)
	case []Value:
		var buf bytes.Buffer
		buf.WriteByte('[')
		for i, e := range tv {
			if i > 0 {
				buf.WriteByte(',')
			}
			b, err := marshalValue(e)
			if err != nil {
				return nil, err
			}
			buf.Write(b)
		}
		buf.WriteByte(']')
		return buf.Bytes(), nil
	case *Object:
		return tv.MarshalJSON()
	case json.Marshaler:
		return tv.MarshalJSON()
	}
	return nil, fmt.Errorf("jsonx: unsupported value type %T", v)
}

// marshalString 用不转义 HTML 的形式编码字符串。
// 自研转义例程，输出形态与 encoding/json（SetEscapeHTML(false) 的字符串编码）一致，
// 但去掉每值一个 Encoder/bytes.Buffer 的分配（10 万记录库实测曾占 POST 耗时的大头）。
func marshalString(s string) ([]byte, error) {
	return appendJSONString(nil, s), nil
}

// appendJSONString 把 s 的 JSON 字符串表示（不做 HTML 转义）追加到 dst。
// 转义规则逐条对齐 encoding/json 的非 HTML 模式：
//   - '"'、'\' 反斜杠转义；\b \f \n \r \t 用短转义；其余 <0x20 控制字符用 /u00XX（小写十六进制）；
//   - 0x7F 原样保留；有效的多字节 UTF-8 原样保留；
//   - 非法 UTF-8 字节序列替换为 /ufffd（每字节一次，行为同 Go 编码器）。
func appendJSONString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	for i := 0; i < len(s); {
		c := s[i]
		if c >= 0x80 {
			r, size := utf8.DecodeRuneInString(s[i:])
			switch {
			case r == utf8.RuneError && size <= 1:
				// 非法 UTF-8 字节：写原始替换字符（EF BF BD），与 Go 编码器一致（服务器实测）。
				dst = append(dst, 0xEF, 0xBF, 0xBD)
				i++
				continue
			case r == 0x2028: // 行/段分隔符：标准库按 JS 安全做 \u 转义，这里保持一致
				dst = append(dst, '\\', 'u', '2', '0', '2', '8')
				i += size
				continue
			case r == 0x2029:
				dst = append(dst, '\\', 'u', '2', '0', '2', '9')
				i += size
				continue
			}
			dst = append(dst, s[i:i+size]...)
			i += size
			continue
		}
		i++
		switch c {
		case '"':
			dst = append(dst, '\\', '"')
		case '\\':
			dst = append(dst, '\\', '\\')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		case '\b':
			dst = append(dst, '\\', 'b')
		case '\f':
			dst = append(dst, '\\', 'f')
		default:
			if c < 0x20 {
				dst = append(dst, '\\', 'u', '0', '0', hexDigit(c>>4), hexDigit(c&0xF))
			} else {
				dst = append(dst, c)
			}
		}
	}
	return append(dst, '"')
}

func hexDigit(b byte) byte {
	const hexChars = "0123456789abcdef"
	return hexChars[b&0xF]
}
