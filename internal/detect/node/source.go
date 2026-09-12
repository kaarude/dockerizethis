package node

import (
	"strconv"
	"strings"
)

type tokenKind byte

const (
	tokenIdentifier tokenKind = iota
	tokenNumber
	tokenString
	tokenOpaque
	tokenPunctuation
)

type sourceToken struct {
	text string
	kind tokenKind
	line int
}

// sourceTokens recognizes enough JavaScript to inspect direct calls. Ambiguous
// regex/division syntax and interpolated templates disable inference for the file;
// this is deliberately not a JavaScript parser.
func sourceTokens(source string) ([]sourceToken, bool) {
	var tokens []sourceToken
	line := 1
	for i := 0; i < len(source); {
		c := source[i]
		switch {
		case c == '\n':
			line++
			i++
		case c == ' ' || c == '\r' || c == '\t':
			i++
		case strings.HasPrefix(source[i:], "//"):
			for i < len(source) && source[i] != '\n' {
				i++
			}
		case strings.HasPrefix(source[i:], "/*"):
			end := strings.Index(source[i+2:], "*/")
			if end < 0 {
				return tokens, false
			}
			end += i + 4
			line += strings.Count(source[i:end], "\n")
			i = end
		case c == '\'' || c == '"' || c == '`':
			start, firstLine := i, line
			i++
			escaped := false
			for i < len(source) && source[i] != c {
				if c == '`' && strings.HasPrefix(source[i:], "${") {
					return tokens, false
				}
				if source[i] == '\\' {
					escaped = true
					i++
					if i >= len(source) {
						return tokens, false
					}
				}
				if source[i] == '\n' {
					line++
				}
				i++
			}
			if i == len(source) {
				return tokens, false
			}
			value := source[start+1 : i]
			i++
			// Keep opaque literals as tokens so adjacent code cannot join across them.
			kind := tokenString
			if escaped || c == '`' {
				kind = tokenOpaque
			}
			tokens = append(tokens, sourceToken{value, kind, firstLine})
		case c == '/' || c >= 128:
			return tokens, false
		case identifierByte(c) || c >= '0' && c <= '9':
			start := i
			kind := tokenIdentifier
			if c >= '0' && c <= '9' {
				kind = tokenNumber
			}
			for i < len(source) && (identifierByte(source[i]) || source[i] >= '0' && source[i] <= '9' || kind == tokenNumber && source[i] == '.') {
				i++
			}
			tokens = append(tokens, sourceToken{source[start:i], kind, line})
		default:
			tokens = append(tokens, sourceToken{string(c), tokenPunctuation, line})
			i++
		}
	}
	return tokens, true
}

func identifierByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == '$'
}

func tokenSequence(tokens []sourceToken, values ...string) bool {
	if len(tokens) < len(values) {
		return false
	}
	for i, value := range values {
		if tokens[i].text != value || tokens[i].kind == tokenString || tokens[i].kind == tokenOpaque {
			return false
		}
	}
	return true
}

// directWebCalls only accepts a single const-bound framework app at top level.
// Imported routers and nested factories need runtime configuration to resolve.
func directWebCalls(source, framework string) (int, string) {
	if framework != "express" && framework != "fastify" && framework != "koa" {
		return 0, ""
	}
	tokens, complete := sourceTokens(source)
	if !complete {
		return 0, ""
	}
	factories := make(map[string]bool)
	apps := make(map[string]bool)
	depth := 0
	for i, token := range tokens {
		rest := tokens[i:]
		if depth == 0 {
			if len(rest) >= 4 && tokenSequence(rest, "import") && rest[1].kind == tokenIdentifier && rest[2].text == "from" && rest[3].kind == tokenString && rest[3].text == framework {
				factories[rest[1].text] = true
			}
			if len(rest) >= 8 && tokenSequence(rest, "const") && rest[1].kind == tokenIdentifier && tokenSequence(rest[2:], "=", "require", "(") && rest[5].kind == tokenString && rest[5].text == framework && tokenSequence(rest[6:], ")") {
				if tokenSequence(rest[7:], "(", ")") && bindingEnds(rest, 9) {
					apps[rest[1].text] = true
				} else if rest[7].text == ";" || rest[7].line > rest[6].line {
					factories[rest[1].text] = true
				}
			}
			if len(rest) >= 6 && tokenSequence(rest, "const") && rest[1].kind == tokenIdentifier && rest[2].text == "=" {
				constructor := rest[3:]
				if framework == "koa" && tokenSequence(constructor, "new") {
					constructor = constructor[1:]
				}
				if len(constructor) >= 3 && constructor[0].kind == tokenIdentifier && factories[constructor[0].text] && tokenSequence(constructor[1:], "(", ")") && bindingEnds(constructor, 3) {
					apps[rest[1].text] = true
				}
			}
		}
		depth = sourceDepth(depth, token)
	}
	if len(apps) != 1 {
		return 0, ""
	}
	ports := make(map[int]bool)
	health := ""
	depth = 0
	for i, token := range tokens {
		rest := tokens[i:]
		start := i == 0 || tokens[i-1].text == ";" || tokens[i-1].text == "}" || tokens[i-1].line < token.line && tokens[i-1].text == ")"
		if depth == 0 && start && token.kind == tokenIdentifier && apps[token.text] && len(rest) >= 6 {
			if tokenSequence(rest[1:], ".", "listen", "(") && rest[4].kind == tokenNumber && (rest[5].text == "," || rest[5].text == ")") {
				value, err := strconv.Atoi(rest[4].text)
				if err == nil && value > 0 && value <= 65535 {
					ports[value] = true
				}
			}
			if framework != "koa" && tokenSequence(rest[1:], ".", "get", "(") && rest[4].kind == tokenString && rest[5].text == "," && health == "" {
				switch rest[4].text {
				case "/health", "/health/", "/healthz", "/healthz/":
					health = rest[4].text
				}
			}
		}
		depth = sourceDepth(depth, token)
	}
	port := 0
	if len(ports) == 1 {
		for value := range ports {
			port = value
		}
	}
	return port, health
}

func sourceDepth(depth int, token sourceToken) int {
	if token.kind != tokenPunctuation {
		return depth
	}
	switch token.text {
	case "{", "(", "[":
		return depth + 1
	case "}", ")", "]":
		return depth - 1
	}
	return depth
}

// A chained constructor may return a router or another object, not the app.
func bindingEnds(tokens []sourceToken, end int) bool {
	if len(tokens) == end {
		return true
	}
	return tokens[end].text == ";" || tokens[end].line > tokens[end-1].line && tokens[end].kind == tokenIdentifier
}
