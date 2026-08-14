package runneripc

type Request struct {
	Command        string   `json:"command"`
	Directory      string   `json:"directory"`
	Environment    []string `json:"environment"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
}

type Response struct {
	Type     string `json:"type"`
	Message  string `json:"message,omitempty"`
	ExitCode *int   `json:"exitCode,omitempty"`
}
