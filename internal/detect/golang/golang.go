// Package golang detects Go applications without executing project code.
package golang

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/carl/dockerizethis/internal/detect"
	"github.com/carl/dockerizethis/internal/plan"
)

type Detector struct{}

var _ detect.Detector = Detector{}

func (Detector) Name() string { return "go" }

var modulePattern = regexp.MustCompile(`(?m)^\s*module\s+(\S+)`)
var versionPattern = regexp.MustCompile(`(?m)^\s*go\s+(1\.[0-9]+(?:\.[0-9]+)?)\s*(?://[^\n]*)?$`)

func (Detector) Detect(dir string) (plan.Plan, bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if errors.Is(err, fs.ErrNotExist) {
		return plan.Plan{}, false, nil
	}
	if err != nil {
		return plan.Plan{}, false, fmt.Errorf("read go.mod: %w", err)
	}
	module := modulePattern.FindSubmatch(data)
	if module == nil {
		return plan.Plan{}, false, fmt.Errorf("parse go.mod: missing module directive")
	}
	p := plan.Plan{Stack: "go", PkgManager: "go", Process: plan.ProcessWorker, Workdir: "/app", StartCmd: path.Base(strings.Trim(string(module[1]), `"`)), Version: "1.26", Confidence: 0.6}
	if version := versionPattern.FindSubmatch(data); version != nil {
		p.Version = string(version[1])
	} else {
		p.Notes = append(p.Notes, "no supported go directive; using Go 1.26")
	}
	imports, names, mains := map[string]bool{}, map[string]bool{}, map[string]bool{}
	web, port := false, 0
	err = filepath.WalkDir(dir, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if filename != dir {
				switch entry.Name() {
				case "vendor", ".git", "testdata", "node_modules":
					return filepath.SkipDir
				}
				if _, err := os.Stat(filepath.Join(filename, "go.mod")); err == nil {
					return filepath.SkipDir
				} else if !errors.Is(err, fs.ErrNotExist) {
					return err
				}
			}
			return nil
		}
		if !entry.Type().IsRegular() || filepath.Ext(filename) != ".go" || strings.HasSuffix(filename, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
		if err != nil {
			return err
		}
		aliases := map[string]string{}
		for _, imp := range f.Imports {
			name, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			imports[name] = true
			alias := path.Base(name)
			if imp.Name != nil {
				alias = imp.Name.Name
			}
			aliases[alias] = name
		}
		if f.Name.Name == "main" {
			for _, decl := range f.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "main" && fn.Recv == nil {
					rel, err := filepath.Rel(dir, filepath.Dir(filename))
					if err != nil {
						return err
					}
					mains[filepath.ToSlash(rel)] = true
				}
			}
		}
		portVars, serverVars := map[string]bool{}, map[string]bool{}
		// First collect variable origins, then inspect calls and fallback assignments.
		ast.Inspect(f, func(n ast.Node) bool {
			if spec, ok := n.(*ast.ValueSpec); ok {
				if sel, ok := spec.Type.(*ast.SelectorExpr); ok && selectorIs(sel, aliases, "net/http", "Server") {
					for _, name := range spec.Names {
						serverVars[name.Name] = true
					}
				}
			}
			lhs, rhs := assignment(n)
			for i, value := range rhs {
				if i >= len(lhs) {
					break
				}
				ast.Inspect(value, func(child ast.Node) bool {
					if call, ok := child.(*ast.CallExpr); ok && callIs(call, aliases, "os", "Getenv") && len(call.Args) == 1 {
						name := literal(call.Args[0])
						if name == "PORT" || name == "ADDR" {
							portVars[lhs[i]] = true
						}
					}
					if composite, ok := child.(*ast.CompositeLit); ok {
						if sel, ok := composite.Type.(*ast.SelectorExpr); ok && selectorIs(sel, aliases, "net/http", "Server") {
							serverVars[lhs[i]] = true
						}
					}
					return true
				})
			}
			return true
		})
		ast.Inspect(f, func(n ast.Node) bool {
			lhs, rhs := assignment(n)
			for i, value := range rhs {
				if i < len(lhs) && portVars[lhs[i]] && port == 0 {
					port = literalPort(value)
				}
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if callIs(call, aliases, "os", "Getenv") && len(call.Args) == 1 {
				if name := literal(call.Args[0]); name != "" {
					names[name] = true
				}
			}
			if f.Name.Name == "main" {
				if callIs(call, aliases, "net/http", "ListenAndServe") || callIs(call, aliases, "net/http", "ListenAndServeTLS") || callIs(call, aliases, "net/http", "Serve") || callIs(call, aliases, "net/http", "ServeTLS") {
					web = true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok && serverVars[id.Name] && slices.Contains([]string{"ListenAndServe", "ListenAndServeTLS", "Serve", "ServeTLS"}, sel.Sel.Name) {
						web = true
					}
				}
			}
			if (callIs(call, aliases, "net/http", "HandleFunc") || callIs(call, aliases, "net/http", "Handle")) && len(call.Args) > 0 && literal(call.Args[0]) == "/health" {
				p.HealthPath = "/health"
			}
			return true
		})
		return nil
	})
	if err != nil {
		return plan.Plan{}, false, fmt.Errorf("scan Go sources: %w", err)
	}
	for _, framework := range []struct{ name, dependency string }{{"gin", "github.com/gin-gonic/gin"}, {"echo", "github.com/labstack/echo"}, {"chi", "github.com/go-chi/chi"}} {
		if hasImport(imports, framework.dependency) {
			p.Framework, web = framework.name, true
			break
		}
		// A declared dependency identifies the framework, but only imports imply a server.
		if p.Framework == "" && regexp.MustCompile(`(?m)^\s*(?:require\s+)?`+regexp.QuoteMeta(framework.dependency)+`(?:/v[0-9]+)?\s+v`).Match(data) {
			p.Framework = framework.name
		}
	}
	if web {
		p.Process, p.Port, p.Confidence = plan.ProcessWeb, port, 0.9
		if p.Port == 0 {
			p.Port = 8080
		}
		if p.Framework == "" {
			p.Framework = "net/http"
		}
	} else {
		p.HealthPath = ""
	}
	for _, service := range []struct {
		name    plan.Service
		imports []string
	}{
		{plan.ServicePostgres, []string{"github.com/lib/pq", "github.com/jackc/pgx"}},
		{plan.ServiceRedis, []string{"github.com/redis/go-redis", "github.com/go-redis/redis"}},
		{plan.ServiceMongo, []string{"go.mongodb.org/mongo-driver"}},
		{plan.ServiceMySQL, []string{"github.com/go-sql-driver/mysql"}},
	} {
		for _, name := range service.imports {
			if hasImport(imports, name) {
				p.Services = append(p.Services, service.name)
				break
			}
		}
	}
	slices.Sort(p.Services)
	for name := range names {
		p.Env = append(p.Env, plan.EnvVar{Name: name, Required: requiredEnv(name)})
	}
	slices.SortFunc(p.Env, func(a, b plan.EnvVar) int { return strings.Compare(a.Name, b.Name) })
	if imports["C"] {
		p.Extras = map[string]string{"cgo": "1"}
		p.Notes = append(p.Notes, "cgo detected; confirm any additional native build and runtime libraries")
	}
	var packages []string
	for name := range mains {
		packages = append(packages, name)
	}
	slices.Sort(packages)
	if len(packages) == 1 {
		p.BuildCmd = "go build -o /app/server ."
		if packages[0] != "." {
			if p.Extras == nil {
				p.Extras = map[string]string{}
			}
			p.Extras["buildPackage"] = "./" + packages[0]
			p.BuildCmd = "go build -o /app/server ./" + packages[0]
		}
		if !web {
			p.Confidence = 0.8
		}
	} else {
		p.Confidence = 0.4
		p.Notes = append(p.Notes, "could not select a single main package; set Extras[\"buildPackage\"] before rendering")
	}
	return p, true, nil
}

func hasImport(imports map[string]bool, prefix string) bool {
	for name := range imports {
		if name == prefix || strings.HasPrefix(name, prefix+"/") {
			return true
		}
	}
	return false
}

func assignment(n ast.Node) ([]string, []ast.Expr) {
	var names []string
	switch n := n.(type) {
	case *ast.AssignStmt:
		for _, expr := range n.Lhs {
			name := ""
			if id, ok := expr.(*ast.Ident); ok {
				name = id.Name
			}
			names = append(names, name)
		}
		return names, n.Rhs
	case *ast.ValueSpec:
		for _, id := range n.Names {
			names = append(names, id.Name)
		}
		return names, n.Values
	}
	return nil, nil
}

func literal(expr ast.Expr) string {
	if value, ok := expr.(*ast.BasicLit); ok && value.Kind == token.STRING {
		result, _ := strconv.Unquote(value.Value)
		return result
	}
	return ""
}
func literalPort(expr ast.Expr) int {
	value := literal(expr)
	if number, ok := expr.(*ast.BasicLit); ok && number.Kind == token.INT {
		value = number.Value
	}
	if i := strings.LastIndex(value, ":"); i >= 0 {
		value = value[i+1:]
	}
	n, _ := strconv.Atoi(value)
	if n > 0 && n <= 65535 {
		return n
	}
	return 0
}
func selectorIs(sel *ast.SelectorExpr, aliases map[string]string, pkg, name string) bool {
	id, ok := sel.X.(*ast.Ident)
	return ok && aliases[id.Name] == pkg && sel.Sel.Name == name
}
func callIs(call *ast.CallExpr, aliases map[string]string, pkg, name string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && selectorIs(sel, aliases, pkg, name)
}
func requiredEnv(name string) bool {
	switch name {
	case "DATABASE_URL", "REDIS_URL", "MONGO_URI", "MONGODB_URI", "MYSQL", "API_KEY", "TOKEN", "SECRET", "PASSWORD":
		return true
	}
	return strings.HasPrefix(name, "MYSQL_") || strings.HasSuffix(name, "_TOKEN") || strings.HasSuffix(name, "_SECRET") || strings.HasSuffix(name, "_API_KEY") || strings.HasSuffix(name, "_PASSWORD")
}
