package architecture_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/TakuyaYagam1/task-per-minute"

type importRule struct {
	directory string
	forbidden []string
}

func TestProductionImportsFollowArchitecture(t *testing.T) {
	t.Parallel()

	internalDir := internalRoot(t)
	rules := []importRule{
		{
			directory: "domain",
			forbidden: []string{
				modulePath + "/internal/usecase",
				modulePath + "/internal/adapter",
				modulePath + "/internal/bootstrap",
			},
		},
		{
			directory: "usecase",
			forbidden: []string{
				modulePath + "/internal/adapter",
				modulePath + "/internal/bootstrap",
			},
		},
		{
			directory: filepath.Join("adapter", "inbound"),
			forbidden: []string{
				modulePath + "/internal/adapter/outbound",
				modulePath + "/internal/bootstrap",
			},
		},
		{
			directory: filepath.Join("adapter", "inbound", "websocket"),
			forbidden: []string{
				modulePath + "/internal/adapter/inbound/http",
			},
		},
		{
			directory: filepath.Join("adapter", "outbound"),
			forbidden: []string{
				modulePath + "/internal/adapter/inbound",
				modulePath + "/internal/bootstrap",
			},
		},
	}

	for _, rule := range rules {
		rule := rule
		t.Run(filepath.ToSlash(rule.directory), func(t *testing.T) {
			t.Parallel()
			assertNoForbiddenImports(t, filepath.Join(internalDir, rule.directory), rule.forbidden)
		})
	}
}

func TestUsecasePackagesStayIndependent(t *testing.T) {
	t.Parallel()

	root := filepath.Join(internalRoot(t), "usecase")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read usecase root: %v", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			if filepath.Ext(entry.Name()) == ".go" && !strings.HasSuffix(entry.Name(), "_test.go") {
				t.Errorf("shared usecase root must not contain production Go file %s", filepath.Join(root, entry.Name()))
			}
			continue
		}

		packageName := entry.Name()
		t.Run(packageName, func(t *testing.T) {
			t.Parallel()
			assertNoSiblingUsecaseImports(t, filepath.Join(root, packageName), packageName)
		})
	}
}

func internalRoot(t *testing.T) string {
	t.Helper()

	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve architecture test working directory: %v", err)
	}
	return filepath.Clean(filepath.Join(workingDir, ".."))
}

func assertNoForbiddenImports(t *testing.T, root string, forbidden []string) {
	t.Helper()

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range file.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			for _, prefix := range forbidden {
				if importPath == prefix || strings.HasPrefix(importPath, prefix+"/") {
					position := fileSet.Position(imported.Pos())
					t.Errorf("%s:%d imports forbidden package %q", path, position.Line, importPath)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("inspect %s: %v", root, err)
	}
}

func assertNoSiblingUsecaseImports(t *testing.T, root, packageName string) {
	t.Helper()

	usecasePrefix := modulePath + "/internal/usecase/"
	allowedPrefix := usecasePrefix + packageName
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range file.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			if strings.HasPrefix(importPath, usecasePrefix) &&
				importPath != allowedPrefix && !strings.HasPrefix(importPath, allowedPrefix+"/") {
				position := fileSet.Position(imported.Pos())
				t.Errorf("%s:%d imports sibling usecase package %q", path, position.Line, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("inspect %s: %v", root, err)
	}
}
