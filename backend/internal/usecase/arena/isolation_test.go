package arena_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestArenaExecutionIsolation(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve Arena source directory")
	}
	directory := filepath.Dir(currentFile)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read Arena source directory: %v", err)
	}

	allowedInternalImports := map[string]struct{}{
		"github.com/TakuyaYagam1/task-per-minute/internal/domain":        {},
		"github.com/TakuyaYagam1/task-per-minute/internal/observability": {},
		"github.com/TakuyaYagam1/task-per-minute/internal/taskexec":      {},
	}
	forbiddenOperations := map[string]struct{}{
		"AddSolved":               {},
		"CountSolvedByDifficulty": {},
		"CreateDuelPlayerTask":    {},
		"Enqueue":                 {},
		"Finish":                  {},
		"GetActiveByPlayerID":     {},
		"GetDuelPlayerTask":       {},
		"GetPlayerTask":           {},
		"IncrementWin":            {},
		"JoinQueue":               {},
		"LeaveQueue":              {},
		"ListSolvedTaskIDs":       {},
		"MarkSolved":              {},
		"PopPair":                 {},
		"Remove":                  {},
		"UpdateDeadline":          {},
		"UpdateStatus":            {},
		"UpdateStatusIfCurrent":   {},
	}

	parsedFiles := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		parsedFiles++
		path := filepath.Join(directory, entry.Name())
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", entry.Name(), parseErr)
		}

		for _, spec := range file.Imports {
			importPath, unquoteErr := strconv.Unquote(spec.Path.Value)
			if unquoteErr != nil {
				t.Fatalf("decode import in %s: %v", entry.Name(), unquoteErr)
			}
			if strings.HasPrefix(importPath, "github.com/TakuyaYagam1/task-per-minute/internal/") {
				if _, allowed := allowedInternalImports[importPath]; !allowed {
					t.Errorf("%s imports non-neutral internal package %q", entry.Name(), importPath)
				}
			}
		}

		ast.Inspect(file, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.CallExpr:
				selector, isSelector := typed.Fun.(*ast.SelectorExpr)
				if isSelector {
					if _, forbidden := forbiddenOperations[selector.Sel.Name]; forbidden {
						t.Errorf("%s invokes forbidden casual operation %s", entry.Name(), selector.Sel.Name)
					}
				}
			case *ast.InterfaceType:
				for _, method := range typed.Methods.List {
					for _, name := range method.Names {
						if _, forbidden := forbiddenOperations[name.Name]; forbidden {
							t.Errorf("%s declares forbidden casual port %s", entry.Name(), name.Name)
						}
					}
				}
			}
			return true
		})
	}

	if parsedFiles == 0 {
		t.Fatal("no Arena production files were inspected")
	}
}
