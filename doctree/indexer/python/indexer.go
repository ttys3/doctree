// Package python provides a doctree indexer implementation for Python.
package python

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/sourcegraph/doctree/doctree/indexer"
	"github.com/sourcegraph/doctree/doctree/schema"
)

func init() {
	indexer.Register(&pythonIndexer{})
}

// Implements the indexer.Language interface.
type pythonIndexer struct{}

func (i *pythonIndexer) Name() schema.Language { return schema.LanguagePython }

func (i *pythonIndexer) Extensions() []string { return []string{"py", "py3"} }

func (i *pythonIndexer) IndexDir(ctx context.Context, dir string) (*schema.Index, error) {
	// Find Python sources
	var sources []string
	if err := fs.WalkDir(os.DirFS(dir), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err // error walking dir
		}
		if !d.IsDir() {
			ext := filepath.Ext(path)
			if ext == ".py" || ext == ".py3" {
				sources = append(sources, path)
			}
		}
		return nil
	}); err != nil {
		return nil, errors.Wrap(err, "WalkDir")
	}

	files := 0
	bytes := 0
	mods := map[string]moduleInfo{}
	functionsByMod := map[string][]schema.Section{}
	for _, path := range sources {
		if strings.Contains(path, "test_") || strings.Contains(path, "_test") || strings.Contains(path, "tests") {
			continue
		}
		dirFS := os.DirFS(dir)
		content, err := fs.ReadFile(dirFS, path)
		if err != nil {
			return nil, errors.Wrap(err, "ReadFile")
		}

		files += 1
		bytes += len(content)

		// Parse the file with tree-sitter.
		parser := sitter.NewParser()
		defer parser.Close()
		parser.SetLanguage(python.GetLanguage())

		tree, err := parser.ParseCtx(ctx, nil, content)
		if err != nil {
			return nil, errors.Wrap(err, "ParseCtx")
		}
		defer tree.Close()

		// Inspect the root node.
		n := tree.RootNode()

		// Module clauses
		var modName string
		{
			query, err := sitter.NewQuery([]byte(`
			(
				module
				.
				(comment)*
				.
				(expression_statement .
					(string) @module_docs
				)?
			)
			`), python.GetLanguage())
			if err != nil {
				return nil, errors.Wrap(err, "NewQuery")
			}
			defer query.Close()

			cursor := sitter.NewQueryCursor()
			defer cursor.Close()
			cursor.Exec(query, n)

			for {
				match, ok := cursor.NextMatch()
				if !ok {
					break
				}
				captures := getCaptures(query, match)

				// Extract module docs and Strip """ from both sides.
				modDocs := joinCaptures(content, captures["module_docs"], "\n")
				modDocs = sanitizeDocs(modDocs)
				modName = strings.ReplaceAll(strings.TrimSuffix(path, ".py"), "/", ".")

				mods[modName] = moduleInfo{path: path, docs: modDocs}
			}
		}

		// Function definitions
		{
			query, err := sitter.NewQuery([]byte(`
			(
				module
				(
				function_definition
					name: (identifier) @func_name
					parameters: (parameters) @func_params
					return_type: (type)? @func_result
					body: (block . (expression_statement (string) @func_docs)?)
				)
			)
			`), python.GetLanguage())
			if err != nil {
				return nil, errors.Wrap(err, "NewQuery")
			}
			defer query.Close()

			cursor := sitter.NewQueryCursor()
			defer cursor.Close()
			cursor.Exec(query, n)

			for {
				match, ok := cursor.NextMatch()
				if !ok {
					break
				}
				captures := getCaptures(query, match)

				funcDocs := joinCaptures(content, captures["func_docs"], "\n")
				funcDocs = sanitizeDocs(funcDocs)
				funcName := firstCaptureContentOr(content, captures["func_name"], "")
				funcParams := firstCaptureContentOr(content, captures["func_params"], "")
				funcResult := firstCaptureContentOr(content, captures["func_result"], "")

				if len(funcName) > 0 && funcName[0] == '_' && funcName[len(funcName)-1] != '_' {
					continue // unexported (private function)
				}

				funcLabel := schema.Markdown("def " + funcName + funcParams)
				if funcResult != "" {
					funcLabel = funcLabel + schema.Markdown(" -> "+funcResult)
				}
				funcs := functionsByMod[modName]
				funcs = append(funcs, schema.Section{
					ID:         funcName,
					ShortLabel: funcName,
					Label:      funcLabel,
					Detail:     schema.Markdown(funcDocs),
					SearchKey:  []string{modName, ".", funcName},
				})
				functionsByMod[modName] = funcs
			}
		}
	}

	var pages []schema.Page
	for modName, moduleInfo := range mods {
		functionsSection := schema.Section{
			ID:         "func",
			ShortLabel: "func",
			Label:      "Functions",
			SearchKey:  []string{},
			Category:   true,
			Children:   functionsByMod[modName],
		}

		pages = append(pages, schema.Page{
			Path:      moduleInfo.path,
			Title:     "Module " + modName,
			Detail:    schema.Markdown(moduleInfo.docs),
			SearchKey: []string{modName},
			Sections:  []schema.Section{functionsSection},
		})
	}

	return &schema.Index{
		SchemaVersion: schema.LatestVersion,
		Language:      schema.LanguagePython,
		NumFiles:      files,
		NumBytes:      bytes,
		Libraries: []schema.Library{
			{
				Name:        "TODO",
				Repository:  "TODO",
				ID:          "TODO",
				Version:     "TODO",
				VersionType: "TODO",
				Pages:       pages,
			},
		},
	}, nil
}

func sanitizeDocs(s string) string {
	return strings.TrimSuffix(strings.TrimPrefix(s, "\"\"\""), "\"\"\"")
}

type moduleInfo struct {
	path string
	docs string
}

func firstCaptureContentOr(content []byte, captures []*sitter.Node, defaultValue string) string {
	if len(captures) > 0 {
		return captures[0].Content(content)
	}
	return defaultValue
}

func joinCaptures(content []byte, captures []*sitter.Node, sep string) string {
	var v []string
	for _, capture := range captures {
		v = append(v, capture.Content(content))
	}
	return strings.Join(v, sep)
}

func getCaptures(q *sitter.Query, m *sitter.QueryMatch) map[string][]*sitter.Node {
	captures := map[string][]*sitter.Node{}
	for _, c := range m.Captures {
		cname := q.CaptureNameForId(c.Index)
		captures[cname] = append(captures[cname], c.Node)
	}
	return captures
}
