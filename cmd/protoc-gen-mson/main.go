package main

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/proto"
	dsc "go.spiff.io/protomson/cmd/internal/descriptor"
	plg "go.spiff.io/protomson/cmd/internal/plugin"
)

var allowAll = os.Getenv("allow_all") == "T"

type UninterpretedOptions interface {
	GetUninterpretedOption() []*dsc.UninterpretedOption
}

func GetByLocation(fi *dsc.FileDescriptorProto, loc []int32) (Scope, proto.Message, []int32) {
	var p proto.Message = fi
	sc := Scope{}

	for p != nil {

		switch p := p.(type) {
		case interface {
			GetPackage() string
		}:
			if pkg := p.GetPackage(); pkg != "" {
				sc = sc.With(pkg)
			}
		case interface {
			GetName() string
		}:
			sc = sc.With(p.GetName())
		case UninterpretedOptions:
			sc = sc.With("<UninterpretedOptions>")
		default:
			sc = sc.With(fmt.Sprintf("<unknown:%T>", p))
		}

		if len(loc) == 0 {
			break
		}

		var id, idx int32 = loc[0], -1
		if len(loc) > 1 {
			idx = loc[1]
		}
		switch d := p.(type) {
		case *dsc.FileDescriptorProto:
			switch id {
			case 4:
				p = d.GetMessageType()[idx]
			case 5:
				p = d.GetEnumType()[idx]
			case 6:
				p = d.GetService()[idx]
			case 7:
				p = d.GetExtension()[idx]
			case 8:
				p = d.GetOptions()
				loc = loc[1:]
				continue
			case 9:
				p = d.GetSourceCodeInfo()
				loc = loc[1:]
				continue
			default:
				return sc, p, loc
			}
		case *dsc.EnumDescriptorProto:
			switch id {
			case 2:
				p = d.GetValue()[idx]
			case 3:
				p = d.GetOptions()
				loc = loc[1:]
				continue
			default:
				return sc, p, loc
			}
		case *dsc.EnumValueDescriptorProto:
			switch id {
			case 3:
				p = d.GetOptions()
				loc = loc[1:]
				continue
			default:
				return sc, p, loc
			}
		case *dsc.MethodDescriptorProto:
			switch id {
			case 4:
				p = d.GetOptions()
				loc = loc[1:]
				continue
			default:
				return sc, p, loc
			}
		case *dsc.DescriptorProto:
			switch id {
			case 2:
				p = d.GetField()[idx]
			case 6:
				p = d.GetExtension()[idx]
			case 3:
				p = d.GetNestedType()[idx]
			case 4:
				p = d.GetEnumType()[idx]
			case 5:
				p = d.GetExtensionRange()[idx]
			case 8:
				p = d.GetOneofDecl()[idx]
			case 7:
				p = d.GetOptions()
				loc = loc[1:]
				continue
			default:
				return sc, p, loc
			}
		case *dsc.ServiceDescriptorProto:
			switch id {
			case 2:
				p = d.GetMethod()[idx]
			case 3:
				p = d.GetOptions()
				loc = loc[1:]
				continue
			default:
				return sc, p, loc
			}
		case *dsc.FieldDescriptorProto:
			switch id {
			case 8:
				p = d.GetOptions()
				loc = loc[1:]
				continue
			default:
				return sc, p, loc
			}
		case UninterpretedOptions:
			switch id {
			case 999:
			default:
				return sc, p, loc
			}
		default:
			return sc, p, loc
		}
		loc = loc[2:]
	}

	return sc, p, loc
}

type Descriptor interface {
	proto.Message

	GetName() string
}

type Message struct {
	Scope      Scope
	Proto      Descriptor
	Parent     *Message
	ToGenerate bool

	LeadingDetachedComments []string
	LeadingComments         []string
	TrailingComments        []string
}

func (m *Message) addComments(loc *dsc.SourceCodeInfo_Location) {
	if c := loc.GetLeadingComments(); c != "" {
		m.LeadingComments = append(m.LeadingComments, normalizeIndent(c))
	}
	if c := loc.GetTrailingComments(); c != "" {
		m.TrailingComments = append(m.TrailingComments, normalizeIndent(c))
	}
	if c := loc.GetLeadingDetachedComments(); len(c) != 0 {
		n := len(m.LeadingDetachedComments)
		m.LeadingDetachedComments = append(m.LeadingDetachedComments, c...)
		for i := n; i < len(m.LeadingDetachedComments); i++ {
			m.LeadingDetachedComments[i] = normalizeIndent(m.LeadingDetachedComments[i])
		}
	}
}

func (m *Message) IsMessage() bool {
	_, ok := m.Proto.(*dsc.DescriptorProto)
	return ok
}

func (m *Message) IsService() bool {
	_, ok := m.Proto.(*dsc.ServiceDescriptorProto)
	return ok
}

func (m *Message) IsMethod() bool {
	_, ok := m.Proto.(*dsc.MethodDescriptorProto)
	return ok
}

func (m *Message) IsField() bool {
	_, ok := m.Proto.(*dsc.FieldDescriptorProto)
	return ok
}

func (m *Message) IsEnum() bool {
	_, ok := m.Proto.(*dsc.EnumDescriptorProto)
	return ok
}

func (m *Message) IsEnumValue() bool {
	_, ok := m.Proto.(*dsc.EnumValueDescriptorProto)
	return ok
}

func GetMessages(toGenerate bool, root proto.Message) (msgs *Messages) {
	msgs = &Messages{
		ByScope:   map[string]*Message{},
		ByMessage: map[proto.Message]*Message{},
	}
	switch p := root.(type) {
	case *plg.CodeGeneratorRequest:
		msgs.walk(toGenerate, p)
	case *dsc.FileDescriptorProto:
		msgs.walkFile(toGenerate, p)
	case *dsc.DescriptorProto:
		msgs.walkMessage(toGenerate, nil, Scope{}, p)
	case *dsc.EnumDescriptorProto:
		msgs.walkEnum(toGenerate, nil, Scope{}, p)
	case Descriptor:
		msgs.walkDescriptor(toGenerate, nil, Scope{}, p)
	}
	return msgs
}

func (m *Message) String() string {
	return m.Scope.String()
}

type Messages struct {
	ByScope   map[string]*Message
	ByMessage map[proto.Message]*Message
}

func (ms *Messages) walk(toGenerate bool, root *plg.CodeGeneratorRequest) {
	togen := root.GetFileToGenerate()
	allowed := make(map[string]struct{}, len(togen))
	for _, fi := range togen {
		allowed[fi] = struct{}{}
	}

	for _, fi := range root.GetProtoFile() {
		_, ok := allowed[fi.GetName()]
		ms.walkFile(toGenerate && ok, fi)
	}
}

func (ms *Messages) put(m *Message) {
	ms.ByScope[m.Scope.String()] = m
	ms.ByMessage[m.Proto] = m
}

func (ms *Messages) walkFile(toGenerate bool, fi *dsc.FileDescriptorProto) {
	var scope Scope
	if pkg := fi.GetPackage(); pkg != "" {
		scope = Scope{pkg}
	}

	var self *Message
	if fi.GetPackage() != "" {
		self = &Message{Scope: scope, Proto: fi, ToGenerate: toGenerate}
		ms.put(self)
	}

	for _, d := range fi.GetMessageType() {
		ms.walkMessage(toGenerate, self, scope, d)
	}

	for _, nd := range fi.GetEnumType() {
		ms.walkEnum(toGenerate, self, scope, nd)
	}
}

func (ms *Messages) walkMessage(toGenerate bool, parent *Message, scope Scope, d *dsc.DescriptorProto) {
	scope = scope.With(d.GetName())
	self := &Message{Scope: scope, Proto: d, Parent: parent, ToGenerate: toGenerate}
	ms.put(self)
	for _, nd := range d.GetNestedType() {
		ms.walkMessage(toGenerate, self, scope, nd)
	}

	for _, nd := range d.GetEnumType() {
		ms.walkEnum(toGenerate, self, scope, nd)
	}

	for _, nd := range d.GetField() {
		ms.walkDescriptor(toGenerate, self, scope, nd)
	}
}

func (ms *Messages) walkEnum(toGenerate bool, parent *Message, scope Scope, d *dsc.EnumDescriptorProto) {
	scope = scope.With(d.GetName())
	self := &Message{Scope: scope, Proto: d, Parent: parent, ToGenerate: toGenerate}
	ms.put(self)

	for _, nd := range d.GetValue() {
		ms.walkDescriptor(toGenerate, self, scope, nd)
	}
}

func (ms *Messages) walkDescriptor(toGenerate bool, parent *Message, scope Scope, d Descriptor) {
	scope = scope.With(d.GetName())
	self := &Message{Scope: scope, Proto: d, Parent: parent, ToGenerate: toGenerate}
	ms.put(self)
}

type Scope []string

func ispkgname(s string) bool {
	for _, r := range s {
		if !(unicode.IsLower(r) || unicode.IsNumber(r) || r == '_') {
			return false
		}
	}
	return true
}

func ParseScope(s string) (scope Scope, absolute bool) {
	if s == "" {
		return nil, false
	}
	if s[0] == '.' {
		return parseAbsoluteScope(s[1:]), true
	}
	return Scope(strings.Split(s, ".")), false
}

func parseAbsoluteScope(s string) Scope {
	// Strip root .
	spl := strings.Split(s, ".")
	var pkg []string
	var sc Scope

	for _, r := range spl {
		if !ispkgname(r) {
			break
		}
		pkg = append(pkg, r)
	}

	if len(pkg) > 0 {
		sc = append(sc, strings.Join(pkg, "."))
	}
	sc = append(sc, spl[len(pkg):]...)

	return sc
}

func (s Scope) String() string {
	return strings.Join(s, ".")
}

func (s Scope) With(next ...string) Scope {
	d := make([]string, len(s)+len(next))
	copy(d[copy(d, s):], next)
	return Scope(d)
}

func (s Scope) Resolve(root proto.Message) proto.Message {
	if root, ok := root.(*plg.CodeGeneratorRequest); ok {
		for _, fi := range root.GetProtoFile() {
			if msg := s.Resolve(fi); msg != nil {
				return msg
			}
		}
	}

	if len(s) == 0 {
		return root
	}

	switch root := root.(type) {
	case *dsc.FileDescriptorProto:
		if pkg := root.GetPackage(); pkg != "" {
			if pkg != s[0] {
				return nil
			}
			s = s[1:]
		}

		if len(s) == 0 {
			return root
		}

		for _, m := range root.GetMessageType() {
			if msg := s.Resolve(m); msg != nil {
				return msg
			}
		}

		for _, m := range root.GetEnumType() {
			if msg := s.Resolve(m); msg != nil {
				return msg
			}
		}

		for _, m := range root.GetService() {
			if msg := s.Resolve(m); msg != nil {
				return msg
			}
		}

		for _, m := range root.GetExtension() {
			if msg := s.Resolve(m); msg != nil {
				return msg
			}
		}

	case *dsc.DescriptorProto:
		if root.GetName() != s[0] {
			return nil
		}

		s = s[1:]
		if len(s) == 0 {
			return root
		}

		for _, m := range root.GetField() {
			if msg := s.Resolve(m); msg != nil {
				return msg
			}
		}

		for _, m := range root.GetNestedType() {
			if msg := s.Resolve(m); msg != nil {
				return msg
			}
		}

		for _, m := range root.GetEnumType() {
			if msg := s.Resolve(m); msg != nil {
				return msg
			}
		}

		for _, m := range root.GetExtension() {
			if msg := s.Resolve(m); msg != nil {
				return msg
			}
		}

	case *dsc.EnumDescriptorProto:
		if root.GetName() != s[0] {
			return nil
		}

		s = s[1:]
		if len(s) == 0 {
			return root
		}

		for _, m := range root.GetValue() {
			if msg := s.Resolve(m); msg != nil {
				return msg
			}
		}

	case *dsc.EnumValueDescriptorProto:
		if root.GetName() != s[0] {
			return nil
		}

		s = s[1:]
		if len(s) == 0 {
			return root
		}

	case *dsc.ServiceDescriptorProto:
		if root.GetName() != s[0] {
			return nil
		}

		s = s[1:]
		if len(s) == 0 {
			return root
		}

		for _, m := range root.GetMethod() {
			if msg := s.Resolve(m); msg != nil {
				return msg
			}
		}

	case *dsc.MethodDescriptorProto:
		if root.GetName() != s[0] {
			return nil
		}

		s = s[1:]
		if len(s) == 0 {
			return root
		}

	case *dsc.FieldDescriptorProto:
		if root.GetName() != s[0] {
			return nil
		}

		s = s[1:]
		if len(s) == 0 {
			return root
		}
	}

	return nil
}

func debugmsg(msg proto.Message, prefix ...interface{}) {
	m := jsonpb.Marshaler{Indent: "    "}
	ms, err := m.MarshalToString(msg)
	if err != nil {
		ms = "<error: " + err.Error() + ">"
	}
	if len(prefix) == 0 {
		log.Println(ms)
	} else {
		log.Print(fmt.Sprint(prefix...), ": ", ms)
	}
}

func readmsg(dst proto.Message, r io.Reader) error {
	msgbuf, err := ioutil.ReadAll(r)
	if err != nil {
		return err
	}
	return proto.Unmarshal(msgbuf, dst)
}

func main() {
	log.SetPrefix("protoc-gen-apibmson: ")
	log.SetFlags(0)

	req := new(plg.CodeGeneratorRequest)
	if err := readmsg(req, os.Stdin); err != nil {
		log.Panic(err)
	}

	resp := &plg.CodeGeneratorResponse{}
	toGenerate := make(map[string]struct{})
	for _, fi := range req.GetFileToGenerate() {
		toGenerate[fi] = struct{}{}
	}

	for _, fi := range req.GetProtoFile() {
		_, ok := toGenerate[fi.GetName()]
		if !(ok || allowAll) {
			continue
		}

		outfile := &plg.CodeGeneratorResponse_File{
			Name: proto.String(strings.TrimSuffix(filepath.Base(fi.GetName()), ".proto") + ".pb.apib"),
		}

		messages := GetMessages(true, fi)
		for _, loc := range fi.GetSourceCodeInfo().GetLocation() {
			sc, msg, trailing := GetByLocation(fi, loc.GetPath())
			_, ok := msg.(Descriptor)
			if !ok || len(trailing) > 0 {
				continue
			}

			if (len(loc.GetTrailingComments()) + len(loc.GetLeadingComments()) + len(loc.GetLeadingDetachedComments())) == 0 {
				continue
			}

			if m, ok := messages.ByScope[sc.String()]; ok {
				m.addComments(loc)

				for _, c := range m.TrailingComments {
					if strings.TrimSpace(c) == "private" {
						log.Println("Removing message", sc, "from scope")
						delete(messages.ByScope, sc.String())
						delete(messages.ByMessage, m.Proto)
					}
				}
			}
		}

		ctx := &Context{req, messages}

		var buf bytes.Buffer
		err := txMessage.Execute(&buf, ctx)
		if err != nil {
			log.Panic(err)
		}

		outfile.Content = proto.String(buf.String())

		resp.File = append(resp.File, outfile)
	}

	out, err := proto.Marshal(resp)
	if err != nil {
		log.Panic(err)
	}

	os.Stdout.Write(out)
}

func normalizeIndent(s string) string {
	const defaultIndent = 2 << 31
	s = strings.Replace(s, "\t", "    ", -1)
	lines := strings.Split(strings.TrimRight(s, " \n"), "\n")
	minIndent := defaultIndent

	n := 0
	for ; n < len(lines) && strings.TrimSpace(lines[n]) == ""; n++ {
	}
	lines = lines[n:]

	for _, line := range lines {
		if line == "" {
			continue
		}
		first := strings.IndexFunc(line, func(r rune) bool { return r != ' ' })

		if first <= 0 {
			minIndent = 0
			break
		}

		if first < minIndent {
			minIndent = first
		}
	}

	if minIndent == defaultIndent || minIndent <= 0 {
		return strings.Join(lines, "\n")
	}

	for i, line := range lines {
		if line == "" {
			continue
		}
		lines[i] = line[minIndent:]
	}

	return strings.Join(lines, "\n")
}
