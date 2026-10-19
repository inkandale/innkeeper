package protocol

// The settings capability serves the instance form. Its calls are stateless:
// the core sends them to a process started with an empty config, and each
// carries the form's unsaved draft.
const (
	MethodSettingsDefaults = "settings.defaults"
	MethodSettingsOptions  = "settings.options"
	MethodSettingsRun      = "settings.run"
)

// SettingsDraft is the form's unsaved config and the secrets that go with it:
// the saved ones with the form's changes on top.
type SettingsDraft struct {
	Config  map[string]any    `json:"config"`
	Secrets map[string]string `json:"secrets"`
}

// SettingsDefaultsResult holds defaults the manifest can't, by JSON pointer
// into the config, such as "/prompts/tighten".
type SettingsDefaultsResult struct {
	Defaults map[string]any `json:"defaults"`
}

// SettingsOptionsParams asks for the choices of the config field at the JSON
// pointer Field.
type SettingsOptionsParams struct {
	SettingsDraft
	Field string `json:"field"`
}

// SettingsOptionsResult lists a field's choices.
type SettingsOptionsResult struct {
	Options []Option `json:"options"`
}

// Option is one choice: a settings field's, a catalog enum filter's, or a
// catalog sort order. An empty Label shows Value; a catalog requires one.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// SettingsRunParams runs one of the manifest's settings.actions.
type SettingsRunParams struct {
	SettingsDraft
	Action string `json:"action"`
}

// SettingsRunResult is what the button reports. A failed check is OK false,
// not an error.
type SettingsRunResult struct {
	OK      bool            `json:"ok"`
	Message string          `json:"message,omitempty"`
	Checks  []SettingsCheck `json:"checks,omitempty"`
}

// SettingsCheck is one row of a run's report.
type SettingsCheck struct {
	Name      string `json:"name"`
	OK        bool   `json:"ok"`
	Message   string `json:"message,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
}
