package runtime_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestModuleImportBoundary(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") && path != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, item := range parsed.Imports {
			name, _ := strconv.Unquote(item.Path.Value)
			for _, blocked := range []string{"github.com/yurika0211/lucky-agent", "github.com/yurika0211/luckyagent-app", "github.com/yurika0211/luckyagent/internal"} {
				if name == blocked || strings.HasPrefix(name, blocked+"/") {
					t.Errorf("%s imports forbidden package %s", path, name)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
