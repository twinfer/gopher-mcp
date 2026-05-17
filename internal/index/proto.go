package index

import (
	"go/ast"
	"go/types"
	"reflect"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/twinfer/gopher-mcp/internal/util"
)

// ProtoField is one protobuf-generated Go struct field.
type ProtoField struct {
	ProtoMessage string // e.g. "PartitionUpdate" (Go type name, also proto message name)
	ProtoField   string // e.g. "partition_id" (snake_case, from `name=` in tag)
	GoField      string // e.g. "PartitionId" (Go struct field name)
	PkgPath      string // package containing the struct
	StructQN     string // "pkgpath.PartitionUpdate"
	FieldQN      string // "pkgpath.PartitionUpdate.PartitionId" (the var QName we hand to References)
}

// ProtoFieldRef is one access site of a generated-proto struct field.
type ProtoFieldRef struct {
	File string
	Line int
	Col  int
}

// FindProtoFields locates all protobuf-generated struct fields in the packages
// matching protoPkgGlob. If protoPkgGlob is empty, every package is searched.
//
// "Protobuf-generated" means: the field has a struct tag whose `protobuf:`
// portion includes `name=<proto_name>`. This avoids requiring a protobuf
// dependency or generated descriptor.
func (s *Snapshot) FindProtoFields(protoPkgGlob string) []ProtoField {
	var out []ProtoField
	for _, pkg := range s.Pkgs {
		if pkg.Types == nil || pkg.TypesInfo == nil {
			continue
		}
		if !util.MatchPackagePath(protoPkgGlob, pkg.PkgPath) {
			continue
		}
		collectProtoFields(pkg, &out)
	}
	return out
}

func collectProtoFields(pkg *packages.Package, out *[]ProtoField) {
	for _, f := range pkg.Syntax {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || len(gd.Specs) == 0 {
				continue
			}
			for _, sp := range gd.Specs {
				ts, ok := sp.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok || st.Fields == nil {
					continue
				}
				structName := ts.Name.Name
				for _, field := range st.Fields.List {
					if field.Tag == nil {
						continue
					}
					protoName, isProto := protoFieldName(field.Tag.Value)
					if !isProto {
						continue
					}
					for _, name := range field.Names {
						if !name.IsExported() {
							continue
						}
						*out = append(*out, ProtoField{
							ProtoMessage: structName,
							ProtoField:   protoName,
							GoField:      name.Name,
							PkgPath:      pkg.PkgPath,
							StructQN:     pkg.PkgPath + "." + structName,
							FieldQN:      pkg.PkgPath + "." + structName + "." + name.Name,
						})
					}
				}
			}
		}
	}
}

// protoFieldName parses the raw struct tag literal (including back-ticks) and
// extracts the `name=<x>` value from the `protobuf:` portion, if any.
func protoFieldName(rawTag string) (string, bool) {
	// rawTag still has surrounding back-ticks from the source.
	tag, ok := unquoteRawTag(rawTag)
	if !ok {
		return "", false
	}
	pb := reflect.StructTag(tag).Get("protobuf")
	if pb == "" {
		return "", false
	}
	for part := range strings.SplitSeq(pb, ",") {
		if name, ok := strings.CutPrefix(part, "name="); ok {
			return name, true
		}
	}
	return "", false
}

func unquoteRawTag(raw string) (string, bool) {
	if len(raw) < 2 {
		return "", false
	}
	if raw[0] == '`' && raw[len(raw)-1] == '`' {
		return raw[1 : len(raw)-1], true
	}
	// Standard double-quoted tag (rare but possible).
	if raw[0] == '"' && raw[len(raw)-1] == '"' {
		// Cheap unquote: tags don't typically contain escapes.
		return raw[1 : len(raw)-1], true
	}
	return "", false
}

// ProtoFieldXRef returns every use-site of the Go struct field corresponding
// to a proto field. Lookup is by message + proto field name (snake_case) or
// Go field name (PascalCase) — either form is accepted.
func (s *Snapshot) ProtoFieldXRef(messageName, fieldName, protoPkgGlob, refPkgGlob string, limit int) (field *ProtoField, refs []ProtoFieldRef, truncated bool) {
	fields := s.FindProtoFields(protoPkgGlob)
	for i, f := range fields {
		if f.ProtoMessage != messageName {
			continue
		}
		if f.ProtoField == fieldName || f.GoField == fieldName {
			field = &fields[i]
			break
		}
	}
	if field == nil {
		return nil, nil, false
	}
	// Resolve the Go field's *types.Var via the struct's type.
	target := lookupStructField(s, field)
	if target == nil {
		return field, nil, false
	}
	for _, pkg := range s.Pkgs {
		if pkg.TypesInfo == nil {
			continue
		}
		if !util.MatchPackagePath(refPkgGlob, pkg.PkgPath) {
			continue
		}
		for id, obj := range pkg.TypesInfo.Uses {
			if obj == target {
				pos := s.Fset.Position(id.Pos())
				refs = append(refs, ProtoFieldRef{File: pos.Filename, Line: pos.Line, Col: pos.Column})
				if limit > 0 && len(refs) >= limit {
					return field, refs, true
				}
			}
		}
	}
	return field, refs, false
}

func lookupStructField(s *Snapshot, pf *ProtoField) types.Object {
	sym, ok := s.Syms.ByQN[pf.StructQN]
	if !ok || sym.Obj == nil {
		return nil
	}
	tn, ok := sym.Obj.(*types.TypeName)
	if !ok {
		return nil
	}
	st, ok := tn.Type().Underlying().(*types.Struct)
	if !ok {
		return nil
	}
	for fld := range st.Fields() {
		if fld.Name() == pf.GoField {
			return fld
		}
	}
	return nil
}
