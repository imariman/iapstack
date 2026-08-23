---
name: iapstack-go-conventions
description: Write, edit, refactor, or review Go code in the IAPStack repository while applying its required declaration order and English documentation-comment conventions.
---

# IAPStack Go Conventions

Apply these conventions to every Go file created or changed in this repository, including production code, tests, helpers, commands, and generated-by-hand fixtures. Preserve the same conventions when reviewing or refactoring existing Go code.

## Declaration order

After the package declaration and imports, organize top-level declarations in this order:

1. Constants
2. Types, including structs and interfaces
3. Package-level variables
4. Functions and methods

Keep related constants in the same group. Separate unrelated constants or constant groups with one blank line.

Do not place a constant after a type, variable, function, or method. Do not place a type or package-level variable after executable code.

## Documentation comments

Write an English explanatory comment for every:

- Constant specification
- Function
- Method
- Named interface method

This requirement also applies to unexported declarations and test helpers.

Place each comment immediately above the declaration it documents. Prefer comments that begin with the declaration name and explain its responsibility or meaning rather than merely repeating its signature.

For a grouped `const` declaration, document each logically distinct constant specification. A comment on the `const` block alone is not a substitute for per-constant documentation.

## Working rules

- Follow the existing package architecture and provider-neutral domain boundaries.
- Run `gofmt` on changed Go files.
- Run `go test ./...` after Go changes. Use the repository's fuller quality checks when the change warrants them.
- Treat `internal/architecture/source_conventions_test.go` as the executable check for these source conventions. Update the code to satisfy it; do not weaken or bypass it to make a change pass.
