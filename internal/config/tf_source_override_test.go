package config

import "testing"

// The demo .env.example declares ROKSBNKCTL_TF_SOURCE_TYPE= with an EMPTY value
// (the guard requires an uncommented declaration). An empty value must be a
// no-op, not an overwrite -- otherwise sourcing that file would blank a
// config.yaml's tf_source and every blueprint would silently fall back to the
// embedded tree.
func TestAnEmptyTFSourceOverrideDoesNotClearTheConfiguredValue(t *testing.T) {
	ws := &Workspace{}
	ws.TFSource.Type = "github"
	ws.TFSource.Repo = "jgruberf5/roksbnkctl"
	ws.TFSource.Ref = "v1.58.0"

	OverrideFromMap(ws, map[string]string{
		"ROKSBNKCTL_TF_SOURCE_TYPE": "",
		"ROKSBNKCTL_TF_SOURCE_REPO": "",
		"ROKSBNKCTL_TF_SOURCE_REF":  "",
		"ROKSBNKCTL_TF_SOURCE_PATH": "",
	})

	if ws.TFSource.Type != "github" || ws.TFSource.Repo != "jgruberf5/roksbnkctl" || ws.TFSource.Ref != "v1.58.0" {
		t.Errorf("an empty override cleared the configured tf_source: type=%q repo=%q ref=%q",
			ws.TFSource.Type, ws.TFSource.Repo, ws.TFSource.Ref)
	}
}
