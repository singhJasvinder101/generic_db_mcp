package sqlguard

import (
	"fmt"
	"strings"
)

type UnsafeQueryError struct {
	Reason string
}

func (e *UnsafeQueryError) Error() string { return e.Reason }

var forbiddenKeywords = map[string]bool{
	"insert": true, "update": true, "delete": true, "drop": true,
	"alter": true, "create": true, "truncate": true, "grant": true,
	"revoke": true, "replace": true, "merge": true, "call": true,
	"exec": true, "execute": true, "vacuum": true, "attach": true,
	"detach": true, "pragma": true, "copy": true, "into": true,
	"reindex": true, "analyze": true, "lock": true, "set": true,
}

func AssertReadOnlySelect(sql string) error {
	tokens, err := tokenize(sql)
	if err != nil {
		return &UnsafeQueryError{Reason: err.Error()}
	}
	if len(tokens) == 0 {
		return &UnsafeQueryError{Reason: "empty query"}
	}

	first := strings.ToLower(tokens[0].text)
	if first != "select" && first != "with" {
		return &UnsafeQueryError{Reason: fmt.Sprintf("only SELECT queries are allowed (statement starts with %q)", tokens[0].text)}
	}

	for _, t := range tokens {
		if t.kind != tokKeywordLike {
			continue
		}
		lower := strings.ToLower(t.text)
		if forbiddenKeywords[lower] {
			return &UnsafeQueryError{Reason: fmt.Sprintf("query contains a disallowed keyword: %q", lower)}
		}
	}

	return nil
}

type tokKind int

const (
	tokKeywordLike tokKind = iota // bare identifier/keyword, outside quotes
	tokOther
	tokStatementSeparator // a top-level semicolon
)

type token struct {
	text string
	kind tokKind
}

func tokenize(sql string) ([]token, error) {
	var tokens []token
	runes := []rune(sql)
	n := len(runes)
	i := 0
	sawTopLevelSemicolon := false

	for i < n {
		c := runes[i]

		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++

		case c == '-' && i+1 < n && runes[i+1] == '-':
			// line comment
			i += 2
			for i < n && runes[i] != '\n' {
				i++
			}

		case c == '/' && i+1 < n && runes[i+1] == '*':
			i += 2
			closed := false
			for i+1 < n {
				if runes[i] == '*' && runes[i+1] == '/' {
					i += 2
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated block comment")
			}

		case c == '\'':
			// string literal, '' is an escaped quote
			i++
			closed := false
			for i < n {
				if runes[i] == '\'' {
					if i+1 < n && runes[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated string literal")
			}

		case c == '"' || c == '`':
			// quoted identifier (ANSI "..." or MySQL/SQLite `...`)
			quote := c
			i++
			closed := false
			for i < n {
				if runes[i] == quote {
					if i+1 < n && runes[i+1] == quote {
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated quoted identifier")
			}

		case c == ';':
			if sawTopLevelSemicolon {
				return nil, fmt.Errorf("multiple statements are not allowed")
			}
			// Only a trailing semicolon (nothing but whitespace/comments
			// after it) is tolerated.
			rest := strings.TrimSpace(stripTrailingComments(string(runes[i+1:])))
			if rest != "" {
				return nil, fmt.Errorf("multiple statements are not allowed")
			}
			sawTopLevelSemicolon = true
			tokens = append(tokens, token{text: ";", kind: tokStatementSeparator})
			i++

		case isIdentStart(c):
			start := i
			for i < n && isIdentPart(runes[i]) {
				i++
			}
			tokens = append(tokens, token{text: string(runes[start:i]), kind: tokKeywordLike})

		default:
			tokens = append(tokens, token{text: string(c), kind: tokOther})
			i++
		}
	}

	return tokens, nil
}

func isIdentStart(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
}

func isIdentPart(r rune) bool {
	return isIdentStart(r) || (r >= '0' && r <= '9')
}

// stripTrailingComments removes leading whitespace/comments so a semicolon
// followed only by "-- trailing comment" isn't mistaken for a second
// statement.
func stripTrailingComments(s string) string {
	s = strings.TrimSpace(s)
	for {
		switch {
		case strings.HasPrefix(s, "--"):
			idx := strings.IndexByte(s, '\n')
			if idx < 0 {
				return ""
			}
			s = strings.TrimSpace(s[idx+1:])
		case strings.HasPrefix(s, "/*"):
			idx := strings.Index(s, "*/")
			if idx < 0 {
				return s
			}
			s = strings.TrimSpace(s[idx+2:])
		default:
			return s
		}
	}
}
