// Package json5 提供 JSON5 到标准 JSON 的最小转换（注释、尾随逗号、单引号、
// 无引号键、+ 号与前导句点数字、BOM、十六进制数字、尾小数点、字符串续行、
// \x 转义、Unicode 标识符）。转换后即可用 encoding/json 解析。
// Infinity/-Infinity/NaN 没有 JSON 表示：转换为 null，并通过 ToJSONWithNotes
// 回传一次性说明（调用方打印警告）。
package json5

import (
	"bytes"
	"fmt"
	"math/big"
)

// ToJSON 把 JSON5 文本转换为标准 JSON。
// 转换在原始字节上做词法级改写，保持其余部分原样输出。
func ToJSON(src []byte) ([]byte, error) {
	out, _, err := ToJSONWithNotes(src)
	return out, err
}

// ToJSONWithNotes 同 ToJSON，另返回转换过程中产生的说明
// （目前仅 Infinity/NaN 降级为 null 一种）。
func ToJSONWithNotes(src []byte) ([]byte, []string, error) {
	c := converter{in: src, out: make([]byte, 0, len(src))}
	if err := c.run(); err != nil {
		return nil, nil, err
	}
	var notes []string
	if c.specialCount > 0 {
		notes = append(notes, fmt.Sprintf(
			"JSON5: %d Infinity/NaN value(s) have no JSON equivalent and were loaded as null", c.specialCount))
	}
	return c.out, notes, nil
}

type converter struct {
	in  []byte
	out []byte
	// lastComma 记录输出中最近一次逗号位置，-1 表示无待处理尾随逗号。
	lastComma int
	// specialCount 累计 Infinity/NaN 被替换为 null 的次数。
	specialCount int
}

func (c *converter) run() error {
	i := 0
	// 跳过 BOM
	if bytes.HasPrefix(c.in, []byte("\xef\xbb\xbf")) {
		i = 3
	}
	c.lastComma = -1
	for i < len(c.in) {
		ch := c.in[i]
		switch {
		case ch == '/' && i+1 < len(c.in) && c.in[i+1] == '/':
			// 行注释：丢弃到换行
			for i < len(c.in) && c.in[i] != '\n' {
				i++
			}
		case ch == '/' && i+1 < len(c.in) && c.in[i+1] == '*':
			end := bytes.Index(c.in[i+2:], []byte("*/"))
			if end < 0 {
				return fmt.Errorf("JSON5: unterminated comment (offset %d)", i)
			}
			i += 2 + end + 2
		case ch == '"':
			n, err := c.emitJSONString(i)
			if err != nil {
				return err
			}
			i = n
		case ch == '\'':
			n, err := c.emitSingleQuoted(i)
			if err != nil {
				return err
			}
			i = n
		case ch == '{' || ch == '[':
			c.out = append(c.out, ch)
			c.lastComma = -1
			i++
		case ch == ',':
			c.out = append(c.out, ch)
			c.lastComma = len(c.out) - 1
			i++
		case ch == '}' || ch == ']':
			c.dropPendingComma()
			c.out = append(c.out, ch)
			c.lastComma = -1
			i++
		case isIdentStart(ch):
			n, err := c.emitIdentifier(i)
			if err != nil {
				return err
			}
			i = n
		case ch == '.':
			n := c.emitDotNumber(i)
			i = n
		case ch == '+':
			n, err := c.emitPlus(i)
			if err != nil {
				return err
			}
			i = n
		case isDigit(ch):
			n := c.emitNumber(i)
			i = n
		default:
			c.out = append(c.out, ch)
			i++
		}
	}
	return nil
}

// dropPendingComma 删除待处理的尾随逗号（仅当其后只有空白）。
func (c *converter) dropPendingComma() {
	if c.lastComma < 0 {
		return
	}
	ok := true
	for j := c.lastComma + 1; j < len(c.out); j++ {
		switch c.out[j] {
		case ' ', '\t', '\n', '\r':
		default:
			ok = false
		}
		if !ok {
			break
		}
	}
	if ok {
		c.out = c.out[:c.lastComma]
	}
}

// emitJSONString 从 i 处复制一个双引号字符串，同时改写 JSON5 专属转义：
// 反斜杠+换行（续行）删除；\xXX 改写为 \u00XX；\' 去掉反斜杠。
func (c *converter) emitJSONString(i int) (int, error) {
	c.out = append(c.out, '"')
	j := i + 1
	for {
		if j >= len(c.in) {
			return 0, fmt.Errorf("JSON5: unterminated string (offset %d)", i)
		}
		ch := c.in[j]
		if ch == '"' {
			c.out = append(c.out, '"')
			c.lastComma = -1
			return j + 1, nil
		}
		if ch == '\\' {
			if j+1 >= len(c.in) {
				return 0, fmt.Errorf("JSON5: unterminated string (offset %d)", i)
			}
			next := c.in[j+1]
			switch next {
			case '\n': // 行连接符：删除
				j += 2
			case '\r': // \r 或 \r\n 续行
				j += 2
				if j < len(c.in) && c.in[j] == '\n' {
					j++
				}
			case 'x': // \xXX → \u00XX
				if j+3 >= len(c.in) || !isHexDigit(c.in[j+2]) || !isHexDigit(c.in[j+3]) {
					return 0, fmt.Errorf("JSON5: incomplete \\x escape (offset %d)", j)
				}
				c.out = append(c.out, '\\', 'u', '0', '0')
				c.out = append(c.out, c.in[j+2:j+4]...)
				j += 4
			case '\'': // \' 在双引号串里无需转义
				c.out = append(c.out, '\'')
				j += 2
			default:
				c.out = append(c.out, '\\', next)
				j += 2
			}
			continue
		}
		if ch == '\n' {
			return 0, fmt.Errorf("JSON5: raw newline in string (offset %d)", i)
		}
		c.out = append(c.out, ch)
		j++
	}
}

// emitSingleQuoted 把 '...' 改写为 "..."，内部未转义的双引号加转义。
func (c *converter) emitSingleQuoted(i int) (int, error) {
	out := []byte{'"'}
	j := i + 1
	for {
		if j >= len(c.in) {
			return 0, fmt.Errorf("JSON5: unterminated string (offset %d)", i)
		}
		ch := c.in[j]
		if ch == '\\' && j+1 < len(c.in) {
			next := c.in[j+1]
			switch next {
			case '\n': // 行连接符：删除
				j += 2
			case '\r': // \r 或 \r\n 续行
				j += 2
				if j < len(c.in) && c.in[j] == '\n' {
					j++
				}
			case '\'': // \' 在双引号串中无需转义
				out = append(out, '\'')
				j += 2
			case 'x': // \xXX → \u00XX
				if j+3 >= len(c.in) || !isHexDigit(c.in[j+2]) || !isHexDigit(c.in[j+3]) {
					return 0, fmt.Errorf("JSON5: incomplete \\x escape (offset %d)", j)
				}
				out = append(out, '\\', 'u', '0', '0')
				out = append(out, c.in[j+2:j+4]...)
				j += 4
			default:
				out = append(out, '\\', next)
				j += 2
			}
			continue
		}
		if ch == '\n' {
			return 0, fmt.Errorf("JSON5: raw newline in string (offset %d)", i)
		}
		if ch == '"' {
			out = append(out, '\\', '"')
			j++
			continue
		}
		if ch == '\'' {
			out = append(out, '"')
			c.out = append(c.out, out...)
			c.lastComma = -1
			return j + 1, nil
		}
		out = append(out, ch)
		j++
	}
}

// emitIdentifier 处理裸词：后随冒号视为键加引号；true/false/null 原样；
// Infinity/NaN 转换为 null（并去掉紧邻的负号，记录降级）。
func (c *converter) emitIdentifier(i int) (int, error) {
	j := i
	for j < len(c.in) && isIdentPart(c.in[j]) {
		j++
	}
	word := c.in[i:j]
	k, err := c.skipSpaceComments(j)
	if err != nil {
		return 0, err
	}
	if k < len(c.in) && c.in[k] == ':' {
		c.out = append(c.out, '"')
		c.out = append(c.out, word...)
		c.out = append(c.out, '"')
	} else {
		switch string(word) {
		case "true", "false", "null":
			c.out = append(c.out, word...)
		case "Infinity", "NaN":
			c.specialCount++
			// -Infinity 的负号已经原样输出，需回退删除。
			if n := len(c.out); n > 0 && c.out[n-1] == '-' {
				c.out = c.out[:n-1]
			}
			c.out = append(c.out, "null"...)
		default:
			return 0, fmt.Errorf("JSON5: unsupported bare identifier %q (offset %d)", word, i)
		}
	}
	c.lastComma = -1
	return j, nil
}

// emitNumber 转换一个以数字起始的数值词：十六进制（0x1A）改写为十进制；
// 尾小数点（5.）补零为 5.0。返回结束位置。
func (c *converter) emitNumber(i int) int {
	// 十六进制：0x 前缀
	if c.in[i] == '0' && i+1 < len(c.in) && (c.in[i+1] == 'x' || c.in[i+1] == 'X') {
		k := i + 2
		for k < len(c.in) && isHexDigit(c.in[k]) {
			k++
		}
		if k > i+2 && (k >= len(c.in) || !isIdentPart(c.in[k])) {
			neg := false
			if n := len(c.out); n > 0 && c.out[n-1] == '-' {
				c.out = c.out[:n-1]
				neg = true
			}
			n, ok := new(big.Int).SetString(string(c.in[i+2:k]), 16)
			if !ok {
				n = new(big.Int)
			}
			if neg {
				n.Neg(n)
			}
			c.out = append(c.out, n.String()...)
			c.lastComma = -1
			return k
		}
	}
	// 十进制：扫描数字、小数点与指数，定位需要补零的尾小数点。
	j := i
	for j < len(c.in) && isDigit(c.in[j]) {
		j++
	}
	dotPos, dotNoFrac := -1, false
	if j < len(c.in) && c.in[j] == '.' {
		dotPos = j
		j++
		fs := j
		for j < len(c.in) && isDigit(c.in[j]) {
			j++
		}
		dotNoFrac = j == fs
	}
	// 指数部分（5e3、5.e3 都并入数值词，避免落到标识符分支报错）
	if j < len(c.in) && (c.in[j] == 'e' || c.in[j] == 'E') {
		k := j + 1
		if k < len(c.in) && (c.in[k] == '+' || c.in[k] == '-') {
			k++
		}
		ds := k
		for k < len(c.in) && isDigit(c.in[k]) {
			k++
		}
		if k > ds {
			j = k
		}
	}
	if dotNoFrac {
		c.out = append(c.out, c.in[i:dotPos+1]...)
		c.out = append(c.out, '0')
		c.out = append(c.out, c.in[dotPos+1:j]...)
	} else {
		c.out = append(c.out, c.in[i:j]...)
	}
	c.lastComma = -1
	return j
}

// emitDotNumber 把值起始处的 .5 改写为 0.5（-.5 之类罕见写法不处理，解析将报错）。
func (c *converter) emitDotNumber(i int) int {
	if c.prevSignificantOneOf('{', '[', ':', ',') {
		c.out = append(c.out, '0', '.')
		return i + 1
	}
	c.out = append(c.out, '.')
	return i + 1
}

// emitPlus 去掉值起始处的 + 号。
func (c *converter) emitPlus(i int) (int, error) {
	if c.prevSignificantOneOf('{', '[', ':', ',') {
		return i + 1, nil
	}
	return 0, fmt.Errorf("JSON5: unsupported plus sign (offset %d)", i)
}

// skipSpaceComments 从 pos 起跳过空白与注释，返回首个实质字符位置。
func (c *converter) skipSpaceComments(pos int) (int, error) {
	for pos < len(c.in) {
		ch := c.in[pos]
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			pos++
			continue
		}
		if ch == '/' && pos+1 < len(c.in) && c.in[pos+1] == '/' {
			for pos < len(c.in) && c.in[pos] != '\n' {
				pos++
			}
			continue
		}
		if ch == '/' && pos+1 < len(c.in) && c.in[pos+1] == '*' {
			end := bytes.Index(c.in[pos+2:], []byte("*/"))
			if end < 0 {
				return 0, fmt.Errorf("JSON5: unterminated comment (offset %d)", pos)
			}
			pos += 2 + end + 2
			continue
		}
		return pos, nil
	}
	return pos, nil
}

// prevSignificantOneOf 判断最近的非空白输出字符属于给定集合。
func (c *converter) prevSignificantOneOf(chars ...byte) bool {
	j := len(c.out)
	for j > 0 && isSpace(c.out[j-1]) {
		j--
	}
	if j == 0 {
		return false
	}
	for _, want := range chars {
		if c.out[j-1] == want {
			return true
		}
	}
	return false
}

func isSpace(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r'
}

func isDigit(ch byte) bool { return ch >= '0' && ch <= '9' }

func isHexDigit(ch byte) bool {
	return isDigit(ch) || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}

// isIdentStart 判断 JSON5 标识符起始字节：ASCII 字母、_、$ 或非 ASCII
// （UTF-8 多字节序列按字节扫描即可，键最终会整体加引号）。
func isIdentStart(ch byte) bool {
	return ch == '_' || ch == '$' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch >= 0x80
}

func isIdentPart(ch byte) bool {
	return isIdentStart(ch) || isDigit(ch)
}
