// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fmt

import (
	"strconv"
	"unicode/utf8"
)

const (
	ldigits = "0123456789abcdefx"
	udigits = "0123456789ABCDEFX"
)

const (
	signed   = true
	unsigned = false
)

// fmtFlags 放置到一个单独的 struct 中，这样方便进行清空整个 struct.
type fmtFlags struct {
	widPresent  bool
	precPresent bool
	minus       bool
	plus        bool
	sharp       bool
	space       bool
	zero        bool

	// 对于格式%+v 、 %#v，分别对应 plusV / sharpV 标志
	// 并清除plus / sharp标志 .
	// %#v 会执行 GoStringer 的 GoString()
	plusV  bool
	sharpV bool
}

// fmt  是 Printf 等格式化输出的基础.
// 它打印到一个单独设立的 buffer 中.
type fmt struct {
	buf *buffer

	fmtFlags

	wid  int // width
	prec int // precision

	// intbuf is large enough to store %b of an int64 with a sign and
	// avoids padding at the end of the struct on 32 bit architectures.
	intbuf [68]byte
}

func (f *fmt) clearflags() {
	f.fmtFlags = fmtFlags{}
}

func (f *fmt) init(buf *buffer) {
	f.buf = buf
	f.clearflags()
}

// writePadding 在 f.buf 后面直接生成 n 个填充字节.
func (f *fmt) writePadding(n int) {
	if n <= 0 { // 无需填充
		return
	}
	buf := *f.buf
	oldLen := len(buf)
	newLen := oldLen + n
	// 根据需要填充的字节数以及原来 buf 的长度，给 buf 开辟新的内存空间.
	if newLen > cap(buf) {
		buf = make(buffer, cap(buf)*2+n)
		copy(buf, *f.buf)
	}
	// 决定 padByte 的字节的内容.
	// 默认是空格，如果设置了 f.zero 那就是 0 填充
	padByte := byte(' ')
	if f.zero {
		padByte = byte('0')
	}
	// 使用 padByte 填充.padding 大小的空间，
	padding := buf[oldLen:newLen]
	for i := range padding {
		padding[i] = padByte
	}
	*f.buf = buf[:newLen]
}

// pad 追加 b 到 f.buf 中,
// 如果 !f.minus 追加在左侧，反之 (f.minus) 右侧 .
func (f *fmt) pad(b []byte) {
	if !f.widPresent || f.wid == 0 {
		f.buf.write(b)
		return
	}
	width := f.wid - utf8.RuneCount(b)
	if !f.minus {
		// 在左侧填充
		f.writePadding(width)
		f.buf.write(b)
	} else {
		// 右侧填充
		f.buf.write(b)
		f.writePadding(width)
	}
}

// padString 追加字符串 s 到 f.buf, 如果 !f.minus 追加在左侧，反之 (f.minus) 右侧 ..
func (f *fmt) padString(s string) {
	if !f.widPresent || f.wid == 0 {
		f.buf.writeString(s)
		return
	}
	width := f.wid - utf8.RuneCountInString(s)
	if !f.minus {
		// 左边追加
		f.writePadding(width)
		f.buf.writeString(s)
	} else {
		// 右边追加
		f.buf.writeString(s)
		f.writePadding(width)
	}
}

// fmtBoolean 格式化输出 bool 类型.
func (f *fmt) fmtBoolean(v bool) {
	if v {
		f.padString("true")
	} else {
		f.padString("false")
	}
}

// fmtUnicode 将 uint64 格式化为 “U+0078” 或将 f.sharp 设置为 “U+0078'x'”.
func (f *fmt) fmtUnicode(u uint64) {
	buf := f.intbuf[0:]

	// With default precision set the maximum needed buf length is 18
	// for formatting -1 with %#U ("U+FFFFFFFFFFFFFFFF") which fits
	// into the already allocated intbuf with a capacity of 68 bytes.
	prec := 4
	if f.precPresent && f.prec > 4 {
		prec = f.prec
		// Compute space needed for "U+" , number, " '", character, "'".
		width := 2 + prec + 2 + utf8.UTFMax + 1
		if width > len(buf) {
			buf = make([]byte, width)
		}
	}

	// Format into buf, ending at buf[i]. Formatting numbers is easier right-to-left.
	i := len(buf)

	// For %#U we want to add a space and a quoted character at the end of the buffer.
	if f.sharp && u <= utf8.MaxRune && strconv.IsPrint(rune(u)) {
		i--
		buf[i] = '\''
		i -= utf8.RuneLen(rune(u))
		utf8.EncodeRune(buf[i:], rune(u))
		i--
		buf[i] = '\''
		i--
		buf[i] = ' '
	}
	// Format the Unicode code point u as a hexadecimal number.
	for u >= 16 {
		i--
		buf[i] = udigits[u&0xF]
		prec--
		u >>= 4
	}
	i--
	buf[i] = udigits[u]
	prec--
	// Add zeros in front of the number until requested precision is reached.
	for prec > 0 {
		i--
		buf[i] = '0'
		prec--
	}
	// Add a leading "U+".
	i--
	buf[i] = '+'
	i--
	buf[i] = 'U'

	oldZero := f.zero
	f.zero = false
	f.pad(buf[i:])
	f.zero = oldZero
}

// fmtInteger 格式化 signed （有符号） 和 unsigned （无符号） 整数.
func (f *fmt) fmtInteger(u uint64, base int, isSigned bool, verb rune, digits string) {
	negative := isSigned && int64(u) < 0
	if negative {
		u = -u
	}

	buf := f.intbuf[0:]
	// 当没有设置精度或宽度时，
	//  已经分配的容量为 68 字节的 f.intbuf 足够进行整数格式化
	if f.widPresent || f.precPresent {
		// 额外增加 3 个字节，因为可能是带符号的或者"0x".
		width := 3 + f.wid + f.prec // wid 和 prec 总是正数.
		if width > len(buf) {
			// 根据最新的width，重新分配内存给 buf.
			buf = make([]byte, width)
		}
	}

	// 要求额外的前置 zero digits : %.3d or %03d.
	// 如果两者都被指定，则忽略 f.zero 标志并使用空格填充。.
	prec := 0
	if f.precPresent {
		prec = f.prec
		// 精度为 0 并且值为 0 意味着 “除了填充之外什么也不打印” 。
		if prec == 0 && u == 0 {
			oldZero := f.zero
			f.zero = false
			f.writePadding(f.wid)
			f.zero = oldZero
			return
		}
	} else if f.zero && f.widPresent {
		prec = f.wid
		if negative || f.plus || f.space {
			prec-- // leave room for sign
		}
	}

	// Because printing is easier right-to-left: format u into buf, ending at buf[i].
	// We could make things marginally faster by splitting the 32-bit case out
	// into a separate block but it's not worth the duplication, so u has 64 bits.
	// 这里 i 为最大长度的原因是为了能给在计算时方便直接从右往左边计算
	i := len(buf)
	// Use constants for the division and modulo for more efficient code.
	// Switch cases ordered by popularity.
	switch base {
	case 10:
		for u >= 10 {
			i--
			next := u / 10
			buf[i] = byte('0' + u - next*10)
			u = next
		}
	case 16:
		for u >= 16 {
			i--
			buf[i] = digits[u&0xF]
			u >>= 4
		}
	case 8:
		for u >= 8 {
			i--
			buf[i] = byte('0' + u&7)
			u >>= 3
		}
	case 2:
		for u >= 2 {
			i--
			buf[i] = byte('0' + u&1)
			u >>= 1
		}
	default:
		panic("fmt: unknown base; can't happen")
	}
	i--
	buf[i] = digits[u]
	for i > 0 && prec > len(buf)-i {
		i--
		buf[i] = '0'
	}

	// Various prefixes: 0x, -, etc.
	if f.sharp {
		switch base {
		case 2:
			// Add a leading 0b.
			i--
			buf[i] = 'b'
			i--
			buf[i] = '0'
		case 8:
			if buf[i] != '0' {
				i--
				buf[i] = '0'
			}
		case 16:
			// Add a leading 0x or 0X.
			i--
			buf[i] = digits[16]
			i--
			buf[i] = '0'
		}
	}
	if verb == 'O' {
		i--
		buf[i] = 'o'
		i--
		buf[i] = '0'
	}

	if negative {
		i--
		buf[i] = '-'
	} else if f.plus {
		i--
		buf[i] = '+'
	} else if f.space {
		i--
		buf[i] = ' '
	}

	// Left padding with zeros has already been handled like precision earlier
	// or the f.zero flag is ignored due to an explicitly set precision.
	oldZero := f.zero
	f.zero = false
	f.pad(buf[i:])
	f.zero = oldZero
}

// truncate 按照 f.prec 的精度，将 s 截断为指定的长度.
func (f *fmt) truncateString(s string) string {
	if f.precPresent {
		n := f.prec
		for i := range s {
			n--
			if n < 0 {
				return s[:i]
			}
		}
	}
	return s
}

// truncate 将字节切片 b 按照字符串的方式进行截断.
func (f *fmt) truncate(b []byte) []byte {
	if f.precPresent {
		n := f.prec
		for i := 0; i < len(b); {
			n--
			if n < 0 {
				return b[:i]
			}
			wid := 1

			// utf8.RuneSelf 表示单字节编码的utf-8
			// 所以，如果大于说明不是单字节，就需要处理
			if b[i] >= utf8.RuneSelf {
				_, wid = utf8.DecodeRune(b[i:])
			}
			i += wid
		}
	}
	return b
}

// fmtS 格式化字符串.
func (f *fmt) fmtS(s string) {
	s = f.truncateString(s)
	f.padString(s)
}

// fmtBs 将字节切换按照字符串格式化，类似于 fmtS.
func (f *fmt) fmtBs(b []byte) {
	b = f.truncate(b)
	f.pad(b)
}

// fmtSbx 将字符串和字节切片格式化为16进制.
func (f *fmt) fmtSbx(s string, b []byte, digits string) {
	length := len(b)
	if b == nil {
		// No byte slice present. Assume string s should be encoded.
		length = len(s)
	}
	// 如果设置了精度，那么按照 f.prec 的值进行设置 length.
	if f.precPresent && f.prec < length {
		length = f.prec
	}
	// Compute width of the encoding taking into account the f.sharp and f.space flag.
	width := 2 * length
	if width > 0 {
		if f.space {
			// 每两个16进制编码的元素会有一个前缀 0x 或 0X.
			if f.sharp {
				width *= 2
			}
			// 每两个元素之间都会有一个空格.
			width += length - 1
		} else if f.sharp {
			// Only a leading 0x or 0X will be added for the whole string.
			width += 2
		}
	} else { // 空的字符串和切片无需进行处理了.
		if f.widPresent {
			f.writePadding(f.wid)
		}
		return
	}
	// 左侧填充.
	if f.widPresent && f.wid > width && !f.minus {
		f.writePadding(f.wid - width)
	}
	// Write the encoding directly into the output buffer.
	buf := *f.buf
	if f.sharp {
		// Add leading 0x or 0X.
		buf = append(buf, '0', digits[16])
	}
	var c byte
	for i := 0; i < length; i++ {
		if f.space && i > 0 {
			// Separate elements with a space.
			buf = append(buf, ' ')
			if f.sharp {
				// Add leading 0x or 0X for each element.
				buf = append(buf, '0', digits[16])
			}
		}
		if b != nil {
			c = b[i] // Take a byte from the input byte slice.
		} else {
			c = s[i] // Take a byte from the input string.
		}
		// 将每个字节编码为两个十六进制数字.
		buf = append(buf, digits[c>>4], digits[c&0xF])
	}
	*f.buf = buf
	// Handle padding to the right.
	if f.widPresent && f.wid > width && f.minus {
		f.writePadding(f.wid - width)
	}
}

// fmtSx 将字符串格式化为字节的十六进制编码.
func (f *fmt) fmtSx(s, digits string) {
	f.fmtSbx(s, nil, digits)
}

// fmtBx 将字节切片格式化为字节的十六进制编码.
func (f *fmt) fmtBx(b []byte, digits string) {
	f.fmtSbx("", b, digits)
}

// fmtQ 将字符串格式化为双引号转义的 Go 字符串常量。.
// 如果设置了 f.sharp，则如果字符串不包含除制表符以外的任何控制字符
// 则可以返回一个原始字符串.
func (f *fmt) fmtQ(s string) {
	s = f.truncateString(s)
	if f.sharp && strconv.CanBackquote(s) {
		f.padString("`" + s + "`")
		return
	}
	buf := f.intbuf[:0]
	if f.plus {
		f.pad(strconv.AppendQuoteToASCII(buf, s))
	} else {
		f.pad(strconv.AppendQuote(buf, s))
	}
}

// fmtC 将整数格式化为 Unicode 字符.
// 如果字符不是有效的 Unicode，它将打印 '\ufffd'.
func (f *fmt) fmtC(c uint64) {
	r := rune(c)
	if c > utf8.MaxRune {
		r = utf8.RuneError
	}
	buf := f.intbuf[:0]
	w := utf8.EncodeRune(buf[:utf8.UTFMax], r)
	f.pad(buf[:w])
}

// fmtQc 将整数格式化为单引号转义的 Go 字符常量.
// 如果字符不是有效的 Unicode，它将打印 '\ufffd'.
func (f *fmt) fmtQc(c uint64) {
	r := rune(c)
	if c > utf8.MaxRune {
		r = utf8.RuneError
	}
	buf := f.intbuf[:0]
	if f.plus {
		f.pad(strconv.AppendQuoteRuneToASCII(buf, r))
	} else {
		f.pad(strconv.AppendQuoteRune(buf, r))
	}
}

// fmtFloat 格式化 float64. 这里假设 verb 对 strconv.AppendFloat 是有效的
// 所以只需要一个字节即可.
func (f *fmt) fmtFloat(v float64, size int, verb rune, prec int) {
	// Explicit precision in format specifier overrules default precision.
	if f.precPresent {
		prec = f.prec
	}
	// Format number, reserving space for leading + sign if needed.
	num := strconv.AppendFloat(f.intbuf[:1], v, byte(verb), prec, size)
	if num[1] == '-' || num[1] == '+' {
		num = num[1:]
	} else {
		num[0] = '+'
	}
	// f.space means to add a leading space instead of a "+" sign unless
	// the sign is explicitly asked for by f.plus.
	if f.space && num[0] == '+' && !f.plus {
		num[0] = ' '
	}
	// Special handling for infinities and NaN,
	// which don't look like a number so shouldn't be padded with zeros.
	if num[1] == 'I' || num[1] == 'N' {
		oldZero := f.zero
		f.zero = false
		// Remove sign before NaN if not asked for.
		if num[1] == 'N' && !f.space && !f.plus {
			num = num[1:]
		}
		f.pad(num)
		f.zero = oldZero
		return
	}
	// The sharp flag forces printing a decimal point for non-binary formats
	// and retains trailing zeros, which we may need to restore.
	if f.sharp && verb != 'b' {
		digits := 0
		switch verb {
		case 'v', 'g', 'G', 'x':
			digits = prec
			// If no precision is set explicitly use a precision of 6.
			if digits == -1 {
				digits = 6
			}
		}

		// Buffer pre-allocated with enough room for
		// exponent notations of the form "e+123" or "p-1023".
		var tailBuf [6]byte
		tail := tailBuf[:0]

		hasDecimalPoint := false
		sawNonzeroDigit := false
		// Starting from i = 1 to skip sign at num[0].
		for i := 1; i < len(num); i++ {
			switch num[i] {
			case '.':
				hasDecimalPoint = true
			case 'p', 'P':
				tail = append(tail, num[i:]...)
				num = num[:i]
			case 'e', 'E':
				if verb != 'x' && verb != 'X' {
					tail = append(tail, num[i:]...)
					num = num[:i]
					break
				}
				fallthrough
			default:
				if num[i] != '0' {
					sawNonzeroDigit = true
				}
				// Count significant digits after the first non-zero digit.
				if sawNonzeroDigit {
					digits--
				}
			}
		}
		if !hasDecimalPoint {
			// Leading digit 0 should contribute once to digits.
			if len(num) == 2 && num[1] == '0' {
				digits--
			}
			num = append(num, '.')
		}
		for digits > 0 {
			num = append(num, '0')
			digits--
		}
		num = append(num, tail...)
	}
	// We want a sign if asked for and if the sign is not positive.
	if f.plus || num[0] != '+' {
		// If we're zero padding to the left we want the sign before the leading zeros.
		// Achieve this by writing the sign out and then padding the unsigned number.
		if f.zero && f.widPresent && f.wid > len(num) {
			f.buf.writeByte(num[0])
			f.writePadding(f.wid - len(num))
			f.buf.write(num[1:])
			return
		}
		f.pad(num)
		return
	}
	// No sign to show and the number is positive; just print the unsigned number.
	f.pad(num[1:])
}
