// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// A post reads the small session file and never the flight recorder — the
// promise in post.go and squeeze.go. It holds today because TurnDensity has
// exactly one caller, in the TUI, and nothing would go red if somebody folded
// the density into SessionBudget and put a 256 KB read into the Stop hook's
// ten-second budget. This is that red, in two halves: the command side is
// scanned for any name that knows the recorder exists, and inside the
// package the call closure of SessionBudget is followed to make sure it
// never reaches recorderTail — the detour the first scan cannot see.
func TestPostNeverReadsTheFlightRecorder(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	scanned, sawPost := 0, false
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || strings.HasPrefix(f, "tui") {
			continue // the TUI is the one reader, and it is not a post
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		ast.Inspect(af, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.FuncDecl:
				if x.Name.Name == "cmdPost" {
					sawPost = true
				}
			case *ast.Ident:
				if strings.Contains(x.Name, "Density") || strings.Contains(strings.ToLower(x.Name), "recorder") {
					t.Errorf("%s: %s — the command side must not know the flight recorder exists", fset.Position(x.Pos()), x.Name)
				}
			}
			return true
		})
	}
	if scanned == 0 || !sawPost {
		t.Fatalf("scanned %d files and cmdPost was not among them — the guard looked at nothing", scanned)
	}

	// the detour: every function SessionBudget reaches inside its package.
	// Method and cross-package calls are kept by their bare name, which
	// over-approximates and never misses.
	callees := map[string]map[string]bool{}
	pkg, err := filepath.Glob(filepath.Join("internal", "wall", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range pkg {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range af.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			set := callees[fn.Name.Name]
			if set == nil {
				set = map[string]bool{}
				callees[fn.Name.Name] = set
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fun := call.Fun.(type) {
				case *ast.Ident:
					set[fun.Name] = true
				case *ast.SelectorExpr:
					set[fun.Sel.Name] = true
				}
				return true
			})
		}
	}
	if len(callees["SessionBudget"]) == 0 {
		t.Fatal("SessionBudget was not found in internal/wall, or calls nothing — the guard followed nothing")
	}
	seen := map[string]bool{}
	queue := []string{"SessionBudget"}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		for callee := range callees[name] {
			queue = append(queue, callee)
		}
	}
	for _, reader := range []string{"recorderTail", "TurnDensity"} {
		if seen[reader] {
			t.Errorf("SessionBudget reaches %s — a post would read the flight recorder inside the Stop hook's budget", reader)
		}
	}
}
