package arena_test

import (
	"context"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

const usecaseImportPrefix = "github.com/TakuyaYagam1/task-per-minute/internal/usecase/"

func TestArenaImportsNoSiblingUsecase(t *testing.T) {
	t.Parallel()

	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve arena package: %v", err)
	}
	err = filepath.WalkDir(workingDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fileSet := token.NewFileSet()
		file, parseErr := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		for _, imported := range file.Imports {
			importPath, unquoteErr := strconv.Unquote(imported.Path.Value)
			if unquoteErr != nil {
				return unquoteErr
			}
			if strings.HasPrefix(importPath, usecaseImportPrefix) && importPath != usecaseImportPrefix+"arena" {
				position := fileSet.Position(imported.Pos())
				t.Errorf("%s:%d imports sibling usecase %q", path, position.Line, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("inspect arena imports: %v", err)
	}
}

func TestArenaPortContractsCarryTransactionContextAndGameScope(t *testing.T) {
	t.Parallel()

	contextType := reflect.TypeOf((*context.Context)(nil)).Elem()
	scopeType := reflect.TypeOf(arena.GameScope{})
	ports := []reflect.Type{
		reflect.TypeOf((*arena.GameRepository)(nil)).Elem(),
		reflect.TypeOf((*arena.TaskSnapshotter)(nil)).Elem(),
		reflect.TypeOf((*arena.FlagValidator)(nil)).Elem(),
		reflect.TypeOf((*arena.SubmissionOrderer)(nil)).Elem(),
	}

	for _, port := range ports {
		for methodIndex := range port.NumMethod() {
			method := port.Method(methodIndex)
			if method.Type.NumIn() < 2 {
				t.Errorf("%s.%s has no context and game scope", port.Name(), method.Name)
				continue
			}
			if method.Type.In(0) != contextType {
				t.Errorf("%s.%s first input = %s, want context.Context", port.Name(), method.Name, method.Type.In(0))
			}
			if method.Type.In(1) != scopeType {
				t.Errorf("%s.%s second input = %s, want arena.GameScope", port.Name(), method.Name, method.Type.In(1))
			}
		}
	}
}

func TestArenaPortContractsExcludeCasualState(t *testing.T) {
	t.Parallel()

	ports := []reflect.Type{
		reflect.TypeOf((*arena.GameRepository)(nil)).Elem(),
		reflect.TypeOf((*arena.TaskSnapshotter)(nil)).Elem(),
		reflect.TypeOf((*arena.FlagValidator)(nil)).Elem(),
		reflect.TypeOf((*arena.SubmissionOrderer)(nil)).Elem(),
	}
	forbidden := []string{"domain.Duel", "domain.PlayerStatus", "Leaderboard", "SolvedHistory"}

	for _, port := range ports {
		for methodIndex := range port.NumMethod() {
			method := port.Method(methodIndex)
			signature := method.Type.String()
			for _, name := range forbidden {
				if strings.Contains(signature, name) {
					t.Errorf("%s.%s exposes casual contract %s", port.Name(), method.Name, name)
				}
			}
		}
	}
}
