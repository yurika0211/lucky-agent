package appservice

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestModuleDoesNotImportHostOrTransports(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range file.Imports {
			name, _ := strconv.Unquote(imported.Path.Value)
			for _, forbidden := range []string{
				"github.com/yurika0211/luckyagent", "github.com/yurika0211/lucky-agent", "github.com/yurika0211/luckyagent-app", "github.com/yurika0211/luckyagent-tools", "net/http", "github.com/gin-gonic/gin", "github.com/yurika0211/luckyagent-api",
				"github.com/yurika0211/luckyagent-gateway", "github.com/yurika0211/luckyagent/internal/apiapp",
				"github.com/yurika0211/luckyagent/internal/channelapp", "github.com/yurika0211/luckyagent/internal/cli",
			} {
				if name == forbidden || strings.HasPrefix(name, forbidden+"/") {
					t.Errorf("%s imports entrance/transport %s", path, name)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
