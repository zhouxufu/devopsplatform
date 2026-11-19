package deploy

import "fmt"

// sqlChangeWithoutComments removes only inert MySQL comments outside quoted
// values/identifiers. Spaces preserve token boundaries and original newlines.
// Never feed the raw commented source to the executor after validating this text.
func sqlChangeWithoutComments(source string) (string, error) {
	out := []byte(source)
	blank := func(start, end int) {
		for j := start; j < end; j++ {
			if out[j] != '\r' && out[j] != '\n' {
				out[j] = ' '
			}
		}
	}
	for i := 0; i < len(source); {
		b := source[i]
		if b == '\'' || b == '`' || b == '"' {
			quote := b
			i++
			for i < len(source) {
				if source[i] == '\\' && i+1 < len(source) {
					i += 2
					continue
				}
				if source[i] == quote {
					i++
					if i < len(source) && source[i] == quote {
						i++
						continue
					}
					break
				}
				i++
			}
			continue
		}
		line := b == '#' || (b == '-' && i+1 < len(source) && source[i+1] == '-' &&
			(i+2 == len(source) || source[i+2] <= ' ' || source[i+2] == 0x7f))
		if line {
			start := i
			for i < len(source) && source[i] != '\n' && source[i] != '\r' {
				i++
			}
			blank(start, i)
			continue
		}
		if b == '/' && i+1 < len(source) && source[i+1] == '*' {
			start := i
			i += 2
			if i < len(source) && (source[i] == '!' || source[i] == '+' ||
				((source[i] == 'M' || source[i] == 'm') && i+1 < len(source) && source[i+1] == '!')) {
				return "", fmt.Errorf("SQL 版本注释或优化器提示可能影响执行，暂不支持就绪或执行；普通说明注释可以使用，原文可保存草稿")
			}
			closed := false
			for i+1 < len(source) {
				if source[i] == '/' && source[i+1] == '*' {
					return "", fmt.Errorf("SQL 不支持嵌套块注释，请拆成独立说明注释")
				}
				if source[i] == '*' && source[i+1] == '/' {
					i += 2
					closed = true
					break
				}
				i++
			}
			if !closed {
				return "", fmt.Errorf("SQL 块注释未闭合，请补齐 */")
			}
			blank(start, i)
			continue
		}
		i++
	}
	return string(out), nil
}
