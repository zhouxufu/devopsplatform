package server

import (
	"fmt"
	"strings"
	"unicode"

	"logplatform/internal/types"
)

// LogQuery is the shared query representation used by history search, Live,
// alert counting and API/MCP callers. Keeping one AST prevents each entry point
// from gradually acquiring different filtering semantics.
type LogQuery struct {
	root logExpr
}

type logExpr interface{ logExpr() }

type logTerm struct {
	value  string
	phrase bool
	prefix bool
}
type logField struct {
	name  string
	child logExpr
}
type logAnd struct{ left, right logExpr }
type logOr struct{ left, right logExpr }
type logNot struct{ child logExpr }

func (logTerm) logExpr()  {}
func (logField) logExpr() {}
func (logAnd) logExpr()   {}
func (logOr) logExpr()    {}
func (logNot) logExpr()   {}

type QuerySyntaxError struct{ Message string }

func (e *QuerySyntaxError) Error() string { return "查询语法错误：" + e.Message }

func queryError(format string, args ...any) error {
	return &QuerySyntaxError{Message: fmt.Sprintf(format, args...)}
}

type queryTokenKind uint8

const (
	tokenEOF queryTokenKind = iota
	tokenWord
	tokenPhrase
	tokenLParen
	tokenRParen
	tokenColon
	tokenMinus
	tokenAnd
	tokenOr
	tokenNot
)

type queryToken struct {
	kind queryTokenKind
	text string
	pos  int
}

func lexLogQuery(input string) ([]queryToken, error) {
	if len(input) > 1000 {
		return nil, queryError("查询内容不能超过 1000 个字符")
	}
	var out []queryToken
	for i := 0; i < len(input); {
		if unicode.IsSpace(rune(input[i])) {
			i++
			continue
		}
		pos := i
		switch input[i] {
		case '(':
			out = append(out, queryToken{kind: tokenLParen, text: "(", pos: pos})
			i++
		case ')':
			out = append(out, queryToken{kind: tokenRParen, text: ")", pos: pos})
			i++
		case ':':
			out = append(out, queryToken{kind: tokenColon, text: ":", pos: pos})
			i++
		case '-':
			out = append(out, queryToken{kind: tokenMinus, text: "-", pos: pos})
			i++
		case '"':
			i++
			var b strings.Builder
			closed := false
			for i < len(input) {
				if input[i] == '"' {
					i++
					closed = true
					break
				}
				if input[i] == '\\' && i+1 < len(input) {
					i++
				}
				b.WriteByte(input[i])
				i++
			}
			if !closed {
				return nil, queryError("第 %d 个字符开始的双引号没有闭合", pos+1)
			}
			if b.Len() == 0 {
				return nil, queryError("精确短语不能为空")
			}
			out = append(out, queryToken{kind: tokenPhrase, text: b.String(), pos: pos})
		default:
			for i < len(input) && !unicode.IsSpace(rune(input[i])) && !strings.ContainsRune(`():"`, rune(input[i])) {
				i++
			}
			word := input[pos:i]
			kind := tokenWord
			switch word {
			case "AND":
				kind = tokenAnd
			case "OR":
				kind = tokenOr
			case "NOT":
				kind = tokenNot
			}
			out = append(out, queryToken{kind: kind, text: word, pos: pos})
		}
		if len(out) > 128 {
			return nil, queryError("查询条件过多，最多允许 128 个语法单元")
		}
	}
	out = append(out, queryToken{kind: tokenEOF, pos: len(input)})
	return out, nil
}

type logQueryParser struct {
	tokens []queryToken
	index  int
}

func ParseLogQuery(input string) (*LogQuery, error) {
	if strings.TrimSpace(input) == "" {
		return &LogQuery{}, nil
	}
	tokens, err := lexLogQuery(input)
	if err != nil {
		return nil, err
	}
	p := &logQueryParser{tokens: tokens}
	root, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if tok := p.peek(); tok.kind != tokenEOF {
		return nil, queryError("第 %d 个字符附近存在多余内容 %q", tok.pos+1, tok.text)
	}
	return &LogQuery{root: root}, nil
}

func (p *logQueryParser) peek() queryToken { return p.tokens[p.index] }
func (p *logQueryParser) take() queryToken {
	t := p.peek()
	p.index++
	return t
}

func (p *logQueryParser) parseOr() (logExpr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tokenOr {
		p.take()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = logOr{left: left, right: right}
	}
	return left, nil
}

func startsQueryOperand(kind queryTokenKind) bool {
	return kind == tokenWord || kind == tokenPhrase || kind == tokenLParen || kind == tokenMinus || kind == tokenNot
}

func (p *logQueryParser) parseAnd() (logExpr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		explicit := p.peek().kind == tokenAnd
		if explicit {
			p.take()
		} else if !startsQueryOperand(p.peek().kind) {
			break
		}
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = logAnd{left: left, right: right}
	}
	return left, nil
}

func (p *logQueryParser) parseUnary() (logExpr, error) {
	if p.peek().kind == tokenMinus || p.peek().kind == tokenNot {
		p.take()
		child, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return logNot{child: child}, nil
	}
	return p.parsePrimary()
}

var queryFields = map[string]string{
	"service": "service", "app": "service",
	"env": "environment", "environment": "environment",
	"host": "host", "level": "level", "path": "path",
	"trace": "trace_id", "trace_id": "trace_id", "message": "message",
}

func (p *logQueryParser) parsePrimary() (logExpr, error) {
	tok := p.peek()
	if tok.kind == tokenLParen {
		p.take()
		if p.peek().kind == tokenRParen {
			return nil, queryError("第 %d 个字符处的括号不能为空", tok.pos+1)
		}
		node, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tokenRParen {
			return nil, queryError("第 %d 个字符开始的括号没有闭合", tok.pos+1)
		}
		p.take()
		return node, nil
	}
	if tok.kind != tokenWord && tok.kind != tokenPhrase {
		if tok.kind == tokenEOF {
			return nil, queryError("查询条件不完整")
		}
		return nil, queryError("第 %d 个字符附近不能使用 %q", tok.pos+1, tok.text)
	}
	p.take()
	if tok.kind == tokenWord && p.peek().kind == tokenColon {
		p.take()
		field, ok := queryFields[strings.ToLower(tok.text)]
		if !ok {
			return nil, queryError("不支持字段 %q，可用字段：service、env、host、level、path、trace、message", tok.text)
		}
		child, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		return logField{name: field, child: child}, nil
	}
	term := logTerm{value: tok.text, phrase: tok.kind == tokenPhrase}
	if !term.phrase {
		if strings.Contains(term.value, "?") {
			return nil, queryError("暂不支持 ? 通配符，请使用完整词或后缀 *")
		}
		if n := strings.Count(term.value, "*"); n > 0 {
			if n != 1 || !strings.HasSuffix(term.value, "*") || len(term.value) == 1 {
				return nil, queryError("只支持后缀通配符，例如 timeout*；不支持 *timeout 或 *timeout*")
			}
			term.prefix = true
			term.value = strings.TrimSuffix(term.value, "*")
		}
	}
	return term, nil
}

func (q *LogQuery) Match(h types.Hit) bool {
	return q == nil || q.root == nil || matchLogExpr(q.root, h, "")
}

func matchLogExpr(expr logExpr, h types.Hit, scopedField string) bool {
	switch node := expr.(type) {
	case logAnd:
		return matchLogExpr(node.left, h, scopedField) && matchLogExpr(node.right, h, scopedField)
	case logOr:
		return matchLogExpr(node.left, h, scopedField) || matchLogExpr(node.right, h, scopedField)
	case logNot:
		return !matchLogExpr(node.child, h, scopedField)
	case logField:
		return matchLogExpr(node.child, h, node.name)
	case logTerm:
		field := scopedField
		if field == "" {
			field = "message"
		}
		value := hitField(h, field)
		if field != "message" {
			if node.prefix {
				return strings.HasPrefix(strings.ToLower(value), strings.ToLower(node.value))
			}
			return strings.EqualFold(value, node.value)
		}
		if node.phrase {
			return strings.Contains(strings.ToLower(value), strings.ToLower(node.value))
		}
		return matchTokenPhrase(value, node.value, node.prefix)
	default:
		return false
	}
}

func hitField(h types.Hit, field string) string {
	switch field {
	case "service":
		return h.Service
	case "environment":
		return h.Environment
	case "host":
		return h.Host
	case "level":
		return h.Level
	case "path":
		return h.Path
	case "trace_id":
		return h.TraceID
	default:
		return h.Message
	}
}

func searchTokens(s string) []string {
	parts := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	return parts
}

func matchTokenPhrase(message, query string, prefix bool) bool {
	messageTokens, queryTokens := searchTokens(message), searchTokens(query)
	if len(queryTokens) == 0 || len(queryTokens) > len(messageTokens) {
		return false
	}
	for i := 0; i+len(queryTokens) <= len(messageTokens); i++ {
		matched := true
		for j := range queryTokens {
			if prefix && j == len(queryTokens)-1 {
				matched = strings.HasPrefix(messageTokens[i+j], queryTokens[j])
			} else {
				matched = messageTokens[i+j] == queryTokens[j]
			}
			if !matched {
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}
