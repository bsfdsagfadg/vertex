// gochecks 聚合静态检查工具：覆盖 gopls 编辑器分析器可编程实现的部分，
// 供 pre-commit 门禁与本地开发使用。挂在根模块下（无独立 go.mod）。
//
// 检查项（对应 gopls 分析器）：
//   - writestring             WriteString 实参顶层字符串拼接（+）
//   - unusedparams            函数参数未使用（接口实现方法、接收者豁免）
//   - simplifycompositelit    复合字面量冗余类型
//   - modernize (b.Loop)      benchmark 循环未使用 b.Loop()
//
// 用法：
//   go run ./scripts/gochecks            # 全量检查（含测试文件）
package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"

	"golang.org/x/tools/go/packages"
)

var checkers = map[string]func(*packages.Package, *ast.File, *token.FileSet) []diag{}

type diag struct {
	file string
	line int
	msg  string
}

func main() {
	cfg := &packages.Config{
		Mode:  packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Tests: true,
	}
	targets := []string{"./internal/...", "./cmd/..."}

	pkgs, err := packages.Load(cfg, targets...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gochecks: load packages:", err)
		os.Exit(2)
	}
	if packages.PrintErrors(pkgs) > 0 {
		os.Exit(2)
	}

	all := []diag{}
	seen := map[string]bool{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			for name, fn := range checkers {
				for _, d := range fn(pkg, file, pkg.Fset) {
					key := fmt.Sprintf("%s:%d:%s", d.file, d.line, d.msg)
					if seen[key] {
						continue
					}
					seen[key] = true
					all = append(all, diag{d.file, d.line, "[" + name + "] " + d.msg})
				}
			}
		}
	}
	fmt.Fprintf(os.Stderr, "gochecks: %d packages, %d diagnostics\n", len(pkgs), len(all))
	if len(all) == 0 {
		return
	}
	for _, d := range all {
		fmt.Printf("%s:%d: %s\n", d.file, d.line, d.msg)
	}
	os.Exit(1)
}

// writestring 检查 WriteString 实参顶层的字符串拼接（+）：
// 每次 + 都会产生一个临时字符串分配，应拆分为多次 WriteString/WriteByte。
// 对应 gopls 分析器 writestring。
func init() {
	checkers["writestring"] = func(pkg *packages.Package, file *ast.File, fset *token.FileSet) []diag {
		var out []diag
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "WriteString" || len(call.Args) != 1 {
				return true
			}
			if _, isConcat := call.Args[0].(*ast.BinaryExpr); isConcat {
				pos := fset.Position(call.Pos())
				out = append(out, diag{pos.Filename, pos.Line, "inefficient string concatenation in call to WriteString"})
			}
			return true
		})
		return out
	}
}

// unusedparams 检查函数参数是否在函数体内被引用。
// 未使用的参数应改为 _（保持接口/回调签名时）或删除。
// 对应 gopls 分析器 unusedparams。
//
// 豁免规则（对齐 gopls 行为）：
//   - 接口实现方法（参数名是契约的一部分，不可随意改名）
//   - 方法接收者（gopls 不报告 receiver）
func init() {
	checkers["unusedparams"] = func(pkg *packages.Package, file *ast.File, fset *token.FileSet) []diag {
		var out []diag
		ifaces := collectInterfaces(pkg)
		ast.Inspect(file, func(n ast.Node) bool {
			fd, ok := n.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				return true
			}
			if fnObj, ok := pkg.TypesInfo.Defs[fd.Name].(*types.Func); ok && implementsInterface(fnObj, ifaces) {
				return true
			}
			for _, field := range fd.Type.Params.List {
				for _, name := range field.Names {
					if name.Name == "_" {
						continue
					}
					if countIdent(fd.Body, name.Name) == 0 {
						pos := fset.Position(name.Pos())
						out = append(out, diag{pos.Filename, pos.Line, "unused parameter: " + name.Name})
					}
				}
			}
			return true
		})
		return out
	}
}

// collectInterfaces 收集包内声明的接口与导入包导出的接口。
func collectInterfaces(pkg *packages.Package) []*types.Interface {
	var out []*types.Interface
	add := func(scope *types.Scope) {
		for _, name := range scope.Names() {
			if tn, ok := scope.Lookup(name).(*types.TypeName); ok {
				if iface, ok := tn.Type().Underlying().(*types.Interface); ok {
					out = append(out, iface)
				}
			}
		}
	}
	add(pkg.Types.Scope())
	for _, imp := range pkg.Types.Imports() {
		add(imp.Scope())
	}
	return out
}

// implementsInterface 判断方法 fn 是否实现任一接口的同名方法（参数/返回一致）。
func implementsInterface(fn *types.Func, ifaces []*types.Interface) bool {
	fnSig, ok := fn.Type().(*types.Signature)
	if !ok {
		return false
	}
	for _, iface := range ifaces {
		ms := types.NewMethodSet(iface)
		for i := 0; i < ms.Len(); i++ {
			m, ok := ms.At(i).Obj().(*types.Func)
			if !ok || m.Name() != fn.Name() {
				continue
			}
			sig, ok := m.Type().(*types.Signature)
			if !ok {
				continue
			}
			if types.Identical(fnSig.Params(), sig.Params()) && types.Identical(fnSig.Results(), sig.Results()) {
				return true
			}
		}
	}
	return false
}

func countIdent(node ast.Node, name string) int {
	n := 0
	ast.Inspect(node, func(nn ast.Node) bool {
		if id, ok := nn.(*ast.Ident); ok && id.Name == name {
			n++
		}
		return true
	})
	return n
}

// simplifycompositelit 检查复合字面量中的冗余类型：
//
//	[]T{T{a: 1}}          → []T{{a: 1}}
//	map[string][]string{"Host": []string{h}} → map[string][]string{"Host": {h}}
//
// 对应 gopls 分析器 simplifycompositelit（等价 gofmt -s）。
func init() {
	checkers["simplifycompositelit"] = func(pkg *packages.Package, file *ast.File, fset *token.FileSet) []diag {
		var out []diag
		ast.Inspect(file, func(n ast.Node) bool {
			outer, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			outerType := pkg.TypesInfo.TypeOf(outer)
			elemType := compositeElemType(outerType)
			for _, elt := range outer.Elts {
				inner, ok := elt.(*ast.CompositeLit)
				if !ok || inner.Type == nil {
					continue
				}
				innerType := pkg.TypesInfo.TypeOf(inner)
				if elemType != nil && innerType != nil && types.Identical(elemType, innerType) {
					pos := fset.Position(inner.Pos())
					out = append(out, diag{pos.Filename, pos.Line, "redundant type from array, slice, or map composite literal"})
				}
			}
			return true
		})
		return out
	}
}

// compositeElemType 返回复合字面量外层类型的元素（数组/切片 → 元素；map → value）。
// 嵌套字面量可省略显式类型的条件是：其类型与外层元素类型一致。
func compositeElemType(t types.Type) types.Type {
	if t == nil {
		return nil
	}
	switch tt := t.(type) {
	case *types.Array:
		return tt.Elem()
	case *types.Slice:
		return tt.Elem()
	case *types.Map:
		return tt.Elem()
	}
	return nil
}

// modernize 检查 benchmark 循环是否使用 Go 1.24+ 的 b.Loop()：
//
//	for range b.N            → for b.Loop()
//	for i := 0; i < b.N; i++ → for b.Loop()
//
// 对应 gopls 分析器 modernize。
func init() {
	checkers["modernize"] = func(pkg *packages.Package, file *ast.File, fset *token.FileSet) []diag {
		var out []diag
		ast.Inspect(file, func(n ast.Node) bool {
			switch stmt := n.(type) {
			case *ast.RangeStmt:
				if sel, ok := stmt.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "N" {
					if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "b" {
						pos := fset.Position(stmt.Pos())
						out = append(out, diag{pos.Filename, pos.Line, "b.N loop can be modernized using b.Loop()"})
					}
				}
			case *ast.ForStmt:
				if isBNLoop(stmt) {
					pos := fset.Position(stmt.Pos())
					out = append(out, diag{pos.Filename, pos.Line, "b.N loop can be modernized using b.Loop()"})
				}
			}
			return true
		})
		return out
	}
}

// isBNLoop 匹配 for i := 0; i < b.N; i++ 模式。
func isBNLoop(stmt *ast.ForStmt) bool {
	if stmt.Init == nil || stmt.Cond == nil || stmt.Post == nil {
		return false
	}
	if _, ok := stmt.Init.(*ast.AssignStmt); !ok {
		return false
	}
	if _, ok := stmt.Post.(*ast.IncDecStmt); !ok {
		return false
	}
	bin, ok := stmt.Cond.(*ast.BinaryExpr)
	if !ok || bin.Op != token.LSS {
		return false
	}
	sel, ok := bin.Y.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "N" {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == "b"
}