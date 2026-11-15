package main

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// cordumctl reads and writes .env by the rules Docker Compose applies to a
// project's .env (compose-spec/compose-go's dotenv parser), so the CLI sends the
// credential that Compose hands to the Gateway.
var (
	dotEnvEscape = regexp.MustCompile(`\\(?:[abcfnrtv$"\\]|0[0-9]{0,3})`)
	dotEnvName   = regexp.MustCompile(`^[_A-Za-z][_A-Za-z0-9]*`)
	dotEnvPlain  = regexp.MustCompile(`^[A-Za-z0-9_./:@%+,=-]*$`)
	dotEnvQuoter = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "$", "$$")
)

// parseDotEnv parses .env content as Compose does. Lines starting with # are
// skipped, and a statement may start with "export ". A key (letters, digits and
// _ . - [ ]) ends at = or :, and a key alone on its line takes lookup's value.
// An unquoted value runs to the end of the line, drops a " #" comment and is
// interpolated. A single-quoted value is literal apart from \'. A double-quoted
// value takes the \a \b \f \n \r \t \v \\ \" \$ and \0nnn escapes, then is interpolated.
// A later definition of a key wins. Interpolation reads lookup, then the keys
// defined earlier. Errors name a line and a key, never a value.
func parseDotEnv(content string, lookup func(string) (string, bool)) (map[string]string, error) {
	out := map[string]string{}
	resolve := func(name string) (string, bool) {
		if v, ok := lookup(name); ok {
			return v, true
		}
		v, ok := out[name]
		return v, ok
	}
	src := strings.TrimPrefix(content, "\uFEFF")
	rest := src
	for {
		rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
		if rest == "" {
			return out, nil
		}
		if rest[0] == '#' {
			_, rest, _ = strings.Cut(rest, "\n")
			continue
		}
		line := 1 + strings.Count(src[:len(src)-len(rest)], "\n")
		key, value, inherited, next, err := parseDotEnvStatement(rest, resolve)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		rest = next
		if !inherited {
			out[key] = value
		} else if v, ok := lookup(key); ok {
			out[key] = v
		}
	}
}

// parseDotEnvStatement reads the statement at the start of src.
func parseDotEnvStatement(src string, resolve func(string) (string, bool)) (key, value string, inherited bool, rest string, err error) {
	if after, ok := strings.CutPrefix(src, "export"); ok && after != "" && strings.ContainsRune(" \t\n\f\r", rune(after[0])) {
		src = strings.TrimLeftFunc(after, isDotEnvSpace)
	}
	end := len(src)
	for i, r := range src {
		if r == '=' || r == ':' || r == '\n' {
			end = i
			break
		}
		if !isDotEnvSpace(r) && !unicode.IsLetter(r) && !unicode.IsNumber(r) && !strings.ContainsRune("_.-[]", r) {
			return "", "", false, "", fmt.Errorf("unexpected character %q in a variable name", r)
		}
	}
	key = strings.TrimRightFunc(src[:end], unicode.IsSpace)
	switch {
	case key == "":
		return "", "", false, "", errors.New("key cannot be empty")
	case strings.Contains(key, " "):
		return "", "", false, "", fmt.Errorf("key %q cannot contain a space", key)
	case end == len(src):
		return key, "", true, "", nil
	case src[end] == '\n':
		return key, "", true, src[end+1:], nil
	}
	value, rest, err = parseDotEnvValue(strings.TrimLeftFunc(src[end+1:], isDotEnvSpace), resolve)
	if err != nil {
		return "", "", false, "", fmt.Errorf("%s: %w", key, err)
	}
	return key, value, false, rest, nil
}

// parseDotEnvValue reads the value at the start of src.
func parseDotEnvValue(src string, resolve func(string) (string, bool)) (string, string, error) {
	if src == "" || (src[0] != '"' && src[0] != '\'') {
		value, rest, _ := strings.Cut(src, "\n")
		value, _, _ = strings.Cut(value, " #")
		value, err := interpolateDotEnv(strings.TrimRightFunc(value, unicode.IsSpace), resolve)
		return value, rest, err
	}
	quote := src[0]
	var b strings.Builder
	escaped := false
	for i := 1; i < len(src); i++ {
		c := src[i]
		switch {
		case c == quote && !escaped:
			if quote == '\'' {
				return b.String(), src[i+1:], nil
			}
			value, err := interpolateDotEnv(expandDotEnvEscapes(b.String()), resolve)
			return value, src[i+1:], err
		case c == quote:
			escaped = false
			b.WriteByte(c)
		case escaped:
			escaped = false
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == '\\':
			escaped = true
		default:
			b.WriteByte(c)
		}
	}
	return "", "", errors.New("unterminated quoted value")
}

// expandDotEnvEscapes applies a double-quoted value's escapes; \$ becomes $$.
func expandDotEnvEscapes(s string) string {
	return dotEnvEscape.ReplaceAllStringFunc(s, func(m string) string {
		if m == `\$` {
			return "$$"
		}
		if strings.HasPrefix(m, `\0`) {
			m = `\` + m[2:]
		}
		r, _, _, err := strconv.UnquoteChar(m, '"')
		if err != nil {
			return m
		}
		return string(r)
	})
}

// interpolateDotEnv expands $$, $NAME and ${NAME}, with the :- - :? ? :+ +
// forms, as Compose does. An unset NAME expands to "", and a $ that starts none
// of these stays as it is.
func interpolateDotEnv(s string, resolve func(string) (string, bool)) (string, error) {
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '$')
		if i < 0 {
			b.WriteString(s)
			return b.String(), nil
		}
		b.WriteString(s[:i])
		s = s[i+1:]
		switch name := dotEnvName.FindString(s); {
		case strings.HasPrefix(s, "$"):
			b.WriteByte('$')
			s = s[1:]
		case strings.HasPrefix(s, "{"):
			end := dotEnvBraceEnd(s)
			if end < 0 {
				return "", errors.New("unterminated ${ in a value")
			}
			v, err := interpolateDotEnvBraced(s[1:end], resolve)
			if err != nil {
				return "", err
			}
			b.WriteString(v)
			s = s[end+1:]
		case name != "":
			v, _ := resolve(name)
			b.WriteString(v)
			s = s[len(name):]
		default:
			b.WriteByte('$')
		}
	}
}

// interpolateDotEnvBraced expands the inside of one ${...}.
func interpolateDotEnvBraced(expr string, resolve func(string) (string, bool)) (string, error) {
	name := dotEnvName.FindString(expr)
	colon := strings.HasPrefix(expr[len(name):], ":")
	op := strings.TrimPrefix(expr[len(name):], ":")
	if name == "" || (op == "" && colon) {
		return "", errors.New("invalid ${...} interpolation")
	}
	val, set := resolve(name)
	if op == "" {
		return val, nil
	}
	present := set && (val != "" || !colon)
	switch op[0] {
	case '-':
		if present {
			return val, nil
		}
		return interpolateDotEnv(op[1:], resolve)
	case '?':
		if present {
			return val, nil
		}
		return "", fmt.Errorf("required variable %s is missing a value", name)
	case '+':
		if present {
			return interpolateDotEnv(op[1:], resolve)
		}
		return "", nil
	}
	return "", fmt.Errorf("invalid interpolation of %s", name)
}

// dotEnvBraceEnd returns the index of the brace closing the one s starts with, or -1.
func dotEnvBraceEnd(s string) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// dotEnvValue returns v as a .env value that Compose and parseDotEnv read back
// unchanged: as it is when it holds only letters, digits and _ . / : @ % + , = -,
// else double-quoted, with \, ", line breaks and carriage returns escaped and $ doubled.
func dotEnvValue(v string) string {
	if dotEnvPlain.MatchString(v) {
		return v
	}
	return `"` + dotEnvQuoter.Replace(v) + `"`
}

// isDotEnvSpace is Compose's in-line space: a unicode space other than \n.
func isDotEnvSpace(r rune) bool { return strings.ContainsRune("\t\v\f\r \u0085\u00a0", r) }
