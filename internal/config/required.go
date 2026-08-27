package config

import "strings"

// RequiredConfigFields are the config.yaml fields `init` refuses to proceed
// without, as dotted paths.
//
// ONE LIST, TWO READERS. missingRequiredConfigFields in internal/cli reports
// them to the operator; the cheatsheet generator marks them on the page. A
// second list would be a second thing to keep true, which is the defect the
// generated cheatsheet exists to avoid — so it would be a poor place to
// introduce one.
//
// Requiredness is NOT derivable from the yaml tag. `omitempty` is a marshalling
// directive: it says a zero value is omitted from output, not that a value must
// be supplied. Deriving from it marked 25 fields required when four are, and
// missed `prefix` — whose absence is the most common `init` failure — because it
// happens to carry omitempty. The two properties are unrelated and only look
// similar.
var RequiredConfigFields = []string{
	"ibmcloud.region",
	"ibmcloud.resource_group",
	"prefix",
	"tf_source.type",
}

// MissingRequiredFields returns the RequiredConfigFields absent from ws.
//
// The switch is exhaustive over RequiredConfigFields and panics on an unhandled
// entry rather than silently skipping it: a field added to the list above and
// not here would otherwise be advertised as required and never enforced, which
// is the same disagreement in a new place.
func MissingRequiredFields(ws *Workspace) []string {
	if ws == nil {
		return append([]string(nil), RequiredConfigFields...)
	}
	var missing []string
	for _, f := range RequiredConfigFields {
		var v string
		switch f {
		case "ibmcloud.region":
			v = ws.IBMCloud.Region
		case "ibmcloud.resource_group":
			v = ws.IBMCloud.ResourceGroup
		case "prefix":
			v = ws.Prefix
		case "tf_source.type":
			v = ws.TFSource.Type
		default:
			panic("RequiredConfigFields lists " + f + " but MissingRequiredFields does not check it")
		}
		if strings.TrimSpace(v) == "" {
			missing = append(missing, f)
		}
	}
	return missing
}
