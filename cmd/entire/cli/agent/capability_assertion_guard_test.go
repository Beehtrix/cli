package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

// nonCapabilityInterfaces are the interfaces in package agent that are not
// optional capabilities, so asserting to them directly is fine.
var nonCapabilityInterfaces = map[string]string{
	"Agent":              "the base contract every agent implements",
	"CapabilityDeclarer": "the resolvers' own declaration gate",
	"SessionLocator":     "the narrow slice of Agent a SessionStore holds, not an optional capability",
	"TestOnly":           "registry marker that hides test agents from selection, not a capability",
}

// TestCapabilitiesAreOnlyReachedThroughHelpers fails when code type-asserts an
// agent to an optional capability interface instead of calling its As* helper.
//
// An inline assertion skips the helper's DeclaredCaps gate, so an external
// agent that implements the method but did not declare the capability is used
// anyway. It also hides the capability from the DeclaredCaps doc list, which is
// derived from the helpers.
//
// The check parses every production Go file rather than grepping it, so an
// import under another name (agentpkg, cliagent) and a type-switch case list
// that wraps onto a second line are both seen.
func TestCapabilitiesAreOnlyReachedThroughHelpers(t *testing.T) {
	t.Parallel()

	repoRoot, ok := testutil.GitGrepGuardRepoRoot(t)
	if !ok {
		return
	}

	interfaces := map[string]struct{}{}
	for _, name := range agentPackageInterfaces(t) {
		interfaces[name] = struct{}{}
	}

	checked := 0
	fset := token.NewFileSet()
	for _, rel := range productionGoFiles(t, repoRoot) {
		file, err := parser.ParseFile(fset, filepath.Join(repoRoot, rel), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		for _, expr := range assertedTypes(file) {
			name, ok := agentInterfaceName(expr, agentImportName(file, rel), interfaces)
			if !ok {
				continue
			}
			checked++
			if _, allowed := nonCapabilityInterfaces[name]; allowed {
				continue
			}
			pos := fset.Position(expr.Pos())
			t.Errorf("%s:%d: asserts an agent to capability %s directly; call As%s so its DeclaredCaps gate applies",
				rel, pos.Line, name, name)
		}
	}
	if checked == 0 {
		t.Fatal("no assertions checked — the guard's detection has gone stale")
	}

	for name := range nonCapabilityInterfaces {
		if _, ok := interfaces[name]; !ok {
			t.Errorf("nonCapabilityInterfaces lists %s, which is no longer an interface in package agent", name)
		}
	}
}

const agentImportPath = "github.com/entireio/cli/cmd/entire/cli/agent"

// agentImportName returns the identifier file uses to qualify package agent's
// names: the import's alias, or "agent" when it is imported plainly. Inside
// package agent itself the names are unqualified, reported as "". Files that do
// not import the package cannot assert to its interfaces at all; "-" matches
// nothing.
func agentImportName(file *ast.File, rel string) string {
	const agentDir = "cmd/entire/cli/agent/"
	if strings.HasPrefix(rel, agentDir) && !strings.Contains(strings.TrimPrefix(rel, agentDir), "/") {
		return ""
	}
	for _, imp := range file.Imports {
		if strings.Trim(imp.Path.Value, `"`) != agentImportPath {
			continue
		}
		if imp.Name == nil {
			return "agent"
		}
		if imp.Name.Name == "." {
			return ""
		}
		return imp.Name.Name
	}
	return "-"
}

// agentInterfaceName reports which package-agent interface expr names, if any:
// qualifier.X for files that import the package, or a bare X where qualifier
// is "" (package agent and dot imports).
func agentInterfaceName(expr ast.Expr, qualifier string, interfaces map[string]struct{}) (string, bool) {
	var name string
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		pkg, ok := e.X.(*ast.Ident)
		if !ok || pkg.Name != qualifier {
			return "", false
		}
		name = e.Sel.Name
	case *ast.Ident:
		if qualifier != "" {
			return "", false
		}
		name = e.Name
	default:
		return "", false
	}
	_, ok := interfaces[name]
	return name, ok
}

// assertedTypes returns every type expression file asserts a value to: the
// X in v.(X) and in each case of a switch v.(type).
func assertedTypes(file *ast.File) []ast.Expr {
	var types []ast.Expr
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.TypeAssertExpr:
			if n.Type != nil {
				types = append(types, n.Type)
			}
		case *ast.TypeSwitchStmt:
			for _, stmt := range n.Body.List {
				if clause, ok := stmt.(*ast.CaseClause); ok {
					types = append(types, clause.List...)
				}
			}
		}
		return true
	})
	return types
}

// productionGoFiles lists the repository's tracked and untracked Go files
// outside _test.go, relative to repoRoot, with the same isolation as
// testutil.GitGrepGuard.
func productionGoFiles(t *testing.T, repoRoot string) []string {
	t.Helper()

	cmd := exec.Command("git", "ls-files", "--cached", "--others", "--exclude-standard", "--", "*.go") //nolint:noctx // guard test, no cancellation needed
	cmd.Dir = repoRoot
	cmd.Env = testutil.GuardGitEnv()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	var files []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line != "" && !strings.HasSuffix(line, "_test.go") {
			files = append(files, filepath.FromSlash(line))
		}
	}
	if len(files) == 0 {
		t.Fatal("git ls-files found no Go files — the guard's file discovery has gone stale")
	}
	return files
}

// agentPackageInterfaces returns the names of the interfaces declared in the
// production files of package agent.
func agentPackageInterfaces(t *testing.T) []string {
	t.Helper()

	dir := findAgentDir(t)
	fset := token.NewFileSet()
	//nolint:staticcheck // ParseDir intentionally scans every production file regardless of build tags.
	pkgs, err := parser.ParseDir(fset, dir, func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parser.ParseDir(%s): %v", dir, err)
	}

	var names []string
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.TYPE {
					continue
				}
				for _, spec := range gen.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					if _, isInterface := typeSpec.Type.(*ast.InterfaceType); isInterface {
						names = append(names, typeSpec.Name.Name)
					}
				}
			}
		}
	}
	if len(names) == 0 {
		t.Fatal("no interfaces found in package agent — test setup is broken")
	}
	slices.Sort(names)
	return names
}
