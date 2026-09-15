// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package commentpolicy

import (
	"fmt"
	"go/ast"
	"strings"
	"unicode/utf8"

	"github.com/golangci/plugin-module-register/register"
	"golang.org/x/tools/go/analysis"
)

const (
	maxLines = 3
	maxChars = 300
)

func init() {
	register.Plugin("commentpolicy", func(any) (register.LinterPlugin, error) {
		return plugin{}, nil
	})
}

type plugin struct{}

func (plugin) GetLoadMode() string {
	return register.LoadModeSyntax
}

func (plugin) BuildAnalyzers() ([]*analysis.Analyzer, error) {
	return []*analysis.Analyzer{{
		Name: "commentpolicy",
		Doc:  fmt.Sprintf("limits inline comment blocks to %d lines and %d characters", maxLines, maxChars),
		Run:  run,
	}}, nil
}

func run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		if ast.IsGenerated(file) {
			continue
		}
		checkFile(pass, file)
	}
	return nil, nil
}

func checkFile(pass *analysis.Pass, file *ast.File) {
	var bodies []*ast.BlockStmt
	ast.Inspect(file, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.FuncDecl:
			if node.Body != nil {
				bodies = append(bodies, node.Body)
			}
		case *ast.FuncLit:
			bodies = append(bodies, node.Body)
		}
		return true
	})
	for _, group := range file.Comments {
		for _, body := range bodies {
			if body.Pos() < group.Pos() && group.End() < body.End() {
				lines, chars := commentSize(group)
				if lines > maxLines || chars > maxChars {
					pass.Reportf(group.Pos(),
						"inline comment has %d lines / %d characters (limit %d / %d); "+
							"keep the non-obvious constraint, remove narration or move extended rationale to documentation",
						lines, chars, maxLines, maxChars)
				}
				break
			}
		}
	}
}

func commentSize(group *ast.CommentGroup) (int, int) {
	lines, chars := 0, 0
	for _, comment := range group.List {
		text := comment.Text
		if content, ok := strings.CutPrefix(text, "//"); ok {
			text = strings.TrimSpace(content)
			if isDirective(text) {
				continue
			}
		} else {
			text = strings.TrimSuffix(strings.TrimPrefix(text, "/*"), "*/")
		}
		for line := range strings.SplitSeq(text, "\n") {
			line = strings.TrimSpace(line)
			lines++
			chars += utf8.RuneCountInString(line)
		}
	}
	return lines, chars
}

func isDirective(text string) bool {
	for _, prefix := range []string{"go:", "+kubebuilder:", "+k8s:", "nolint:", "lint:", "gosec:"} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return text == "nolint" || strings.HasPrefix(text, "nolint ")
}
