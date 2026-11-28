package g2

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquag2 "github.com/aquaproj/aqua/v2/pkg/g2"
)

// TemplateFileName is where a package served as a template has its definition, in place of
// the versions a generated package has.
const TemplateFileName = aquag2.TemplateFileName

// IsTemplate reports whether a definition declares its package served as a template.
func IsTemplate(cfg *aquag2.Config) bool {
	return cfg != nil && cfg.Template != nil
}

// templateFieldsFile is every field a template-1.json may set, one path a line, as aqua
// knew them when template-1 was introduced. See TemplateFields.
//
//go:embed template_fields.txt
var templateFieldsFile string

// templateExcludedFile is the fields aqua's definition has that template-1 doesn't allow:
// ones added after template-1 whose absence changes what an older aqua resolves. See
// TemplateExcludedFields.
//
//go:embed template_excluded_fields.txt
var templateExcludedFile string

// TemplateFields is every field template-1 allows, as paths: "url", "files[].src",
// "replacements{}", "version_overrides[].checksum.url". A path is matched as it is: a field
// below one template-1 allows isn't allowed for that.
//
// aqua reads a template with encoding/json, which drops a field it doesn't know without a
// word. A field added to aqua's definition after template-1 was introduced, used in a
// template, would therefore be dropped by every aqua older than it, and one that changes
// what is resolved would make that aqua resolve another asset and lock its checksum. So
// template-1 is the fields aqua knew then, and a template using any other is refused.
//
// A test compares the list with aqua's definition, so that updating aqua stops at every
// field it adds: one that changes nothing when it is dropped can join template-1, and one
// that changes what is resolved needs template-2, and goes in TemplateExcludedFields.
func TemplateFields() []string {
	return fieldsOf(templateFieldsFile)
}

// TemplateExcludedFields is the fields aqua's definition has that template-1 refuses: added
// to aqua after template-1, and changing what is resolved, so that an aqua older than them
// would resolve something else. They wait for template-2. Together with TemplateFields they
// are every field aqua's definition has, which is what the test holds them to: a field aqua
// gains is decided on, one way or the other, before ar2 can use that aqua.
func TemplateExcludedFields() []string {
	return fieldsOf(templateExcludedFile)
}

func fieldsOf(content string) []string {
	var fields []string
	for line := range strings.SplitSeq(content, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			fields = append(fields, line)
		}
	}
	return fields
}

// DefinitionFields is every field aqua's definition of a package has, as TemplateFields
// spells them. It is what TemplateFields is compared with.
func DefinitionFields() []string {
	fields := map[string]struct{}{}
	walkType(reflect.TypeFor[registry.PackageInfo](), "", fields, nil)
	out := make([]string, 0, len(fields))
	for field := range fields {
		out = append(out, field)
	}
	slices.Sort(out)
	return out
}

// usedFields is every field the definition sets, as TemplateFields spells them.
func usedFields(pkgInfo *registry.PackageInfo) []string {
	fields := map[string]struct{}{}
	walkValue(reflect.ValueOf(pkgInfo), "", fields)
	out := make([]string, 0, len(fields))
	for field := range fields {
		out = append(out, field)
	}
	slices.Sort(out)
	return out
}

func join(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// jsonName is the name encoding/json writes a field under, or "" for one it skips. An
// embedded struct without a name is reported as embedded, since its fields are its parent's.
func jsonName(field reflect.StructField) (string, bool) {
	tag := field.Tag.Get("json")
	if tag == "-" || !field.IsExported() {
		return "", false
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" && field.Anonymous {
		return "", true
	}
	if name == "" {
		name = field.Name
	}
	return name, false
}

func walkType(t reflect.Type, prefix string, fields map[string]struct{}, seen []reflect.Type) {
	switch t.Kind() { //nolint:exhaustive // every other kind is a leaf
	case reflect.Pointer:
		walkType(t.Elem(), prefix, fields, seen)
		return
	case reflect.Slice, reflect.Array:
		walkType(t.Elem(), prefix+"[]", fields, seen)
		return
	case reflect.Map:
		walkType(t.Elem(), prefix+"{}", fields, seen)
		return
	case reflect.Struct:
		if slices.Contains(seen, t) {
			// A type holding itself, such as an override holding overrides. Its
			// fields are listed where it first appeared.
			fields[prefix] = struct{}{}
			return
		}
		seen = append(seen, t)
		for field := range t.Fields() {
			name, embedded := jsonName(field)
			if embedded {
				walkType(field.Type, prefix, fields, seen)
				continue
			}
			if name == "" {
				continue
			}
			walkType(field.Type, join(prefix, name), fields, seen)
		}
		return
	}
	if prefix != "" {
		fields[prefix] = struct{}{}
	}
}

func walkValue(v reflect.Value, prefix string, fields map[string]struct{}) {
	switch v.Kind() { //nolint:exhaustive // every other kind is a leaf
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return
		}
		walkValue(v.Elem(), prefix, fields)
		return
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			walkValue(v.Index(i), prefix+"[]", fields)
		}
		return
	case reflect.Map:
		for _, key := range v.MapKeys() {
			walkValue(v.MapIndex(key), prefix+"{}", fields)
		}
		return
	case reflect.Struct:
		walkStruct(v, prefix, fields)
		return
	}
	if prefix != "" && !v.IsZero() {
		fields[prefix] = struct{}{}
	}
}

func walkStruct(v reflect.Value, prefix string, fields map[string]struct{}) {
	t := v.Type()
	for i := range t.NumField() {
		name, embedded := jsonName(t.Field(i))
		if embedded {
			walkValue(v.Field(i), prefix, fields)
			continue
		}
		if name == "" || v.Field(i).IsZero() {
			continue
		}
		walkValue(v.Field(i), join(prefix, name), fields)
	}
}

// ValidateTemplate checks a definition that declares its package served as a template.
//
// A template is read by aqua as it is, so what a generator would catch for a generated
// package has to be caught here: a definition that can't say what it means, and fields aqua
// would drop or that do nothing for a template.
func ValidateTemplate(cfg *aquag2.Config) error {
	tmpl := cfg.Template
	if strings.TrimSpace(tmpl.Reason) == "" {
		return errTemplateNoReason
	}
	if (len(tmpl.TestVersions) == 0) == (strings.TrimSpace(tmpl.SkipTest) == "") {
		return errTemplateTest
	}
	if cfg.VersionConstraints != "" {
		// Copied from aqua-registry, where the top is the newest definition and carries
		// a constraint. Here the top is only the base the overrides inherit from, so the
		// constraint would be ignored and the definition say something else than it
		// looks like it says.
		return errTemplateTopConstraint
	}
	if field := generatorField(cfg); field != "" {
		return fmt.Errorf("%w: %s", errTemplateGeneratorField, field)
	}
	if err := survivesJSON(cfg); err != nil {
		return err
	}
	allowedFields := map[string]struct{}{}
	for _, field := range TemplateFields() {
		allowedFields[field] = struct{}{}
	}
	for _, field := range usedFields(aquag2.NewTemplate(cfg)) {
		if _, ok := allowedFields[field]; !ok {
			return fmt.Errorf("%w: %s", errTemplateField, field)
		}
	}
	return nil
}

// survivesJSON checks that template-1.json says what the definition says. aqua's definition
// leaves an empty list or map out of JSON, and an override reads one that is absent as
// "inherit": `overrides: []` in a version_override, which clears the overrides above it,
// would come back as nothing and keep them. Such a definition is refused rather than
// written meaning something else; leaving the base without what the override cleared, and
// writing it into the overrides that need it, says the same in a form JSON keeps.
func survivesJSON(cfg *aquag2.Config) error {
	b, err := MarshalTemplate(cfg)
	if err != nil {
		return err
	}
	read, err := aquag2.ParseTemplate(b)
	if err != nil {
		return fmt.Errorf("read the rendered template back: %w", err)
	}
	if !reflect.DeepEqual(aquag2.NewTemplate(cfg), read.PackageInfo) {
		return errTemplateLostInJSON
	}
	return nil
}

// RoundTrip is the definition as aqua reads it from template-1.json, which is what a test
// of the template installs.
func RoundTrip(cfg *aquag2.Config) (*aquag2.Config, error) {
	b, err := MarshalTemplate(cfg)
	if err != nil {
		return nil, err
	}
	read, err := aquag2.ParseTemplate(b)
	if err != nil {
		return nil, fmt.Errorf("read the rendered template back: %w", err)
	}
	read.Template = cfg.Template
	return read, nil
}

// generatorField is the first field the definition sets that only generating a version
// reads -- the assets of a release, which a template has none of -- or "".
func generatorField(cfg *aquag2.Config) string {
	switch {
	case cfg.AllAssetsFilter != "":
		return "all_assets_filter"
	case len(cfg.AssetFilters) != 0:
		return "asset_filters"
	case len(cfg.AssetRules) != 0:
		return "asset_rules"
	case cfg.Prerelease:
		return "prerelease"
	}
	return ""
}

// MarshalTemplate renders what TemplateFileName holds for a definition.
func MarshalTemplate(cfg *aquag2.Config) ([]byte, error) {
	b, err := json.MarshalIndent(aquag2.NewTemplate(cfg), "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal the template: %w", err)
	}
	return append(b, '\n'), nil
}

var (
	errTemplateNoReason       = errors.New("template.reason must say why the package's versions can't be generated ahead of time")
	errTemplateTest           = errors.New("a template must have exactly one of template.test_versions and template.skip_test")
	errTemplateTopConstraint  = errors.New("a template has no version_constraint at the top: version_overrides are read newest first, and a version none of them matches takes the first")
	errTemplateGeneratorField = errors.New("a template has no release to read assets from, so this field does nothing")
	errTemplateLostInJSON     = errors.New("the template doesn't survive being written as JSON: an empty list or map, such as overrides: [] or replacements: {} in a version_override, is left out and read as inheriting the one above it")
	errTemplateField          = errors.New("the field isn't one template-1 allows: an aqua older than the field would drop it without a word")
)
