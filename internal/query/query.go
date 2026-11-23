// Package query parses and evaluates task predicates without CLI or Git access.
package query

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Record struct {
	Status  string
	Title   string
	Updated time.Time
}

type Expression struct {
	root         node
	needsUpdated bool
}

func (e *Expression) NeedsUpdated() bool    { return e != nil && e.needsUpdated }
func (e *Expression) Matches(r Record) bool { return e == nil || e.root.matches(r) }

type node interface {
	matches(Record) bool
}

type statusIs string

func (n statusIs) matches(r Record) bool { return r.Status == string(n) }

type titleContains string

func (n titleContains) matches(r Record) bool {
	return strings.Contains(strings.ToLower(r.Title), string(n))
}

type not struct{ inner node }

func (n not) matches(r Record) bool { return !n.inner.matches(r) }

type all []node

func (n all) matches(r Record) bool {
	for _, child := range n {
		if !child.matches(r) {
			return false
		}
	}
	return true
}

type anyOf []node

func (n anyOf) matches(r Record) bool {
	for _, child := range n {
		if child.matches(r) {
			return true
		}
	}
	return false
}

type token struct {
	kind byte // 'w' (word), '"' (quoted value), '!', ':', '(', ')', or zero (EOF)
	text string
	pos  int
}

type lexer struct {
	source string
	pos    int
}

func (l *lexer) next(value bool) token {
	for l.pos < len(l.source) {
		r, size := utf8.DecodeRuneInString(l.source[l.pos:])
		if !unicode.IsSpace(r) {
			break
		}
		l.pos += size
	}
	start := l.pos
	if start == len(l.source) {
		return token{pos: start}
	}
	switch ch := l.source[l.pos]; ch {
	case ':':
		if value {
			break
		}
		fallthrough
	case '(', ')':
		l.pos++
		return token{kind: ch, pos: start}
	case '"':
		l.pos++
		var value strings.Builder
		for l.pos < len(l.source) {
			ch := l.source[l.pos]
			l.pos++
			switch ch {
			case '"':
				return token{kind: '"', text: value.String(), pos: start}
			case '\\':
				if l.pos < len(l.source) && (l.source[l.pos] == '"' || l.source[l.pos] == '\\') {
					ch = l.source[l.pos]
					l.pos++
				}
			}
			value.WriteByte(ch)
		}
		return token{kind: '!', text: "unterminated quoted value", pos: start}
	}
	for l.pos < len(l.source) {
		r, size := utf8.DecodeRuneInString(l.source[l.pos:])
		if unicode.IsSpace(r) || strings.ContainsRune("()\"", r) || (!value && r == ':') {
			break
		}
		l.pos += size
	}
	return token{kind: 'w', text: l.source[start:l.pos], pos: start}
}

type parser struct {
	lexer        lexer
	current      token
	now          time.Time
	needsUpdated bool
	validStatus  func(string) bool
}

// Parse binds all date values to the same clock. validStatus supplies the
// application's vocabulary for exact status predicates.
func Parse(source string, now time.Time, validStatus func(string) bool) (*Expression, error) {
	p := parser{lexer: lexer{source: source}, now: now.UTC(), validStatus: validStatus}
	p.advance()
	root, err := p.disjunction(0)
	if err != nil {
		return nil, err
	}
	if p.current.kind != 0 {
		return nil, p.error("expected AND, OR, or end of query")
	}
	return &Expression{root: root, needsUpdated: p.needsUpdated}, nil
}

func (p *parser) advance()                 { p.current = p.lexer.next(false) }
func (p *parser) keyword(word string) bool { return p.current.kind == 'w' && p.current.text == word }

func at(pos int, message string) error {
	return fmt.Errorf("query at byte %d: %s", pos+1, message)
}

func (p *parser) error(message string) error {
	if p.current.kind == '!' {
		message = p.current.text
	}
	return at(p.current.pos, message)
}

func (p *parser) disjunction(depth int) (node, error) {
	first, err := p.conjunction(depth)
	if err != nil || !p.keyword("OR") {
		return first, err
	}
	nodes := anyOf{first}
	for p.keyword("OR") {
		p.advance()
		next, err := p.conjunction(depth)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, next)
	}
	return nodes, nil
}

func (p *parser) conjunction(depth int) (node, error) {
	first, err := p.unary(depth)
	if err != nil || !p.keyword("AND") {
		return first, err
	}
	nodes := all{first}
	for p.keyword("AND") {
		p.advance()
		next, err := p.unary(depth)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, next)
	}
	return nodes, nil
}

func (p *parser) unary(depth int) (node, error) {
	if depth > 256 {
		return nil, p.error("query nesting is too deep")
	}
	if p.keyword("NOT") {
		p.advance()
		n, err := p.unary(depth + 1)
		return not{n}, err
	}
	if p.current.kind == '(' {
		p.advance()
		n, err := p.disjunction(depth + 1)
		if err != nil {
			return nil, err
		}
		if p.current.kind != ')' {
			return nil, p.error("expected AND, OR, or closing parenthesis")
		}
		p.advance()
		return n, nil
	}
	return p.predicate()
}

func (p *parser) predicate() (node, error) {
	field := p.current
	if field.kind != 'w' {
		return nil, p.error("expected a key:value predicate, NOT, or opening parenthesis")
	}
	switch field.text {
	case "status", "title", "updated":
	default:
		return nil, at(field.pos, fmt.Sprintf("unknown field %q", field.text))
	}
	p.advance()
	if p.current.kind != ':' || p.current.pos != field.pos+len(field.text) {
		return nil, p.error("expected ':' immediately after field name")
	}
	colon := p.current.pos
	p.current = p.lexer.next(true)
	value := p.current
	if (value.kind != 'w' && value.kind != '"') || value.pos != colon+1 || value.text == "" {
		return nil, p.error("expected a nonempty value immediately after ':'")
	}
	var n node
	switch field.text {
	case "status":
		if !p.validStatus(value.text) {
			return nil, p.error(fmt.Sprintf("invalid status %q", value.text))
		}
		n = statusIs(value.text)
	case "title":
		n = titleContains(strings.ToLower(value.text))
	case "updated":
		test, err := parseUpdated(value.text, p.now)
		if err != nil {
			return nil, p.error(err.Error())
		}
		p.needsUpdated = true
		n = test
	}
	p.advance()
	return n, nil
}
