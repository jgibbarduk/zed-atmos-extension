package handler

type config struct {
	StacksPath         string `json:"stacks_path,omitempty"`
	DiagnosticsEnabled *bool  `json:"diagnostics_enabled,omitempty"`
	LogLevel           string `json:"log_level,omitempty"`
}

func defaultConfig() config {
	enabled := true
	return config{
		DiagnosticsEnabled: &enabled,
		LogLevel:           "info",
	}
}
