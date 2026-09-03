// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// A post reads the small session file and never the flight recorder — the
// promise in post.go and squeeze.go. It holds today because TurnDensity has
// exactly one caller, in the TUI, and nothing would go red if somebody folded
// the density into SessionBudget, into Append, or into a helper that happens
// to live in a tui file. This is that red: the call graph of the two commands
// the Stop hook runs, cmdPost and cmdTail, is followed by name through
// package main and on into internal/wall, and nothing it reaches may be
// recorderTail, TurnDensity, or any name that says recorder or density.
//
// Names, not types: a selector is kept by its last element and a function
// used as a value counts as called, which over-approximates — a reached name
// that merely resembles one of the package's functions is followed too — and
// never misses. The two targets are asserted to exist, so a rename fails
// here instead of leaving a guard that watches for nothing.
func TestPostNeverReadsTheFlightRecorder(t *testing.T) {
	fset := token.NewFileSet()
	mainGraph, err := callGraph(fset, ".")
	if err != nil {
		t.Fatal(err)
	}
	wallGraph, err := callGraph(fset, filepath.Join("internal", "wall"))
	if err != nil {
		t.Fatal(err)
	}
	roots := []string{"cmdPost", "cmdTail"}
	for _, root := range roots {
		if _, ok := mainGraph[root]; !ok {
			t.Fatalf("%s not found in package main — the guard would follow nothing", root)
		}
	}
	readers := []string{"recorderTail", "TurnDensity"}
	for _, reader := range readers {
		if _, ok := wallGraph[reader]; !ok {
			t.Fatalf("%s not found in internal/wall — renamed? the guard names its targets, update it", reader)
		}
	}

	reachedMain := closure(mainGraph, roots)
	var wallRoots []string
	for name := range reachedMain {
		if strings.HasPrefix(name, "wall.") {
			wallRoots = append(wallRoots, strings.TrimPrefix(name, "wall."))
		} else if _, ok := wallGraph[name]; ok {
			wallRoots = append(wallRoots, name) // a method on a wall value, kept by its bare name
		}
	}
	if len(wallRoots) == 0 {
		t.Fatal("cmdPost and cmdTail reach nothing in internal/wall — the guard followed nothing")
	}
	reachedWall := closure(wallGraph, wallRoots)

	var hits []string
	for _, reached := range []map[string]bool{reachedMain, reachedWall} {
		for name := range reached {
			bare := name[strings.LastIndex(name, ".")+1:]
			if bare == "recorderTail" || bare == "TurnDensity" ||
				strings.Contains(bare, "Density") || strings.Contains(strings.ToLower(bare), "recorder") {
				hits = append(hits, name)
			}
		}
	}
	sort.Strings(hits)
	for _, h := range hits {
		t.Errorf("a post reaches %s — the flight recorder would be read inside the Stop hook's ten-second budget", h)
	}
}

// callGraph maps every function and method declared in dir's non-test files
// to the names it refers to: identifiers that name a function of the same
// package (called or taken as a value), selectors by their last element, and
// pkg.X selectors also as "pkg.X" so a reference into another package keeps
// its package.
func callGraph(fset *token.FileSet, dir string) (map[string]map[string]bool, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	var parsed []*ast.File
	declared := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, af)
		for _, d := range af.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok {
				declared[fn.Name.Name] = true
			}
		}
	}
	graph := map[string]map[string]bool{}
	for _, af := range parsed {
		for _, d := range af.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			set := graph[fn.Name.Name]
			if set == nil {
				set = map[string]bool{}
				graph[fn.Name.Name] = set
			}
			if fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.Ident:
					if declared[x.Name] {
						set[x.Name] = true
					}
				case *ast.SelectorExpr:
					set[x.Sel.Name] = true
					if pkg, ok := x.X.(*ast.Ident); ok {
						set[pkg.Name+"."+x.Sel.Name] = true
					}
				}
				return true
			})
		}
	}
	return graph, nil
}

// closure is every name reachable from roots through graph, roots included.
func closure(graph map[string]map[string]bool, roots []string) map[string]bool {
	seen := map[string]bool{}
	queue := append([]string(nil), roots...)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		for callee := range graph[name] {
			queue = append(queue, callee)
		}
	}
	return seen
}
