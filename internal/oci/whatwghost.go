package oci

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/idna"
)

// The reference CLI parses registry and auth-host authorities with Node's
// WHATWG URL parser (new URL(...)), whose host rules differ from net/url's:
// it drops the scheme's default port, lower-cases and IDNA-maps domains,
// normalizes IPv4 and IPv6 literals, and tolerates a path that resolves to "/"
// or an empty query or fragment after the authority. This file ports the parts
// of https://url.spec.whatwg.org/ that decide the `host` of an http(s) URL.

var errInvalidHost = errors.New("invalid WHATWG host")

func whatwgHost(hostport, defaultPort string) (string, error) {
	host, port, err := splitWHATWGHostPort(hostport, defaultPort)
	if err != nil {
		return "", err
	}
	if port != "" {
		return host + ":" + port, nil
	}
	return host, nil
}

func splitWHATWGHostPort(hostport, defaultPort string) (host, port string, err error) {
	rawHost, rawPort, inBrackets := hostport, "", false
	for i, c := range hostport {
		if c == '[' {
			inBrackets = true
		} else if c == ']' {
			inBrackets = false
		} else if c == ':' && !inBrackets {
			rawHost, rawPort = hostport[:i], hostport[i+1:]
			break
		}
	}
	port, err = parseWHATWGPort(rawPort, defaultPort)
	if err != nil {
		return "", "", err
	}
	if rawHost == "" {
		return "", "", errInvalidHost
	}
	host, err = parseWHATWGHost(rawHost)
	if err != nil {
		return "", "", err
	}
	return host, port, nil
}

func parseWHATWGPort(s, defaultPort string) (string, error) {
	if s == "" {
		return "", nil
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return "", errInvalidHost
		}
	}
	// Digits only, so Atoi fails only out of range, returning the largest int.
	n, _ := strconv.Atoi(s)
	if n > 65535 {
		return "", errInvalidHost
	}
	port := strconv.Itoa(n)
	if port == defaultPort {
		return "", nil
	}
	return port, nil
}

func parseWHATWGHost(input string) (string, error) {
	if strings.HasPrefix(input, "[") {
		if !strings.HasSuffix(input, "]") {
			return "", errInvalidHost
		}
		return parseWHATWGIPv6(input[1 : len(input)-1])
	}
	domain, err := domainToASCII(percentDecodeUTF8(input))
	if err != nil {
		return "", err
	}
	if strings.ContainsFunc(domain, isForbiddenDomainCodePoint) {
		return "", errInvalidHost
	}
	if endsInANumber(domain) {
		return parseWHATWGIPv4(domain)
	}
	return domain, nil
}

func percentDecodeUTF8(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if v, err := hex.DecodeString(s[i+1 : i+3]); err == nil {
				b = append(b, v[0])
				i += 2
				continue
			}
		}
		b = append(b, s[i])
	}
	return strings.ToValidUTF8(string(b), string(utf8.RuneError))
}

// whatwgIDNA is UTS #46 ToASCII with the flags WHATWG domain to ASCII passes:
// CheckHyphens, UseSTD3ASCIIRules, Transitional_Processing and VerifyDnsLength
// off, CheckBidi and CheckJoiners on.
var whatwgIDNA = idna.New(
	idna.MapForLookup(),
	idna.Transitional(false),
	idna.CheckHyphens(false),
	idna.CheckJoiners(true),
	idna.BidiRule(),
	idna.StrictDomainName(false),
	idna.VerifyDNSLength(false),
)

// domainToASCII is WHATWG domain to ASCII (beStrict false) as Node.js 24 (the
// version versions.yaml pins for the oracle) runs it: a pure-ASCII domain is
// only lower-cased, its "xn--" labels not checked as Punycode.
func domainToASCII(domain string) (string, error) {
	if isASCII(domain) {
		return strings.ToLower(domain), nil
	}
	out, err := whatwgIDNA.ToASCII(domain)
	if err != nil || out == "" {
		return "", errInvalidHost
	}
	return out, nil
}

func isASCII(s string) bool {
	for _, r := range s {
		if r >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func isForbiddenDomainCodePoint(r rune) bool {
	return r <= 0x20 || r == 0x7f || strings.ContainsRune("#%/:<>?@[\\]^|", r)
}

func endsInANumber(domain string) bool {
	labels := ipv4Parts(domain)
	last := labels[len(labels)-1]
	if last != "" && strings.Trim(last, "0123456789") == "" {
		return true
	}
	_, err := parseIPv4Number(last)
	return err == nil
}

func parseIPv4Number(s string) (uint64, error) {
	if s == "" {
		return 0, errInvalidHost
	}
	base := 10
	switch {
	case len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X"):
		s, base = s[2:], 16
		if s == "" {
			return 0, nil
		}
	case len(s) >= 2 && s[0] == '0':
		s, base = s[1:], 8
	}
	n, err := strconv.ParseUint(s, base, 64)
	if errors.Is(err, strconv.ErrSyntax) {
		return 0, errInvalidHost
	}
	// Out of range, ParseUint returns the largest uint64: every digit is
	// valid and the value exceeds any IPv4 part, as WHATWG has it.
	return n, nil
}

func ipv4Parts(domain string) []string {
	return strings.Split(strings.TrimSuffix(domain, "."), ".")
}

func parseWHATWGIPv4(s string) (string, error) {
	parts := ipv4Parts(s)
	if len(parts) > 4 {
		return "", errInvalidHost
	}
	numbers := make([]uint64, len(parts))
	for i, part := range parts {
		n, err := parseIPv4Number(part)
		if err != nil {
			return "", err
		}
		if i < len(parts)-1 && n > 255 {
			return "", errInvalidHost
		}
		numbers[i] = n
	}
	last := numbers[len(numbers)-1]
	if last >= 1<<(8*(5-len(numbers))) {
		return "", errInvalidHost
	}
	ipv4 := last
	for i, n := range numbers[:len(numbers)-1] {
		ipv4 += n << (8 * (3 - i))
	}
	return fmt.Sprintf("%d.%d.%d.%d", byte(ipv4>>24), byte(ipv4>>16), byte(ipv4>>8), byte(ipv4)), nil
}

func parseWHATWGIPv6(s string) (string, error) {
	addr, err := netip.ParseAddr(s)
	if err != nil || !addr.Is6() || addr.Zone() != "" {
		return "", errInvalidHost
	}
	b := addr.As16()
	var pieces [8]uint16
	for i := range pieces {
		pieces[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
	}
	var start, length int
	for i := range pieces {
		n := 0
		for i+n < len(pieces) && pieces[i+n] == 0 {
			n++
		}
		if n > length {
			start, length = i, n
		}
	}
	compress := length >= 2
	var out strings.Builder
	out.WriteByte('[')
	for i := 0; i < len(pieces); i++ {
		if compress && i == start {
			out.WriteString("::")
			i += length - 1
			continue
		}
		if i > 0 && (!compress || i != start+length) {
			out.WriteByte(':')
		}
		out.WriteString(strconv.FormatUint(uint64(pieces[i]), 16))
	}
	out.WriteByte(']')
	return out.String(), nil
}

var tabOrNewline = strings.NewReplacer("\t", "", "\n", "", "\r", "")

func parseHTTPSAuthority(authority string) (string, error) {
	s := strings.TrimRightFunc(authority, func(r rune) bool { return r <= 0x20 })
	s = tabOrNewline.Replace(s)
	s = strings.TrimLeft(s, `/\`)
	auth, rest := s, ""
	if end := strings.IndexAny(s, `/\?#`); end != -1 {
		auth, rest = s[:end], s[end:]
	}
	if at := strings.LastIndex(auth, "@"); at >= 0 {
		if userinfo := auth[:at]; userinfo != "" && userinfo != ":" {
			return "", errInvalidHost
		}
		auth = auth[at+1:]
	}
	if !resolvesToRootPath(rest) {
		return "", errInvalidHost
	}
	return whatwgHost(auth, "443")
}

func resolvesToRootPath(rest string) bool {
	path := rest
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		path = rest[:i]
		switch tail := rest[i:]; tail {
		case "?", "#", "?#":
		default:
			return false
		}
	}
	if path != "" {
		path = path[1:]
	}
	var segments []bool
	for i, last := 0, false; !last; {
		j := strings.IndexAny(path[i:], `/\`)
		var segment string
		if j == -1 {
			segment, last = path[i:], true
		} else {
			segment = path[i : i+j]
			i += j + 1
		}
		switch {
		case isDoubleDotSegment(segment):
			if len(segments) > 0 {
				segments = segments[:len(segments)-1]
			}
			if last {
				segments = append(segments, true)
			}
		case isSingleDotSegment(segment):
			if last {
				segments = append(segments, true)
			}
		default:
			segments = append(segments, segment == "")
		}
	}
	return len(segments) == 1 && segments[0]
}

func isSingleDotSegment(s string) bool {
	return s == "." || strings.EqualFold(s, "%2e")
}

func isDoubleDotSegment(s string) bool {
	switch strings.ToLower(s) {
	case "..", ".%2e", "%2e.", "%2e%2e":
		return true
	}
	return false
}
