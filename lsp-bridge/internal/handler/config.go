package handler

type Config struct {
	AtmosCLIPath       string `json:"atmos_cli_path,omitempty"`
	StacksPath         string `json:"stacks_path,omitempty"`
	DiagnosticsEnabled *bool  `json:"diagnostics_enabled,omitempty"`
	LogLevel           string `json:"log_level,omitempty"`
}

func DefaultConfig() Config {
	enabled := true
	return Config{
		DiagnosticsEnabled: &enabled,
		LogLevel:           "info",
	}
}
