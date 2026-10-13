package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
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
func TestCapabilitiesAreOnlyReachedThroughHelpers(t *testing.T) {
	t.Parallel()

	repoRoot, ok := testutil.GitGrepGuardRepoRoot(t)
	if !ok {
		return
	}

	interfaces := agentPackageInterfaces(t)
	names := strings.Join(interfaces, "|")
	// A type assertion `.(X)` or a type-switch case naming X, qualified as
	// agent.X outside package agent. Non-capability interfaces are matched too,
	// so the resolvers' own CapabilityDeclarer assertion keeps the grep from
	// coming back empty. git grep's ERE has no \b, so the case half is loose and
	// the Go regexp below does the precise matching.
	pattern := `\.\((agent\.)?(` + names + `)\)|case .*(` + names + `)[ ,:]`
	out := testutil.GitGrepGuard(t, repoRoot, "-n", "-E", "-e", pattern,
		"--", ":(glob)**/*.go", ":(exclude,glob)**/*_test.go")

	assertion := regexp.MustCompile(`(?:\.\(|case |, )(agent\.)?(` + names + `)\b`)
	const agentDir = "cmd/entire/cli/agent/"

	checked := 0
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		path, rest, ok := strings.Cut(line, ":")
		if !ok || !strings.HasSuffix(path, ".go") {
			t.Fatalf("cannot parse git grep output; expected path:line:text, got:\n  %s\n"+
				"Check whether git is colorizing into a pipe (color.ui or color.grep set to `always`).", line)
		}
		checked++

		// Unqualified names refer to package agent only inside package agent
		// itself; elsewhere they are some other package's types.
		inAgentPackage := strings.HasPrefix(path, agentDir) &&
			!strings.Contains(strings.TrimPrefix(path, agentDir), "/")
		for _, m := range assertion.FindAllStringSubmatch(rest, -1) {
			qualified, name := m[1] != "", m[2]
			if !qualified && !inAgentPackage {
				continue
			}
			if _, allowed := nonCapabilityInterfaces[name]; allowed {
				continue
			}
			t.Errorf("%s:%s: asserts an agent to capability %s directly; call As%s so its DeclaredCaps gate applies",
				path, rest, name, name)
		}
	}
	if checked == 0 {
		t.Fatal("no assertions checked — the guard's detection pattern has gone stale")
	}

	for name := range nonCapabilityInterfaces {
		if !slices.Contains(interfaces, name) {
			t.Errorf("nonCapabilityInterfaces lists %s, which is no longer an interface in package agent", name)
		}
	}
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
