package features

import (
	"fmt"
	"unicode/utf16"
)

// This file ports the error reporting of V8's JSON.parse (json-parser.cc, the
// V8 12.4 Node.js 22 ships), which the reference's readLockfile surfaces as
// the message of the SyntaxError it rethrows. It only validates: encoding/json
// still decodes a lockfile that parses.
//
// V8 parses a JavaScript string, so positions, columns and the quoted context
// count UTF-16 code units; a lone surrogate V8 would quote comes out as U+FFFD.

// v8JSONSyntaxError is the message JSON.parse(src) throws with, or "" when src
// is valid JSON.
func v8JSONSyntaxError(src string) string {
	p := &v8Parser{s: utf16.Encode([]rune(src))}
	if p.value() && !p.check(tokEOS) {
		p.report(p.peek(), msgNonWhitespaceAfterJSON)
	}
	return p.err
}

type jsonToken int

const (
	tokIllegal jsonToken = iota
	tokEOS
	tokString
	tokNumber
	tokTrue
	tokFalse
	tokNull
	tokWhitespace
	tokColon
	tokComma
	tokLBrack
	tokRBrack
	tokLBrace
	tokRBrace
)

// tokenOf is V8's one_char_json_tokens: the token a character starts.
func tokenOf(c uint16) jsonToken {
	switch {
	case c == '"':
		return tokString
	case c == '-' || '0' <= c && c <= '9':
		return tokNumber
	case c == ' ' || c == '\t' || c == '\r' || c == '\n':
		return tokWhitespace
	}
	switch c {
	case 't':
		return tokTrue
	case 'f':
		return tokFalse
	case 'n':
		return tokNull
	case ':':
		return tokColon
	case ',':
		return tokComma
	case '[':
		return tokLBrack
	case ']':
		return tokRBrack
	case '{':
		return tokLBrace
	case '}':
		return tokRBrace
	}
	return tokIllegal
}

// The message templates V8 reports with a position (message-template.h), up
// to " at position".
const (
	msgPropNameOrRBrace       = "Expected property name or '}' in JSON"
	msgDoubleQuotedPropName   = "Expected double-quoted property name in JSON"
	msgColonAfterPropName     = "Expected ':' after property name in JSON"
	msgCommaOrRBrace          = "Expected ',' or '}' after property value in JSON"
	msgCommaOrRBrack          = "Expected ',' or ']' after array element in JSON"
	msgNonWhitespaceAfterJSON = "Unexpected non-whitespace character after JSON"
	msgUnterminatedString     = "Unterminated string in JSON"
	msgBadControlCharacter    = "Bad control character in string literal in JSON"
	msgBadEscapedCharacter    = "Bad escaped character in JSON"
	msgBadUnicodeEscape       = "Bad Unicode escape in JSON"
	msgNoNumberAfterMinus     = "No number after minus sign in JSON"
	msgUnterminatedFraction   = "Unterminated fractional number in JSON"
	msgExponentMissingNumber  = "Exponent part is missing a number in JSON"
	msgUnexpectedNumber       = "Unexpected number in JSON"
	msgUnexpectedString       = "Unexpected string in JSON"
)

// kMaxContextCharacters: how much of a long source an "Unexpected token"
// message quotes on each side of the token.
const v8ContextCharacters = 10

type v8Parser struct {
	s   []uint16
	pos int
	err string
}

// peek is SkipWhitespace: it moves past whitespace and returns the token the
// next character starts (EOS at the end).
func (p *v8Parser) peek() jsonToken {
	for ; p.pos < len(p.s); p.pos++ {
		if t := tokenOf(p.s[p.pos]); t != tokWhitespace {
			return t
		}
	}
	return tokEOS
}

// check consumes the next token when it is t.
func (p *v8Parser) check(t jsonToken) bool {
	if p.peek() == t {
		p.pos++
		return true
	}
	return false
}

// expect consumes the next token, which must be t, else reports msg.
func (p *v8Parser) expect(t jsonToken, msg string) bool {
	if p.check(t) {
		return true
	}
	p.report(p.peek(), msg)
	return false
}

// value parses one JSON value (ParseJsonValue).
func (p *v8Parser) value() bool {
	switch p.peek() {
	case tokString:
		p.pos++
		return p.scanString()
	case tokNumber:
		return p.number()
	case tokLBrace:
		p.pos++
		if p.check(tokRBrace) {
			return true
		}
		if !p.property(msgPropNameOrRBrace) {
			return false
		}
		for {
			if !p.value() {
				return false
			}
			if !p.check(tokComma) {
				return p.expect(tokRBrace, msgCommaOrRBrace)
			}
			if !p.property(msgDoubleQuotedPropName) {
				return false
			}
		}
	case tokLBrack:
		p.pos++
		if p.check(tokRBrack) {
			return true
		}
		for {
			if !p.value() {
				return false
			}
			if !p.check(tokComma) {
				return p.expect(tokRBrack, msgCommaOrRBrack)
			}
		}
	case tokTrue:
		return p.literal("true")
	case tokFalse:
		return p.literal("false")
	case tokNull:
		return p.literal("null")
	}
	p.reportUnexpectedCharacter()
	return false
}

// property parses a property key and its colon; missing is the message for a
// missing key.
func (p *v8Parser) property(missing string) bool {
	return p.expect(tokString, missing) && p.scanString() && p.expect(tokColon, msgColonAfterPropName)
}

// literal is ScanLiteral: the first character of word already matched.
func (p *v8Parser) literal(word string) bool {
	for i := 1; i < len(word); i++ {
		p.pos++
		if p.pos == len(p.s) {
			p.report(tokEOS, "")
			return false
		}
		if p.s[p.pos] != uint16(word[i]) {
			p.reportUnexpectedCharacter()
			return false
		}
	}
	p.pos++
	return true
}

// scanString is ScanJsonString, from just after the opening quote.
func (p *v8Parser) scanString() bool {
	for ; p.pos < len(p.s); p.pos++ {
		switch c := p.s[p.pos]; {
		case c == '"':
			p.pos++
			return true
		case c == '\\':
			if !p.escape() {
				return false
			}
		case c < 0x20:
			p.report(tokIllegal, msgBadControlCharacter)
			return false
		}
	}
	p.report(tokIllegal, msgUnterminatedString)
	return false
}

// escape checks the escape sequence whose backslash is at pos, leaving pos on
// its last character.
func (p *v8Parser) escape() bool {
	p.pos++
	if p.pos == len(p.s) || p.s[p.pos] > 0xff {
		p.reportUnexpectedCharacter()
		return false
	}
	switch p.s[p.pos] {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		return true
	case 'u':
		for range 4 {
			p.pos++
			if p.pos == len(p.s) || !isHexDigit(p.s[p.pos]) {
				p.report(tokIllegal, msgBadUnicodeEscape)
				return false
			}
		}
		return true
	}
	p.report(tokIllegal, msgBadEscapedCharacter)
	return false
}

// number is ParseJsonNumber's validation.
func (p *v8Parser) number() bool {
	if p.s[p.pos] == '-' {
		p.pos++
	}
	if p.at('0') {
		p.pos++
		if p.isDigit() {
			p.report(tokNumber, "")
			return false
		}
	} else if !p.digits() {
		p.report(tokIllegal, msgNoNumberAfterMinus)
		return false
	}
	if p.at('.') {
		p.pos++
		if !p.digits() {
			p.report(tokIllegal, msgUnterminatedFraction)
			return false
		}
	}
	if p.at('e') || p.at('E') {
		p.pos++
		if p.at('-') || p.at('+') {
			p.pos++
		}
		if !p.digits() {
			p.report(tokIllegal, msgExponentMissingNumber)
			return false
		}
	}
	return true
}

func (p *v8Parser) at(c uint16) bool {
	return p.pos < len(p.s) && p.s[p.pos] == c
}

func (p *v8Parser) isDigit() bool {
	return p.pos < len(p.s) && '0' <= p.s[p.pos] && p.s[p.pos] <= '9'
}

// digits consumes a run of decimal digits, reporting whether there was one.
func (p *v8Parser) digits() bool {
	start := p.pos
	for p.isDigit() {
		p.pos++
	}
	return p.pos > start
}

func isHexDigit(c uint16) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

// reportUnexpectedCharacter reports the character at pos by the token it
// starts.
func (p *v8Parser) reportUnexpectedCharacter() {
	if p.pos == len(p.s) {
		p.report(tokEOS, "")
		return
	}
	p.report(tokenOf(p.s[p.pos]), "")
}

// report is ReportUnexpectedToken: msg, a positioned template, wins over the
// token's own message (LookUpErrorMessageForJsonToken).
func (p *v8Parser) report(t jsonToken, msg string) {
	switch {
	case msg == "" && t == tokEOS:
		p.err = "Unexpected end of JSON input"
		return
	case msg == "" && t == tokNumber:
		msg = msgUnexpectedNumber
	case msg == "" && t == tokString:
		msg = msgUnexpectedString
	case msg == "":
		p.err = p.unexpectedToken()
		return
	}
	line, column := p.location()
	p.err = fmt.Sprintf("%s at position %d (line %d column %d)", msg, p.pos, line, column)
}

// unexpectedToken is the message for an unexpected character, quoting the
// source, or for a long source the context around the character.
func (p *v8Parser) unexpectedToken() string {
	src := p.text(0, len(p.s))
	switch src {
	case "NaN", "Infinity", "undefined", "[object Object]":
		return fmt.Sprintf("%q is not valid JSON", src)
	}
	token := p.text(p.pos, p.pos+1)
	n := len(p.s)
	switch {
	case n < 2*v8ContextCharacters+1:
		return fmt.Sprintf("Unexpected token '%s', \"%s\" is not valid JSON", token, src)
	case p.pos < v8ContextCharacters:
		return fmt.Sprintf("Unexpected token '%s', \"%s\"... is not valid JSON", token, p.text(0, p.pos+v8ContextCharacters))
	case p.pos < n-v8ContextCharacters:
		return fmt.Sprintf("Unexpected token '%s', ...\"%s\"... is not valid JSON", token, p.text(p.pos-v8ContextCharacters, p.pos+v8ContextCharacters))
	}
	return fmt.Sprintf("Unexpected token '%s', ...\"%s\" is not valid JSON", token, p.text(p.pos-v8ContextCharacters, n))
}

func (p *v8Parser) text(from, to int) string {
	return string(utf16.Decode(p.s[from:to]))
}

// location is CalculateFileLocation: the 1-based line and column of pos, with
// "\r\n", "\r" and "\n" each ending a line.
func (p *v8Parser) location() (line, column int) {
	line, lineStart := 1, 0
	for i, c := range p.s[:p.pos] {
		switch c {
		case '\r':
			line, lineStart = line+1, i+1
		case '\n':
			// The '\n' of a "\r\n" ends no second line.
			if i == 0 || p.s[i-1] != '\r' {
				line++
			}
			lineStart = i + 1
		}
	}
	return line, p.pos - lineStart + 1
}
