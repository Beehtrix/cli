package agent

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path"
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
// that wraps onto a second line are both seen. Assertions to an interface
// literal or to an interface declared in the asserting package are matched by
// their capability methods, since naming the package-agent interface is not the
// only way to reach one.
func TestCapabilitiesAreOnlyReachedThroughHelpers(t *testing.T) {
	t.Parallel()

	repoRoot, ok := testutil.GitGrepGuardRepoRoot(t)
	if !ok {
		return
	}

	interfaces, helpers := agentPackageDecls(t)
	methods := capabilityMethods(interfaces)

	type parsedFile struct {
		rel  string
		file *ast.File
	}
	var files []parsedFile
	localInterfaces := map[string]map[string]*ast.InterfaceType{}
	fset := token.NewFileSet()
	for _, rel := range productionGoFiles(t, repoRoot) {
		src, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if errors.Is(err, fs.ErrNotExist) {
			// A tracked file deleted from the working tree or excluded by a
			// sparse checkout is not compiled here either; the checkout that
			// builds it runs this guard over it.
			t.Logf("skipping %s: %v", rel, err)
			continue
		}
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		file, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
		if err != nil {
			// A file with a syntax error cannot compile either, so it cannot
			// hide an assertion; it is more likely an untracked scratch file.
			t.Logf("skipping %s: %v", rel, err)
			continue
		}
		files = append(files, parsedFile{rel, file})
		dir := path.Dir(rel)
		for name, iface := range declaredInterfaces(file) {
			if localInterfaces[dir] == nil {
				localInterfaces[dir] = map[string]*ast.InterfaceType{}
			}
			localInterfaces[dir][name] = iface
		}
	}

	// Only assertions outside package agent count toward the staleness check:
	// the resolvers' own CapabilityDeclarer assertions are always found, so
	// counting them would keep the check from ever firing.
	checkedOutside := 0
	for _, f := range files {
		qualifier := agentImportName(f.file, f.rel)
		inAgentPackage := path.Dir(f.rel) == agentPackageDir
		local := localInterfaces[path.Dir(f.rel)]
		resolve := func(e ast.Expr) *ast.InterfaceType { return asInterfaceType(e, local) }
		for _, expr := range assertedTypes(f.file) {
			pos := fset.Position(expr.Pos())
			name, ok := agentInterfaceName(expr, qualifier, interfaces)
			if ok {
				if !inAgentPackage {
					checkedOutside++
				}
				if _, allowed := nonCapabilityInterfaces[name]; !allowed {
					t.Errorf("%s:%d: asserts an agent to capability %s directly; %s",
						f.rel, pos.Line, name, helperAdvice(name, helpers))
				}
				continue
			}
			iface := resolve(expr)
			if iface == nil {
				continue
			}
			if capability, via, found := reachedCapability(iface, qualifier, interfaces, methods, resolve, map[*ast.InterfaceType]bool{}); found {
				t.Errorf("%s:%d: asserts to an interface that reaches capability %s through %s; %s",
					f.rel, pos.Line, capability, via, helperAdvice(capability, helpers))
			}
		}
	}
	if checkedOutside == 0 {
		t.Fatal("no assertions outside package agent checked — the guard's detection has gone stale")
	}

	for name := range nonCapabilityInterfaces {
		if _, ok := interfaces[name]; !ok {
			t.Errorf("nonCapabilityInterfaces lists %s, which is no longer an interface in package agent", name)
		}
	}
}

const agentPackageDir = "cmd/entire/cli/agent"

// helperAdvice tells the developer how to reach capability through its As*
// helper, including how to add one when the capability has none yet.
func helperAdvice(capability string, helpers map[string]struct{}) string {
	helper := "As" + capability
	if _, ok := helpers[helper]; ok {
		return "call " + helper + " so its DeclaredCaps gate applies"
	}
	return "add " + helper + " to package agent, resolving through declaredCapability (with a DeclaredCaps field), " +
		"builtinCapability, or nonDeclarerCapability (listing " + capability + " in the DeclaredCaps doc for the last two), and call it"
}

// capabilityMethods maps each method declared by a capability interface to
// that interface, the alphabetically first when several declare it. Methods
// also declared by a non-capability interface (the Agent contract every agent
// implements) are left out, since asserting to them reaches no optional
// capability.
func capabilityMethods(interfaces map[string]*ast.InterfaceType) map[string]string {
	shared := map[string]struct{}{}
	for name := range nonCapabilityInterfaces {
		if iface, ok := interfaces[name]; ok {
			for _, method := range methodNames(iface) {
				shared[method] = struct{}{}
			}
		}
	}
	methods := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(interfaces)) {
		if _, ok := nonCapabilityInterfaces[name]; ok {
			continue
		}
		for _, method := range methodNames(interfaces[name]) {
			_, isShared := shared[method]
			_, taken := methods[method]
			if !isShared && !taken {
				methods[method] = name
			}
		}
	}
	return methods
}

// methodNames returns the methods iface declares itself, not those it embeds.
func methodNames(iface *ast.InterfaceType) []string {
	var names []string
	for _, field := range iface.Methods.List {
		for _, name := range field.Names {
			names = append(names, name.Name)
		}
	}
	return names
}

// declaredInterfaces returns the interface types file declares, by name.
func declaredInterfaces(file *ast.File) map[string]*ast.InterfaceType {
	interfaces := map[string]*ast.InterfaceType{}
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
			if iface, ok := typeSpec.Type.(*ast.InterfaceType); ok {
				interfaces[typeSpec.Name.Name] = iface
			}
		}
	}
	return interfaces
}

// asInterfaceType returns expr's interface type when it is an interface
// literal or names an interface declared in the asserting package.
func asInterfaceType(expr ast.Expr, local map[string]*ast.InterfaceType) *ast.InterfaceType {
	switch e := expr.(type) {
	case *ast.InterfaceType:
		return e
	case *ast.ParenExpr:
		return asInterfaceType(e.X, local)
	case *ast.Ident:
		return local[e.Name]
	}
	return nil
}

// reachedCapability reports the first capability iface reaches, through a
// declared method or an embedded interface, and describes how it reaches it.
func reachedCapability(
	iface *ast.InterfaceType,
	qualifier string,
	interfaces map[string]*ast.InterfaceType,
	methods map[string]string,
	resolve func(ast.Expr) *ast.InterfaceType,
	seen map[*ast.InterfaceType]bool,
) (string, string, bool) {
	if seen[iface] {
		return "", "", false
	}
	seen[iface] = true
	for _, field := range iface.Methods.List {
		for _, name := range field.Names {
			if capability, ok := methods[name.Name]; ok {
				return capability, "method " + name.Name, true
			}
		}
		if len(field.Names) > 0 {
			continue
		}
		if name, ok := agentInterfaceName(field.Type, qualifier, interfaces); ok {
			if _, allowed := nonCapabilityInterfaces[name]; !allowed {
				return name, "an embedded " + name, true
			}
			continue
		}
		if embedded := resolve(field.Type); embedded != nil {
			if capability, method, ok := reachedCapability(embedded, qualifier, interfaces, methods, resolve, seen); ok {
				return capability, method, true
			}
		}
	}
	return "", "", false
}

const agentImportPath = "github.com/entireio/cli/cmd/entire/cli/agent"

// agentImportName returns the identifier file uses to qualify package agent's
// names: the import's alias, or "agent" when it is imported plainly. Inside
// package agent itself the names are unqualified, reported as "". Files that do
// not import the package cannot assert to its interfaces at all; "-" matches
// nothing. rel is slash-separated, as git prints it.
func agentImportName(file *ast.File, rel string) string {
	if path.Dir(rel) == agentPackageDir {
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
func agentInterfaceName(expr ast.Expr, qualifier string, interfaces map[string]*ast.InterfaceType) (string, bool) {
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
// outside _test.go, slash-separated relative to repoRoot, with the same
// isolation as testutil.GitGrepGuard. -z keeps non-ASCII paths unquoted, since
// GuardGitEnv drops the caller's core.quotePath setting.
func productionGoFiles(t *testing.T, repoRoot string) []string {
	t.Helper()

	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "*.go") //nolint:noctx // guard test, no cancellation needed
	cmd.Dir = repoRoot
	cmd.Env = testutil.GuardGitEnv()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	var files []string
	for name := range strings.SplitSeq(string(out), "\x00") {
		if name != "" && !strings.HasSuffix(name, "_test.go") {
			files = append(files, name)
		}
	}
	if len(files) == 0 {
		t.Fatal("git ls-files found no Go files — the guard's file discovery has gone stale")
	}
	return files
}

// agentPackageDecls returns the interfaces declared in the production files of
// package agent, by name, and the names of its package-level functions.
func agentPackageDecls(t *testing.T) (map[string]*ast.InterfaceType, map[string]struct{}) {
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

	interfaces := map[string]*ast.InterfaceType{}
	funcs := map[string]struct{}{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			maps.Copy(interfaces, declaredInterfaces(file))
			for _, decl := range file.Decls {
				if funcDecl, ok := decl.(*ast.FuncDecl); ok && funcDecl.Recv == nil {
					funcs[funcDecl.Name.Name] = struct{}{}
				}
			}
		}
	}
	if len(interfaces) == 0 {
		t.Fatal("no interfaces found in package agent — test setup is broken")
	}
	return interfaces, funcs
}
