package architecture_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"testing"
)

// TestGoSourceConventions verifies declaration order and required documentation comments.
func TestGoSourceConventions(t *testing.T) {
	t.Parallel()

	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}

	err = filepath.WalkDir(repositoryRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		return checkGoFile(path)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// checkGoFile parses one Go file and validates its top-level declarations.
func checkGoFile(path string) error {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, nil, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	declarationStage := 0
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.GenDecl:
			switch declaration.Tok {
			case token.CONST:
				if declarationStage > 0 {
					return fmt.Errorf("%s:%d: constants must appear before type, variable, and function declarations", path, fileSet.Position(declaration.Pos()).Line)
				}
				if err := checkConstantComments(path, fileSet, declaration); err != nil {
					return err
				}
			case token.TYPE, token.VAR:
				if declarationStage > 1 {
					return fmt.Errorf("%s:%d: type and variable declarations must appear before functions", path, fileSet.Position(declaration.Pos()).Line)
				}
				declarationStage = 1
				if declaration.Tok == token.TYPE {
					if err := checkInterfaceMethodComments(path, fileSet, declaration); err != nil {
						return err
					}
				}
			}
		case *ast.FuncDecl:
			declarationStage = 2
			if declaration.Doc == nil || len(declaration.Doc.List) == 0 {
				return fmt.Errorf("%s:%d: function or method %s requires an English comment", path, fileSet.Position(declaration.Pos()).Line, declaration.Name.Name)
			}
		}
	}

	return nil
}

// checkInterfaceMethodComments verifies documentation for named interface methods.
func checkInterfaceMethodComments(path string, fileSet *token.FileSet, declaration *ast.GenDecl) error {
	for _, specification := range declaration.Specs {
		typeSpecification, ok := specification.(*ast.TypeSpec)
		if !ok {
			continue
		}
		interfaceType, ok := typeSpecification.Type.(*ast.InterfaceType)
		if !ok {
			continue
		}
		for _, method := range interfaceType.Methods.List {
			if len(method.Names) == 0 {
				continue
			}
			if method.Doc == nil || len(method.Doc.List) == 0 {
				return fmt.Errorf("%s:%d: interface method %s requires an English comment", path, fileSet.Position(method.Pos()).Line, method.Names[0].Name)
			}
		}
	}
	return nil
}

// checkConstantComments verifies that every constant specification has its own comment.
func checkConstantComments(path string, fileSet *token.FileSet, declaration *ast.GenDecl) error {
	for _, specification := range declaration.Specs {
		valueSpecification, ok := specification.(*ast.ValueSpec)
		if !ok {
			continue
		}
		if valueSpecification.Doc == nil && declaration.Doc == nil {
			return fmt.Errorf("%s:%d: every constant requires an English comment", path, fileSet.Position(valueSpecification.Pos()).Line)
		}
	}
	return nil
}
