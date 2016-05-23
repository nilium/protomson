package main

import (
	"fmt"
	"strings"
	"text/template"
	"unicode"

	"github.com/golang/protobuf/proto"

	dsc "go.spiff.io/protomson/cmd/internal/descriptor"
	plg "go.spiff.io/protomson/cmd/internal/plugin"
)

const txMessageTemplate = `{{- $ctx := . -}}
{{ range .Messages.ByScope -}}
{{- if .ToGenerate -}}
{{- if .IsMessage }}
{{ with $msg := . -}}
## {{ .Scope }} (object)
{{- with $coms := join "\n" .LeadingComments .TrailingComments }}
{{ $coms }}

### Properties
{{- end -}}
{{- range $i, $field := .Proto.GetField }}
+ {{ $field.GetName }} (
	{{- with $ctx.Find $msg $field.GetTypeName -}}
		{{ .Scope }}
	{{- else -}}
		{{ $ctx.TypeNameOf $field }}
	{{- end -}}
	, optional)
	{{- with $fmsg := index $ctx.Messages.ByMessage $field -}}
		{{- with $coms := join "\n    " $fmsg.LeadingComments $fmsg.TrailingComments -}}
			{{/*space*/}} -
    {{ $coms }}
		{{- end }}
	{{- end }}
{{- end }}
{{ end -}}
{{- else if .IsEnum }}
## {{ .Scope }} (enum)
{{- with $coms := join "\n" .LeadingComments .TrailingComments }}
{{ $coms }}
{{ end }}
# Members
{{- range .Proto.GetValue }}
+ {{Q}}{{ .GetName }}{{Q}}
{{- end }}
{{ end -}}
{{ end -}}
{{ end -}}
`

var txFuncs = template.FuncMap{
	"camelcase": camelcase,
	"join": func(sep string, sets ...[]string) string {
		var s []string
		for _, t := range sets {
			if len(t) > 0 {
				s = append(s, strings.Join(t, sep))
			}
		}
		return strings.Join(s, sep)
	},
	"Q": func() string { return "`" },
}

var txMessage = template.Must(template.New("message").Funcs(txFuncs).Parse(txMessageTemplate))

func camelcase(str string) string {
	up := false
	return strings.Map(func(r rune) rune {
		if r == '_' {
			up = true
			return -1
		}

		if up {
			r = unicode.ToUpper(r)
			up = false
		}
		return r
	}, str)
}

type Context struct {
	Request  *plg.CodeGeneratorRequest
	Messages *Messages
}

func (c *Context) Find(from *Message, scope string) (result *Message) {
	if scope == "" {
		return nil
	}
	sc, abs := ParseScope(scope)
	if abs {
		m := sc.Resolve(c.Request)
		return c.Messages.ByMessage[m]
	} else {
		for from != nil {
			m := sc.Resolve(from.Proto)
			if m != nil {
				return c.Messages.ByMessage[m]
			}

			from = from.Parent
		}
	}
	return nil
}

func (c *Context) TypeNameOf(p proto.Message) string {
	f, ok := p.(*dsc.FieldDescriptorProto)
	if !ok {
		return ""
	}

	if n := f.GetTypeName(); n != "" {
		if n[0] == '.' {
			return n[1:]
		}
		return n
	}

	switch t := f.GetType(); t {
	case dsc.FieldDescriptorProto_TYPE_DOUBLE,
		dsc.FieldDescriptorProto_TYPE_FLOAT,
		dsc.FieldDescriptorProto_TYPE_INT64,
		dsc.FieldDescriptorProto_TYPE_UINT64,
		dsc.FieldDescriptorProto_TYPE_INT32,
		dsc.FieldDescriptorProto_TYPE_FIXED64,
		dsc.FieldDescriptorProto_TYPE_FIXED32,
		dsc.FieldDescriptorProto_TYPE_UINT32,
		dsc.FieldDescriptorProto_TYPE_SFIXED32,
		dsc.FieldDescriptorProto_TYPE_SFIXED64,
		dsc.FieldDescriptorProto_TYPE_SINT32,
		dsc.FieldDescriptorProto_TYPE_SINT64:
		return "number"
	case dsc.FieldDescriptorProto_TYPE_BOOL:
		return "boolean"
	case dsc.FieldDescriptorProto_TYPE_STRING:
		return "string"
	case dsc.FieldDescriptorProto_TYPE_MESSAGE:
		return "object"
	case dsc.FieldDescriptorProto_TYPE_BYTES:
		return "string"
	case dsc.FieldDescriptorProto_TYPE_ENUM:
		return "enum"
	default:
		panic(fmt.Sprint("unhandled type: ", t))
	}
}
