package node

import (
	"path/filepath"
	"regexp"
	"strings"
)

var routeSegment = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// fileRouteHealthPath maps only conventional route roots and literal URL segments.
// Dynamic, private, parallel, and intercepted routes need configuration we cannot infer.
func fileRouteHealthPath(dir, filename, framework, source string) string {
	rel, err := filepath.Rel(dir, filename)
	if err != nil {
		return ""
	}
	rel = filepath.ToSlash(rel)
	ext := filepath.Ext(rel)
	stem := strings.TrimSuffix(filepath.Base(rel), ext)
	var route []string
	switch framework {
	case "next":
		if ext != ".js" && ext != ".jsx" && ext != ".ts" && ext != ".tsx" {
			return ""
		}
		rel = strings.TrimPrefix(rel, "src/")
		switch {
		case strings.HasPrefix(rel, "app/"):
			if stem == "route" && ext != ".ts" && ext != ".js" {
				return ""
			}
			if stem != "page" && stem != "route" {
				return ""
			}
			if stem == "route" && !exportsGET(source) || stem == "page" && !exportsDefault(source) {
				return ""
			}
			segments := strings.Split(strings.TrimPrefix(rel, "app/"), "/")
			for _, segment := range segments[:len(segments)-1] {
				if strings.HasPrefix(segment, "(") && strings.HasSuffix(segment, ")") {
					continue
				}
				if strings.HasPrefix(segment, "_") {
					return ""
				}
				route = append(route, segment)
			}
		case strings.HasPrefix(rel, "pages/"):
			if !exportsDefault(source) {
				return ""
			}
			route = strings.Split(strings.TrimSuffix(strings.TrimPrefix(rel, "pages/"), ext), "/")
			if stem == "index" {
				route = route[:len(route)-1]
			}
		default:
			return ""
		}
	case "nuxt":
		switch {
		case strings.HasPrefix(rel, "server/api/") || strings.HasPrefix(rel, "server/routes/"):
			if ext != ".ts" && ext != ".js" || !exportsDefault(source) {
				return ""
			}
			prefix := "server/routes/"
			if strings.HasPrefix(rel, "server/api/") {
				prefix = "server/api/"
				route = append(route, "api")
			}
			rest := strings.TrimSuffix(strings.TrimPrefix(rel, prefix), ext)
			rest = strings.TrimSuffix(rest, ".get")
			route = append(route, strings.Split(rest, "/")...)
			if route[len(route)-1] == "index" {
				route = route[:len(route)-1]
			}
		case ext == ".vue" && (strings.HasPrefix(rel, "pages/") || strings.HasPrefix(rel, "app/pages/")):
			rel = strings.TrimPrefix(rel, "app/")
			route = strings.Split(strings.TrimSuffix(strings.TrimPrefix(rel, "pages/"), ext), "/")
			if stem == "index" {
				route = route[:len(route)-1]
			}
		default:
			return ""
		}
	default:
		return ""
	}
	if len(route) == 0 {
		return ""
	}
	for _, segment := range route {
		if !routeSegment.MatchString(segment) {
			return ""
		}
	}
	last := route[len(route)-1]
	if last != "health" && last != "healthz" {
		return ""
	}
	return "/" + strings.Join(route, "/")
}

func exportsGET(source string) bool {
	tokens, _ := sourceTokens(source)
	for i := range tokens {
		if tokenSequence(tokens[i:], "export", "function", "GET", "(") || tokenSequence(tokens[i:], "export", "async", "function", "GET", "(") || tokenSequence(tokens[i:], "export", "const", "GET", "=") {
			return true
		}
	}
	return false
}

func exportsDefault(source string) bool {
	tokens, _ := sourceTokens(source)
	for i := range tokens {
		if tokenSequence(tokens[i:], "export", "default") {
			return true
		}
	}
	return false
}
