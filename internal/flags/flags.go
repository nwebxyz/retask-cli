// internal/flags/flags.go
package flags

// Global holds persistent flags available on every command.
type Global struct {
	Profile     string
	WorkspaceID string
	Pretty      bool
	Insecure    bool
	NoSave      bool
	ConfigPath  string
}
