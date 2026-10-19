package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// A minimal HCL emitter.
//
// It is written by hand rather than with hashicorp/hcl: the subset needed here
// (blocks, scalars, lists and unquoted references) takes about a hundred lines,
// and it keeps hclwrite and its cty dependency tree out of the CLI.

// hclValue is either a literal (quoted) or a reference (emitted bare).
type hclValue struct {
	raw       string
	reference bool
}

// hclRef is a Terraform expression such as `nemau_network.app.id`, emitted
// without quotes.
//
// References make an export replayable in another project: unlike a literal
// id, `nemau_network.app.id` still resolves after a `terraform apply` into an
// empty project.
func hclRef(expression string) hclValue { return hclValue{raw: expression, reference: true} }

// hclBlock is one `resource "type" "name" { … }`.
type hclBlock struct {
	kind       string // resource, import, provider…
	labels     []string
	attributes []hclAttribute
	blocks     []hclBlock
	comment    string
}

type hclAttribute struct {
	name  string
	value any // string, int, bool, []string, hclValue
}

func (b *hclBlock) set(name string, value any) {
	if value == nil {
		return
	}
	switch typed := value.(type) {
	case string:
		if typed == "" {
			return
		}
	case []string:
		if len(typed) == 0 {
			return
		}
	case int:
		if typed == 0 {
			return
		}
	}
	b.attributes = append(b.attributes, hclAttribute{name: name, value: value})
}

// setAlways emits an attribute even at its zero value, for the required ones
// where absence would produce an invalid configuration rather than a default.
func (b *hclBlock) setAlways(name string, value any) {
	b.attributes = append(b.attributes, hclAttribute{name: name, value: value})
}

func (b *hclBlock) render(out *strings.Builder, indent string) {
	if b.comment != "" {
		for _, line := range strings.Split(b.comment, "\n") {
			fmt.Fprintf(out, "%s# %s\n", indent, line)
		}
	}
	out.WriteString(indent)
	out.WriteString(b.kind)
	for _, label := range b.labels {
		fmt.Fprintf(out, " %q", label)
	}
	out.WriteString(" {\n")

	width := 0
	for _, attribute := range b.attributes {
		if len(attribute.name) > width {
			width = len(attribute.name)
		}
	}
	for _, attribute := range b.attributes {
		fmt.Fprintf(out, "%s  %-*s = %s\n", indent, width, attribute.name, hclLiteral(attribute.value))
	}
	for i := range b.blocks {
		out.WriteString("\n")
		b.blocks[i].render(out, indent+"  ")
	}
	out.WriteString(indent)
	out.WriteString("}\n")
}

func hclLiteral(value any) string {
	switch typed := value.(type) {
	case hclValue:
		if typed.reference {
			return typed.raw
		}
		return strconv.Quote(typed.raw)
	case string:
		return strconv.Quote(typed)
	case bool:
		return strconv.FormatBool(typed)
	case int:
		return strconv.Itoa(typed)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case int64:
		return strconv.FormatInt(typed, 10)
	case []string:
		quoted := make([]string, 0, len(typed))
		for _, item := range typed {
			quoted = append(quoted, strconv.Quote(item))
		}
		return "[" + strings.Join(quoted, ", ") + "]"
	default:
		return strconv.Quote(fmt.Sprint(typed))
	}
}

// hclDocument accumulates blocks and the identifiers already handed out.
type hclDocument struct {
	blocks []hclBlock
	taken  map[string]bool
	// address maps a platform resource id to its HCL address, so a later
	// resource can reference it instead of repeating a literal id.
	address map[string]string
}

func newHCLDocument() *hclDocument {
	return &hclDocument{taken: map[string]bool{}, address: map[string]string{}}
}

// name turns a platform name into a Terraform identifier, unique within the
// document.
//
// Terraform identifiers accept letters, digits, underscores and dashes, and may
// not start with a digit. Platform resource names have no such constraint, so
// other characters become underscores.
func (d *hclDocument) name(kind, raw string) string {
	var builder strings.Builder
	for _, r := range raw {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-':
			builder.WriteRune(r)
		default:
			builder.WriteRune('_')
		}
	}
	candidate := strings.Trim(builder.String(), "_-")
	if candidate == "" {
		candidate = "unnamed"
	}
	if r := rune(candidate[0]); unicode.IsDigit(r) {
		candidate = "r_" + candidate
	}

	key := kind + "." + candidate
	if !d.taken[key] {
		d.taken[key] = true
		return candidate
	}
	for suffix := 2; ; suffix++ {
		numbered := fmt.Sprintf("%s_%d", candidate, suffix)
		key := kind + "." + numbered
		if !d.taken[key] {
			d.taken[key] = true
			return numbered
		}
	}
}

// add records a resource block and, when id is non-empty, the address other
// resources may reference it by.
func (d *hclDocument) add(kind, name, id string, build func(*hclBlock)) string {
	block := hclBlock{kind: "resource", labels: []string{kind, name}}
	build(&block)
	d.blocks = append(d.blocks, block)
	address := kind + "." + name
	if id != "" {
		d.address[id] = address
	}
	return address
}

// ref returns a reference to a previously exported resource's id, or the
// literal id when that resource is not part of the export.
//
// The fallback covers resources this command does not model: a literal id keeps
// the configuration valid, where a dangling reference would fail at plan time.
func (d *hclDocument) ref(id string) hclValue {
	if address, ok := d.address[id]; ok {
		return hclRef(address + ".id")
	}
	return hclValue{raw: id}
}

func (d *hclDocument) render(header string) string {
	var out strings.Builder
	out.WriteString(header)
	for i := range d.blocks {
		out.WriteString("\n")
		d.blocks[i].render(&out, "")
	}
	return out.String()
}

// importBlocks renders Terraform 1.5 `import` blocks for everything exported,
// so the generated configuration adopts the live resources instead of trying to
// create duplicates.
func (d *hclDocument) importBlocks() string {
	type entry struct{ address, id string }
	entries := make([]entry, 0, len(d.address))
	for id, address := range d.address {
		entries = append(entries, entry{address: address, id: id})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].address < entries[j].address })

	var out strings.Builder
	out.WriteString(`# Terraform 1.5+ import blocks.
#
# Applying the exported configuration against the project it came from would
# create duplicates: these blocks make it adopt the existing resources instead.
#
#     terraform plan     # should report imports, no creations
#     terraform apply
#
# Once the state is built, this file can be deleted.
`)
	for _, item := range entries {
		out.WriteString("\nimport {\n")
		fmt.Fprintf(&out, "  to = %s\n", item.address)
		fmt.Fprintf(&out, "  id = %q\n", item.id)
		out.WriteString("}\n")
	}
	return out.String()
}
