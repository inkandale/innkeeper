package protocol

// Version is the protocol version this core speaks.
const Version = 1

// Methods every plugin implements.
const (
	MethodInitialize = "initialize"
	MethodShutdown   = "shutdown"
)

// InitializeParams is sent by the core as the first request to a plugin.
type InitializeParams struct {
	Protocol    int               `json:"protocol"`
	InstanceID  string            `json:"instance_id"`
	Config      map[string]any    `json:"config"`
	Secrets     map[string]string `json:"secrets"`
	BlobStores  map[string]string `json:"blob_stores"`
	BlobTmp     string            `json:"blob_tmp"`
	DatabaseURL string            `json:"database_url"`
}

// InitializeResult is the plugin's reply to initialize.
type InitializeResult struct {
	Protocol     int      `json:"protocol"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
}
